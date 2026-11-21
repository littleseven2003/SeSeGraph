package storage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
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
	path = strings.TrimLeft(strings.TrimSpace(path), "/")
	if path == "" {
		return nil, ErrFileUnavailable
	}

	endpoint := t.apiBase + "/file/bot" + t.token + "/" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, ErrUpstream
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

// OpenStream 查询文件路径并打开下载流，不把文件整体载入内存。
func (t *Telegram) OpenStream(ctx context.Context, fileID string) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return Stream{}, err
	}

	path, size, err := t.getFilePath(ctx, fileID)
	if err != nil {
		return Stream{}, err
	}

	response, err := t.openDownload(ctx, path)
	if err != nil {
		return Stream{}, err
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		if response.StatusCode == http.StatusNotFound {
			return Stream{}, ErrFileUnavailable
		}
		return Stream{}, ErrUpstream
	}

	contentType := strings.TrimSpace(response.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if response.ContentLength > 0 {
		size = response.ContentLength
	}
	return Stream{Body: response.Body, ContentType: contentType, Size: size}, nil
}
