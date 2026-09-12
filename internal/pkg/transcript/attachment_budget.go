package transcript

// attachment_budget.go 回答「这一轮带的附件是不是太多了」。
//
// 它在**宿主**这一侧,而不是只在输入框那一侧:客户端已经拦了一道(共享包 composer 按
// 同一个常量算),但宿主不能信客户端 —— 老版本的控制台、别的实现,都送得进来一条超量
// 的 runtime.run。
//
// 超量的后果不是「这一次请求失败了」。链路读上限被撞破时 gorilla 回 1009 并让读循环
// 出错,于是**整条物理连接**被拆掉,而那条链路上跑着那台机器的全部虚拟通道,所有会话
// 一起断线重连(见 pkg/wire/wirelimits 的说明)。所以这道闸要在参数解出来之后、这一轮
// 真的开跑之前就落下。
//
// 拒绝的是**整轮**,不是截掉超出的那几张:静默跑一轮「用户以为发了图、模型没看见」的
// 对话,比一个明确的错误更糟(spec 2026-09-07-host-transcript-user-input 决策 3 对
// 「附件解不开」下的也是同一条判断)。

import (
	"errors"
	"fmt"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-hub/agentre/pkg/wire/wirelimits"
)

// ErrAttachmentBudget 是这一轮的附件总量超过了预算。
//
// 调用方据此把它归成「该轮参数无效」而不是一次内部故障 —— 它是调用方送来的东西不合规,
// 重试同一份不会变好。
var ErrAttachmentBudget = errors.New("attachment budget exceeded")

// CheckAttachmentBudget 校验这一批块里的附件**原始字节**总量。
//
// 量原始字节而不是 base64 之后的量:落库的块里装的就是原始字节,而 base64 只是它过线
// 时的形态。预算本身已经把那 4/3 的膨胀算进去了(见 wirelimits.MaxAttachmentBytes)。
//
// 没有附件时恒放行:这条规矩只管附件那一维,一轮纯文本与它无关。
func CheckAttachmentBudget(bs []cagoblocks.ContentBlock) error {
	total := 0
	for _, b := range bs {
		// 值与指针两支并列,与本仓每一处认图片块的地方同形(chat_svc 的 projection.go /
		// transcript_replacement.go、claudecode 的 images.go):ContentBlock 的方法是值
		// 接收者,*ImageBlock 同样满足这个接口,两种形态都进得了同一条切片。
		// 只认一支的话这道闸是**失效放行** —— 而它失效的方向只允许是拒绝。
		switch image := b.(type) {
		case cagoblocks.ImageBlock:
			total += len(image.Source.Inline)
		case *cagoblocks.ImageBlock:
			if image != nil {
				total += len(image.Source.Inline)
			}
		}
	}
	if total == 0 || int64(total) <= wirelimits.MaxAttachmentBytes {
		return nil
	}
	return fmt.Errorf("%w: %d bytes over the %d-byte budget",
		ErrAttachmentBudget, total, wirelimits.MaxAttachmentBytes)
}
