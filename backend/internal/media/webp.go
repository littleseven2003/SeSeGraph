// WebP 头部解析：落盘前确认内容确实是静态 WebP 位图，并读出真实像素尺寸，
// 避免客户端把任意字节或超高像素的图片塞进缓存。
package media

import "errors"

// ErrNotWebP 表示内容不是可解析的 WebP 位图。
var ErrNotWebP = errors.New("不是 WebP 位图")

// WebPInfo 是从文件头解析出的基本信息。
type WebPInfo struct {
	// Width 与 Height 是位图实际像素，取值来自图像块而不是画布。
	Width  int
	Height int
	// Animated 表示带 ANIM 块或扩展头声明动画。
	Animated bool
}

// InspectWebP 解析 WebP 的块顺序，取出图像尺寸并判断是否为静态图。
// 文档只认已验证的三种图像块，其他块一律跳过，不触碰负载内容。
func InspectWebP(data []byte) (WebPInfo, error) {
	if !webpHeader(data) {
		return WebPInfo{}, ErrNotWebP
	}
	var (
		info       WebPInfo
		imageSized bool
		canvasW    int
		canvasH    int
	)
	for offset := 12; offset+8 <= len(data); {
		fourcc := string(data[offset : offset+4])
		size := int(readUint32LE(data[offset+4 : offset+8]))
		payload := offset + 8
		if size < 0 || payload+size > len(data) || payload+size < payload {
			return WebPInfo{}, ErrNotWebP
		}
		chunk := data[payload : payload+size]
		switch fourcc {
		case "VP8 ":
			width, height, err := lossySize(chunk)
			if err != nil {
				return WebPInfo{}, err
			}
			if !imageSized {
				info.Width, info.Height, imageSized = width, height, true
			}
		case "VP8L":
			width, height, err := losslessSize(chunk)
			if err != nil {
				return WebPInfo{}, err
			}
			if !imageSized {
				info.Width, info.Height, imageSized = width, height, true
			}
		case "VP8X":
			if len(chunk) < 10 {
				return WebPInfo{}, ErrNotWebP
			}
			// 扩展头的标志位里，动画位一旦置位就说明不是静态图。
			if chunk[0]&0x02 != 0 {
				info.Animated = true
			}
			canvasW = int(readUint24LE(chunk[4:7])) + 1
			canvasH = int(readUint24LE(chunk[7:10])) + 1
		case "ANIM":
			info.Animated = true
		}
		// 块负载按偶数字节对齐，奇数长度后面有一个填充字节。
		offset = payload + size + size&1
	}
	if !imageSized {
		if canvasW <= 0 || canvasH <= 0 {
			return WebPInfo{}, ErrNotWebP
		}
		info.Width, info.Height = canvasW, canvasH
	}
	return info, nil
}

// lossySize 从 VP8 关键帧头读出宽高，先校验关键帧起始码。
func lossySize(chunk []byte) (int, int, error) {
	if len(chunk) < 10 {
		return 0, 0, ErrNotWebP
	}
	start := chunk[3:6]
	if start[0] != 0x9d || start[1] != 0x01 || start[2] != 0x2a {
		return 0, 0, ErrNotWebP
	}
	width := int(uint16(chunk[6]) | uint16(chunk[7])<<8) & 0x3fff
	height := int(uint16(chunk[8]) | uint16(chunk[9])<<8) & 0x3fff
	if width <= 0 || height <= 0 {
		return 0, 0, ErrNotWebP
	}
	return width, height, nil
}

// losslessSize 从 VP8L 头读出宽高：14 位存的是减一后的值。
func losslessSize(chunk []byte) (int, int, error) {
	if len(chunk) < 5 || chunk[0] != 0x2f {
		return 0, 0, ErrNotWebP
	}
	bits := readUint32LE(chunk[1:5])
	width := int(bits&0x3fff) + 1
	height := int(bits>>14&0x3fff) + 1
	if width <= 0 || height <= 0 || width > 1<<14 || height > 1<<14 {
		return 0, 0, ErrNotWebP
	}
	return width, height, nil
}

// readUint32LE 按小端读出 32 位无符号整数。
func readUint32LE(data []byte) uint32 {
	return uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16 | uint32(data[3])<<24
}

// readUint24LE 按小端读出 24 位无符号整数。
func readUint24LE(data []byte) uint32 {
	return uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16
}
