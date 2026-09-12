package transcript_test

import (
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/pkg/transcript"
)

// TurnAttachments 是两个宿主共用的那一道:runtime.run 的 userBlocks 有两个填法不同的
// 真实调用方(桌面端 buildRunRequest 放的是那条消息的全部块,浏览器控制台只放图),
// 而宿主只有一份「userText + 附件」的落库逻辑。这几条钉的正是这道收敛。
func TestTurnAttachments(t *testing.T) {
	t.Parallel()

	image := cagoblocks.ImageBlock{MediaType: "image/png", Source: cagoblocks.BlobSource{Inline: []byte("PNG")}}

	for _, tc := range []struct {
		name     string
		userText string
		in       []cagoblocks.ContentBlock
		want     []cagoblocks.ContentBlock
		reason   string
	}{
		{
			name: "浏览器控制台:只放了图", userText: "look",
			in:   []cagoblocks.ContentBlock{image},
			want: []cagoblocks.ContentBlock{image},
		},
		{
			name: "桌面端:第一个就是同一句话的文本块", userText: "look",
			in:     []cagoblocks.ContentBlock{&cagoblocks.TextBlock{Text: "look"}, image},
			want:   []cagoblocks.ContentBlock{image},
			reason: "它是 userText 自己的另一种写法,留着就会把同一句话落成两个文本块",
		},
		{
			name: "从 wire 解出来的是值不是指针", userText: "look",
			in:     []cagoblocks.ContentBlock{cagoblocks.TextBlock{Text: "look"}, image},
			want:   []cagoblocks.ContentBlock{image},
			reason: "blocks.DecodeAll 交回的是值,两种写法都得认",
		},
		{
			name: "文本块与 userText 不同字:留着", userText: "look",
			in:     []cagoblocks.ContentBlock{cagoblocks.TextBlock{Text: "另一段"}, image},
			want:   []cagoblocks.ContentBlock{cagoblocks.TextBlock{Text: "另一段"}, image},
			reason: "那是这一轮真的多带的一段正文,不是 userText 的复本",
		},
		{
			name: "只去掉第一个同字文本块", userText: "look",
			in:   []cagoblocks.ContentBlock{cagoblocks.TextBlock{Text: "look"}, cagoblocks.TextBlock{Text: "look"}},
			want: []cagoblocks.ContentBlock{cagoblocks.TextBlock{Text: "look"}},
		},
		{
			name: "userText 为空:一个都不去", userText: "",
			in:     []cagoblocks.ContentBlock{cagoblocks.TextBlock{Text: "look"}, image},
			want:   []cagoblocks.ContentBlock{cagoblocks.TextBlock{Text: "look"}, image},
			reason: "那时 userBlocks 是这条消息仅有的正文来源",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, transcript.TurnAttachments(tc.userText, tc.in), tc.reason)
		})
	}
}
