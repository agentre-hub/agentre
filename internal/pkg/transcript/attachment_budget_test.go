package transcript_test

import (
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/transcript"
	"github.com/agentre-hub/agentre/pkg/wire/wirelimits"
)

// 附件总量的宿主侧兜底。
//
// 客户端那一侧已经拦了一道(共享包 composer),但宿主不能信它:老版本控制台、别的实现,
// 都可能把一条超量的 runtime.run 送进来。而超量的后果不是这一次请求失败 —— 链路读上限
// 被撞破时 gorilla 回 1009,整条物理连接被拆掉,那台机器上所有会话一起重连。
//
// 判据是**原始字节**总量(不是 base64 之后的量):落库的块里装的就是原始字节。
func TestAttachmentBudget(t *testing.T) {
	t.Parallel()

	image := func(n int) cagoblocks.ContentBlock {
		return cagoblocks.ImageBlock{
			MediaType: "image/png",
			Source:    cagoblocks.BlobSource{Inline: make([]byte, n)},
		}
	}
	budget := int(wirelimits.MaxAttachmentBytes)

	t.Run("预算之内放行", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, transcript.CheckAttachmentBudget([]cagoblocks.ContentBlock{
			cagoblocks.TextBlock{Text: "看看这个"},
			image(budget / 2),
		}))
	})

	t.Run("正好顶格放行 —— 上限是可以用满的", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, transcript.CheckAttachmentBudget([]cagoblocks.ContentBlock{image(budget)}))
	})

	t.Run("多张加起来超了就拒绝:单张合法不代表一条消息合法", func(t *testing.T) {
		t.Parallel()
		err := transcript.CheckAttachmentBudget([]cagoblocks.ContentBlock{
			image(budget/2 + 1),
			image(budget/2 + 1),
		})
		require.Error(t, err)
		// 报的是总量与上限,不是「第几张不对」—— 拒的是这一整轮。
		assert.ErrorIs(t, err, transcript.ErrAttachmentBudget)
	})

	// 指针形态同样要量。本仓每一处认图片块的地方(chat_svc 的 projection.go /
	// transcript_replacement.go、claudecode 的 images.go)都是 `case blocks.ImageBlock`
	// 与 `case *blocks.ImageBlock` 两支并列 —— 因为 ContentBlock 的方法是值接收者,
	// 指针同样满足这个接口,两种形态都进得了同一条切片。
	//
	// 这道闸只认值形态的话它是**失效放行**:一条超量的消息以指针形态进来时,预算
	// 算出 0、原样放行,而后果不是这一次请求失败,是链路撞破读上限、整条物理连接
	// 被拆掉。守卫失效的方向必须是拒绝,不是放行。
	t.Run("指针形态的图片块同样算进总量,不是悄悄放行", func(t *testing.T) {
		t.Parallel()
		err := transcript.CheckAttachmentBudget([]cagoblocks.ContentBlock{
			&cagoblocks.ImageBlock{
				MediaType: "image/png",
				Source:    cagoblocks.BlobSource{Inline: make([]byte, budget+1)},
			},
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, transcript.ErrAttachmentBudget)
	})

	t.Run("没有附件时不设限:纯文本一轮与这条规矩无关", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, transcript.CheckAttachmentBudget([]cagoblocks.ContentBlock{
			cagoblocks.TextBlock{Text: "只有文字"},
		}))
		require.NoError(t, transcript.CheckAttachmentBudget(nil))
	})
}
