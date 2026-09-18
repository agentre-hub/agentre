package claudecode

import (
	cagoblocks "github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/pkg/claudecode"
)

// extractImages 从本轮用户 blocks 里抽出 inline ImageBlock,转成 claudecode.Image,
// 由 Run 透传给 CLI stream-json user frame 的 image content block。
func extractImages(blocks []cagoblocks.ContentBlock) []claudecode.Image {
	return agentruntime.ExtractInlineImages(blocks, func(data []byte, mimeType string) claudecode.Image {
		return claudecode.Image{Data: data, MediaType: mimeType}
	})
}
