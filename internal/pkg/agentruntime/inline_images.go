package agentruntime

import cagoblocks "github.com/cago-frame/agents/agent/blocks"

// InlineImage 是从用户 blocks 里抽出的一张 inline 图片:原始字节 + MIME。
type InlineImage struct {
	Data     []byte
	MimeType string
}

// ExtractInlineImages 把本轮用户 blocks 里的 ImageBlock 抽成 InlineImage,并用
// convert 投影成各 runtime 自己的图片类型。非图片 block 跳过;只有 URL、没有 inline
// 字节的图片当前不支持(各 CLI 都走 base64 inline),同样跳过。
//
// B 是目标图片类型(claudecode.Image / pkgpi.Image),字段名不同,故由调用方给一行
// 转换函数,遍历与判空的这套规则只留一份。
func ExtractInlineImages[B any](blocks []cagoblocks.ContentBlock, convert func(data []byte, mimeType string) B) []B {
	var out []B
	for _, b := range blocks {
		switch v := b.(type) {
		case cagoblocks.ImageBlock:
			if img, ok := inlineImage(v); ok {
				out = append(out, convert(img.Data, img.MimeType))
			}
		case *cagoblocks.ImageBlock:
			if v == nil {
				continue
			}
			if img, ok := inlineImage(*v); ok {
				out = append(out, convert(img.Data, img.MimeType))
			}
		}
	}
	return out
}

func inlineImage(b cagoblocks.ImageBlock) (InlineImage, bool) {
	if len(b.Source.Inline) == 0 {
		return InlineImage{}, false
	}
	return InlineImage{Data: b.Source.Inline, MimeType: b.MediaType}, true
}
