package storage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// getFilePath 查询远端文件路径与体积。路径只在本类型内部使用，不会出现在错误里。
func (t *Telegram) getFilePath(ctx context.Context, fileID string) (string, int64, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return "", 0, ErrFileUnavailable
	}

	endpoint := t.apiBase + "/bot" + t.token + "/getFile?file_id=" + url.QueryEscape(fileID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", 0, ErrUpstream
	}

	response, err := t.client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", 0, ctxErr
		}
		return "", 0, errTransport
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		_ = response.Body.Close()
	}()

	var payload apiResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return "", 0, ErrUpstream
	}
	if !payload.OK {
		// 文件标识失效或格式不对时按不可用处理，由调用方返回 404。
		if payload.ErrorCode == http.StatusNotFound || payload.ErrorCode == http.StatusBadRequest {
			return "", 0, ErrFileUnavailable
		}
		return "", 0, t.errorFor(payload)
	}

	var file struct {
		FilePath string `json:"file_path"`
		FileSize int64  `json:"file_size"`
	}
	if err := json.Unmarshal(payload.Result, &file); err != nil || strings.TrimSpace(file.FilePath) == "" {
		return "", 0, ErrUpstream
	}
	return strings.TrimLeft(strings.TrimSpace(file.FilePath), "/"), file.FileSize, nil
}

// openDownload 按文件路径打开下载流，返回的响应体由调用方负责关闭。
func (t *Telegram) openDownload(ctx context.Context, path string) (*http.Response, error) {
	return t.openDownloadRange(ctx, path, -1, -1)
}

// openDownloadRange 在 openDownload 基础上带上区间请求头；start 为负表示整段。
func (t *Telegram) openDownloadRange(ctx context.Context, path string, start, end int64) (*http.Response, error) {
	path = strings.TrimLeft(strings.TrimSpace(path), "/")
	if path == "" {
		return nil, ErrFileUnavailable
	}

	endpoint := t.apiBase + "/file/bot" + t.token + "/" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, ErrUpstream
	}
	if start >= 0 {
		spec := "bytes=" + strconv.FormatInt(start, 10) + "-"
		if end >= start {
			spec += strconv.FormatInt(end, 10)
		}
		req.Header.Set("Range", spec)
	}

	response, err := t.client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, errTransport
	}
	return response, nil
}

// resolvePath 优先读缓存，缓存缺失时查询上游并写入。
func (t *Telegram) resolvePath(ctx context.Context, fileID string) (string, int64, error) {
	now := t.now()
	if entry, ok := t.cache.get(fileID, now); ok {
		return entry.path, entry.size, nil
	}

	path, size, err := t.getFilePath(ctx, fileID)
	if err != nil {
		return "", 0, err
	}
	t.cache.set(fileID, pathEntry{path: path, size: size, expires: now.Add(t.cache.ttl)}, now)
	return path, size, nil
}

// OpenStream 查询文件路径并打开下载流，不把文件整体载入内存。
// 缓存里的路径可能已失效：遇到 404 时刷新路径并只重试一次。
// VerifyFile 用当前凭据确认一个远端文件可读，只查询文件信息，不下载内容。
// imageTypeFromPath 按文件扩展名推断图片类型，无法判断时返回空串。
func imageTypeFromPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".mp4":
		return "video/mp4"
	default:
		return ""
	}
}

// Telegram 的 file_id 只对上传它的 Bot 有效，因此可用于判断旧图片是否属于当前 Bot。
func (t *Telegram) VerifyFile(ctx context.Context, fileID string) error {
	_, _, err := t.getFilePath(ctx, fileID)
	return err
}

func (t *Telegram) OpenStream(ctx context.Context, fileID string) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, err
	}

	path, size, err := t.resolvePath(ctx, fileID)
	if err != nil {
		return Stream{}, err
	}

	response, err := t.openDownload(ctx, path)
	if err != nil {
		return Stream{}, err
	}
	if response.StatusCode == http.StatusNotFound {
		_ = response.Body.Close()
		t.cache.delete(fileID)

		path, size, err = t.getFilePath(ctx, fileID)
		if err != nil {
			return Stream{}, err
		}
		t.cache.set(fileID, pathEntry{path: path, size: size, expires: t.now().Add(t.cache.ttl)}, t.now())

		response, err = t.openDownload(ctx, path)
		if err != nil {
			return Stream{}, err
		}
		if response.StatusCode == http.StatusNotFound {
			_ = response.Body.Close()
			return Stream{}, ErrFileUnavailable
		}
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return Stream{}, ErrUpstream
	}

	// Telegram 的下载接口常把图片标成 application/octet-stream，
	// 此时按文件路径的扩展名推断，避免网关把图片当二进制流发给浏览器。
	contentType := strings.TrimSpace(response.Header.Get("Content-Type"))
	switch strings.ToLower(contentType) {
	case "", "application/octet-stream":
		if guessed := imageTypeFromPath(path); guessed != "" {
			contentType = guessed
		}
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if response.ContentLength > 0 {
		size = response.ContentLength
	}
	return Stream{Body: response.Body, ContentType: contentType, Size: size, Start: -1, End: -1}, nil
}

// Size 返回远端文件大小，供网关解析 Range 请求。
func (t *Telegram) Size(ctx context.Context, fileID string) (int64, error) {
	_, size, err := t.resolvePath(ctx, fileID)
	return size, err
}

