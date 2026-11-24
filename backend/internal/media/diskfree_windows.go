//go:build windows

package media

// diskFree 在 Windows 上不做探测，交由上限检查兜底，避免引入额外依赖。
func diskFree(string) (int64, bool) {
	return 0, false
}
