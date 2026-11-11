// Package clipboardimg 把一张 PNG 写进系统剪贴板（平台叶子，不反向依赖 service 层）。
//
// Wails v2 的运行时只提供 ClipboardSetText，图片得自己写：
//   - macOS：NSPasteboard，类型 public.png（write_darwin.go，cgo）；
//   - Windows：OpenClipboard，同时放注册格式 "PNG" 与 CF_DIBV5（write_windows.go，纯 Go）
//     —— 浏览器 / Office 认 "PNG"，画图等老应用只认位图；
//   - Linux：GTK 剪贴板（write_linux.go，cgo），只在 wails 的 dev / production 构建里编译，
//     其余构建组合（如 CI 上不装 GTK 的 go test）落到 write_other.go 的桩。
package clipboardimg

import (
	"bytes"
	"errors"
)

// ErrUnsupported 表示这个构建里没有原生写图片的实现。
var ErrUnsupported = errors.New("clipboardimg: writing images is not supported in this build")

var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}

// IsPNG 判断 data 是否以 PNG 签名开头。
func IsPNG(data []byte) bool {
	return bytes.HasPrefix(data, pngSignature)
}

// WritePNG 把 PNG 原样写进系统剪贴板，替换其中原有内容。调用方负责先用 IsPNG 校验。
func WritePNG(png []byte) error {
	return writePNG(png)
}