// OpenStreamRange 按字节区间取流：优先让上游返回 206；
// 上游忽略 Range 时，只有总长可信且不超过视频上限才跳过前缀并限长输出。
func (t *Telegram) OpenStreamRange(ctx context.Context, fileID string, r ByteRange) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, err
	}

	path, size, err := t.resolvePath(ctx, fileID)
	if err != nil {
		return Stream{}, err
	}
	if size <= 0 {
		return Stream{}, ErrRangeUnsupported
	}
	if r.Start < 0 || r.Start >= size {
		return Stream{}, ErrFileUnavailable
	}
	end := int64(-1)
	if r.Length >= 0 {
		if r.Length == 0 {
			return Stream{}, ErrRangeUnsupported
		}
		end = r.Start + r.Length - 1
		if end >= size {
			end = size - 1
		}
	}

	response, err := t.openDownloadRange(ctx, path, r.Start, end)
	if err != nil {
		return Stream{}, err
	}
	if response.StatusCode == http.StatusNotFound {
		_ = response.Body.Close()
		t.cache.delete(fileID)

		path, size, err = t.getFilePath(ctx, fileID)
		if err != nil {
			return Stream{}, err
		}
		if size <= 0 || r.Start >= size {
			return Stream{}, ErrFileUnavailable
		}
		if r.Length >= 0 {
			end = r.Start + r.Length - 1
			if end >= size {
				end = size - 1
			}
		}
		t.cache.set(fileID, pathEntry{path: path, size: size, expires: t.now().Add(t.cache.ttl)}, t.now())

		response, err = t.openDownloadRange(ctx, path, r.Start, end)
		if err != nil {
			return Stream{}, err
		}
		if response.StatusCode == http.StatusNotFound {
			_ = response.Body.Close()
			return Stream{}, ErrFileUnavailable
		}
	}

	switch response.StatusCode {
	case http.StatusPartialContent:
		start, end, total, ok := parseContentRange(response.Header.Get("Content-Range"))
		if !ok {
			_ = response.Body.Close()
			return Stream{}, ErrUpstream
		}
		// 上游区间必须落在请求起点，且总长与 getFile 一致，否则拒绝而不是写出错位的 206。
		if start != r.Start || total != size || end < start || end >= size {
			_ = response.Body.Close()
			return Stream{}, ErrUpstream
		}
		length := end - start + 1
		if response.ContentLength > 0 && response.ContentLength != length {
			_ = response.Body.Close()
			return Stream{}, ErrUpstream
		}
		return Stream{Body: response.Body, ContentType: streamContentType(response, path), Size: total, Start: start, End: end}, nil
	case http.StatusOK:
		// 上游忽略 Range：总长必须可信且不超过视频上限，长度也要与 getFile 一致。
		if size <= 0 || size > MaxVideoBytes {
			_ = response.Body.Close()
			return Stream{}, ErrRangeUnsupported
		}
		if response.ContentLength > 0 && response.ContentLength != size {
			_ = response.Body.Close()
			return Stream{}, ErrUpstream
		}
		if r.Start > 0 {
			if _, err := io.CopyN(io.Discard, response.Body, r.Start); err != nil {
				_ = response.Body.Close()
				return Stream{}, errTransport
			}
		}
		var body io.ReadCloser = response.Body
		if r.Length >= 0 {
			body = struct {
				io.Reader
				io.Closer
			}{io.LimitReader(response.Body, r.Length), response.Body}
		}
		// 整段回退仍标记为整段响应，网关据此写 200。
		return Stream{Body: body, ContentType: streamContentType(response, path), Size: size, Start: -1, End: -1}, nil
	default:
		_ = response.Body.Close()
		return Stream{}, ErrUpstream
	}
}

// parseContentRange 解析 bytes start-end/total，缺字段或不合法时返回 false。
func parseContentRange(value string) (start, end, total int64, ok bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(strings.ToLower(value), "bytes ") {
		return 0, 0, 0, false
	}
	spec := strings.TrimSpace(value[len("bytes "):])
	rangePart, totalPart, found := strings.Cut(spec, "/")
	if !found {
		return 0, 0, 0, false
	}
	startText, endText, found := strings.Cut(rangePart, "-")
	if !found {
		return 0, 0, 0, false
	}
	var err error
	if start, err = strconv.ParseInt(strings.TrimSpace(startText), 10, 64); err != nil {
		return 0, 0, 0, false
	}
	if end, err = strconv.ParseInt(strings.TrimSpace(endText), 10, 64); err != nil {
		return 0, 0, 0, false
	}
	if total, err = strconv.ParseInt(strings.TrimSpace(totalPart), 10, 64); err != nil {
		return 0, 0, 0, false
	}
	if start < 0 || end < start || total <= end {
		return 0, 0, 0, false
	}
	return start, end, total, true
}

// streamContentType 解析下载响应的内容类型，必要时按文件扩展名补全。
func streamContentType(response *http.Response, path string) string {
	contentType := strings.TrimSpace(response.Header.Get("Content-Type"))
	switch strings.ToLower(contentType) {
	case "", "application/octet-stream":
		if guessed := imageTypeFromPath(path); guessed != "" {
			contentType = guessed
		}
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return contentType
}
