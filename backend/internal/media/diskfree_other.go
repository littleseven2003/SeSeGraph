//go:build !windows

package media

import "syscall"

// diskFree 返回数据盘可用字节数；探测失败时第二个返回值为 false，调用方按不阻塞处理。
func diskFree(path string) (int64, bool) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, false
	}
	return int64(stat.Bavail) * int64(stat.Bsize), true
}
