package app

import (
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/clipboardimg"
)

// writeClipboardPNG 是平台写入器的测试接缝；生产代码写真正的系统剪贴板。
var writeClipboardPNG = clipboardimg.WritePNG

// CopyImageToClipboard 把一张 PNG（base64）写进系统剪贴板。
//
// 前端共享包已经把图转成了 PNG（spec 2026-11-06-image-viewer-and-copy 决策 6），
// 这里只校验「非空且是 PNG」，字节原样交给平台写入器。不走 WebView 的 Clipboard
// API 的理由与文字复制相同：WKWebView 不看手势就拒成 NotAllowedError。
//
// 平台错误原样返回：前端只拿它分类成一句事实（原话不进可见文案），原话写进日志。
func (a *App) CopyImageToClipboard(pngBase64 string) error {
	data, err := base64.StdEncoding.DecodeString(pngBase64)
	if err != nil {
		return fmt.Errorf("decode image: %w", err)
	}
	if len(data) == 0 {
		return errors.New("empty image")
	}
	if !clipboardimg.IsPNG(data) {
		return errors.New("image is not a PNG")
	}
	if err := writeClipboardPNG(data); err != nil {
		logger.Ctx(a.ctx).Warn("app.CopyImageToClipboard: write system clipboard",
			zap.Int("bytes", len(data)), zap.Error(err))
		return err
	}
	return nil
}
