package claudecode

import (
	cagoblocks "github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-hub/agentre/pkg/claudecode"
)

// extractImages 从本轮用户 blocks 里抽出 ImageBlock,转成 claudecode.Image
// (inline 字节 + MIME),由 Run 透传给 CLI stream-json user frame 的 image content
// block。非图片 block 跳过;只有 URL、无 inline 字节的图片当前不支持(CLI 走 base64
// inline),同样跳过 —— 与 piagent extractImages 行为一致。
func extractImages(blocks []cagoblocks.ContentBlock) []claudecode.Image {
	var out []claudecode.Image
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

func imageFromBlock(b cagoblocks.ImageBlock) (claudecode.Image, bool) {
	if len(b.Source.Inline) == 0 {
		return claudecode.Image{}, false
	}
	return claudecode.Image{Data: b.Source.Inline, MediaType: b.MediaType}, true
}
