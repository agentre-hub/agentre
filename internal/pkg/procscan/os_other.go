//go:build !darwin && !linux && !windows

package procscan

// 其余平台没有扫描实现：platformOS 保持 nil，New() 退化为 ErrUnsupported。
func init() {
	platformOS = func() OS { return unsupportedOS{} }
}
