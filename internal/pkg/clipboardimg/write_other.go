//go:build !(darwin && cgo) && !windows && !(linux && cgo && (dev || production))

package clipboardimg

// 没有原生实现的构建：CI 上不装 GTK 的 Linux go test / lint、wails 的 bindings 生成、
// 关了 cgo 的构建，以及其它平台。
func writePNG([]byte) error {
	return ErrUnsupported
}
