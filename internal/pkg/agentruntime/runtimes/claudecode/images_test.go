package claudecode

import (
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/claudecode"
)

// 用户 turn 里的 ImageBlock(inline 字节)应被抽成 claudecode.Image,透传给 CLI
// stream-json user frame。非图片 block 跳过;URL-only 图片(无 inline 字节)当前
// 不支持(CLI 走 base64 inline),跳过而非报错 —— 与 piagent extractImages 行为一致。
func TestExtractImagesFromUserBlocks(t *testing.T) {
	blocks := []cagoblocks.ContentBlock{
		cagoblocks.TextBlock{Text: "look at this"},
		cagoblocks.ImageBlock{MediaType: "image/png", Source: cagoblocks.BlobSource{Inline: []byte{0xDE, 0xAD}}},
		&cagoblocks.ImageBlock{MediaType: "image/jpeg", Source: cagoblocks.BlobSource{Inline: []byte{0xBE, 0xEF}}},
		cagoblocks.ImageBlock{MediaType: "image/png", Source: cagoblocks.BlobSource{URL: "https://x/y.png"}},
	}

	imgs := extractImages(blocks)

	require.Len(t, imgs, 2)
	assert.Equal(t, claudecode.Image{Data: []byte{0xDE, 0xAD}, MediaType: "image/png"}, imgs[0])
	assert.Equal(t, claudecode.Image{Data: []byte{0xBE, 0xEF}, MediaType: "image/jpeg"}, imgs[1])
}

func TestExtractImagesEmptyWhenNoImages(t *testing.T) {
	assert.Nil(t, extractImages([]cagoblocks.ContentBlock{cagoblocks.TextBlock{Text: "hi"}}))
	assert.Nil(t, extractImages(nil))
}
