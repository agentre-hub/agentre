package piagent

import (
	cagoblocks "github.com/cago-frame/agents/agent/blocks"

	pkgpi "github.com/agentre-hub/agentre/pkg/piagent"
)

// extractImages 从本轮用户 blocks 里抽出 ImageBlock，转成 Pi prompt 需要的
// pkgpi.Image（inline 字节 + MIME）。非图片 block 跳过；只有 URL、无 inline 字节
// 的图片当前不支持（Pi RPC 走 base64 inline），同样跳过。
func extractImages(blocks []cagoblocks.ContentBlock) []pkgpi.Image {
	var out []pkgpi.Image
	for _, b := range blocks {
		switch v := b.(type) {
		case cagoblocks.ImageBlock:
			if img, ok := imageFromBlock(v); ok {
				out = append(out, img)
			}
		case *cagoblocks.ImageBlock:
			if v == nil {
				continue
			}
			if img, ok := imageFromBlock(*v); ok {
				out = append(out, img)
			}
		}
	}
	return out
}

func imageFromBlock(b cagoblocks.ImageBlock) (pkgpi.Image, bool) {
	if len(b.Source.Inline) == 0 {
		return pkgpi.Image{}, false
	}
	return pkgpi.Image{Data: b.Source.Inline, MimeType: b.MediaType}, true
}
