package piagent

import (
	cagoblocks "github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	pkgpi "github.com/agentre-hub/agentre/pkg/piagent"
)

// extractImages 从本轮用户 blocks 里抽出 inline ImageBlock,转成 Pi prompt 需要的
// pkgpi.Image(inline 字节 + MIME)。
func extractImages(blocks []cagoblocks.ContentBlock) []pkgpi.Image {
	return agentruntime.ExtractInlineImages(blocks, func(data []byte, mimeType string) pkgpi.Image {
		return pkgpi.Image{Data: data, MimeType: mimeType}
	})
}
