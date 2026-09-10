package chat_svc

import (
	"github.com/cago-frame/agents/agent/blocks"

	chatblocks "github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
)

func hasCompactBoundaryBlock(bs []blocks.ContentBlock) bool {
	for _, b := range bs {
		switch b.(type) {
		case chatblocks.CompactBoundaryBlock, *chatblocks.CompactBoundaryBlock:
			return true
		}
	}
	return false
}
