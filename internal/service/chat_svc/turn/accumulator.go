// Package turn 管理一轮 chat turn 的 block 累积与事件 dispatch。
//
// Accumulator 替代旧 chat_svc/chat.go turnBlockAccumulator:
//   - text/thinking 累在 buf,遇 AddToolUse/AddBlock flush 成块
//   - thinking 按时间顺序穿插:每段(thinking→text)在 flush 时先落 thinking
//     再落 text(与流序 thinking_delta...text_delta 一致),工具循环里后几轮的
//     thinking 出现在对应 tool_result 之后,而不是全堆到 index 0。
//   - 通过范型 Mutate[B](acc, key, func(*B)) 取代写死的 patchXxx 方法
//
// 与旧 acc 不同:Mutate 必须传 *B(指针),因为 mutate 语义就是 in-place patch;
// addBlock 时传 cagoblocks.ContentBlock 接口指针或值都可,但若想被 Mutate 命中
// 必须传 *B。
package turn

import (
	"strings"
	"sync"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
)

type Accumulator struct {
	mu          sync.Mutex
	finalBlocks []cagoblocks.ContentBlock
	textBuf     strings.Builder
	thinkingBuf strings.Builder
	mutateIndex map[string]int
}

func New() *Accumulator {
	return &Accumulator{mutateIndex: map[string]int{}}
}

func (a *Accumulator) AddText(s string) {
	if s == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.textBuf.WriteString(s)
}

func (a *Accumulator) AddThinking(s string) {
	if s == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.thinkingBuf.WriteString(s)
}

// AddToolUse 与 AddBlock 等价但语义更明确:cago ToolUseBlock 走这条。
func (a *Accumulator) AddToolUse(b cagoblocks.ContentBlock, mutateKey string) {
	a.AddBlock(b, mutateKey)
}

// AddToolResult 不 flush buf(tool_use→tool_result 之间一般无文字)。
func (a *Accumulator) AddToolResult(b cagoblocks.ContentBlock) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.finalBlocks = append(a.finalBlocks, b)
}

// AddBlock 任意 block 走这条;先 flush 当前段(thinking → text),再 push,
// 记 mutateIndex。
func (a *Accumulator) AddBlock(b cagoblocks.ContentBlock, mutateKey string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.flushBufsLocked()
	if mutateKey != "" {
		a.mutateIndex[mutateKey] = len(a.finalBlocks)
	}
	a.finalBlocks = append(a.finalBlocks, b)
}

// HasToolUse 查询当前是否已 push 过该 ID 的 cago.ToolUseBlock(value 或 pointer)。
// 用于孤儿 tool_result 丢弃。
func (a *Accumulator) HasToolUse(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, b := range a.finalBlocks {
		switch tu := b.(type) {
		case *cagoblocks.ToolUseBlock:
			if tu.ID == id {
				return true
			}
		case cagoblocks.ToolUseBlock:
			if tu.ID == id {
				return true
			}
		}
	}
	return false
}

// HasOpenToolUse 是否还有已 push、但尚未收到配对 tool_result 的外层
// cago.ToolUseBlock —— 即"工具在途"。只看外层块,与 ToolResultHandler 的孤儿
// 判定(ParentToolCallID == "" 才查 HasToolUse)同一口径:subagent 内层
// NestedToolUseBlock 的结果不走外层配对,不计入在途。
//
// 用于 turn 分段:SteerConsumed 到达时若还有在途工具,分段要等它的 tool_result
// 落进当前 accumulator 之后再做,否则结果会在新 accumulator 里成为孤儿被丢弃。
func (a *Accumulator) HasOpenToolUse() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	open := make(map[string]struct{})
	for _, b := range a.finalBlocks {
		switch blk := b.(type) {
		case *cagoblocks.ToolUseBlock:
			open[blk.ID] = struct{}{}
		case cagoblocks.ToolUseBlock:
			open[blk.ID] = struct{}{}
		case *cagoblocks.ToolResultBlock:
			delete(open, blk.ToolUseID)
		case cagoblocks.ToolResultBlock:
			delete(open, blk.ToolUseID)
		}
	}
	return len(open) > 0
}

// ToolUseInput 返回已 push 的该 ID cago.ToolUseBlock 的原始 Input(value 或 pointer
// 形态),未找到返回 (nil, false)。SubagentStarted handler 用它读 run_in_background
// 判定一次 local_bash 帧是真后台 bash 还是普通前台 bash。
func (a *Accumulator) ToolUseInput(id string) (map[string]any, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, b := range a.finalBlocks {
		switch tu := b.(type) {
		case *cagoblocks.ToolUseBlock:
			if tu.ID == id {
				return tu.Input, true
			}
		case cagoblocks.ToolUseBlock:
			if tu.ID == id {
				return tu.Input, true
			}
		}
	}
	return nil, false
}

// Empty 反映"无任何内容 + 无 buf";turn 收尾判定是否落 message 用。
func (a *Accumulator) Empty() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.finalBlocks) == 0 && a.textBuf.Len() == 0 && a.thinkingBuf.Len() == 0
}

// Snapshot 中途快照(checkpoint 用),不消费 buf。返回新切片。
// thinking 排在末尾当前段内 thinking→text,与 Finalize 同一时间顺序。
func (a *Accumulator) Snapshot() []cagoblocks.ContentBlock {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]cagoblocks.ContentBlock, 0, len(a.finalBlocks)+2)
	out = append(out, a.finalBlocks...)
	if a.thinkingBuf.Len() > 0 {
		out = append(out, &cagoblocks.ThinkingBlock{Text: a.thinkingBuf.String()})
	}
	if a.textBuf.Len() > 0 {
		out = append(out, &cagoblocks.TextBlock{Text: a.textBuf.String()})
	}
	return out
}

// Finalize 收尾:flush 剩余段(thinking → text)到末尾。thinking 不再整体
// 提到 index 0 —— 工具循环里后几轮的思考按真实位置穿插。
func (a *Accumulator) Finalize() []cagoblocks.ContentBlock {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.flushBufsLocked()
	return a.finalBlocks
}

// flushBufsLocked 把当前段(thinking → text)落成块。thinking 先于 text ——
// 与流序(thinking_delta... text_delta...)一致,同段内 thinking 永远在 text 前。
func (a *Accumulator) flushBufsLocked() {
	if a.thinkingBuf.Len() > 0 {
		a.finalBlocks = append(a.finalBlocks, &cagoblocks.ThinkingBlock{Text: a.thinkingBuf.String()})
		a.thinkingBuf.Reset()
	}
	if a.textBuf.Len() > 0 {
		a.finalBlocks = append(a.finalBlocks, &cagoblocks.TextBlock{Text: a.textBuf.String()})
		a.textBuf.Reset()
	}
}

// Mutate[B] 范型 patch: 按 key 查 mutateIndex,断言为 *B 后调 fn。
// 返回是否命中(未命中 = key 缺失或类型断言失败 = B 类型不符)。
//
// B 用 `any` 而不是 ContentBlock 约束:类型参数的指针 *B 不会自动 satisfy 接口,
// 必须改用两步断言(any → *B)。调用方写出 B = 具体 block 类型即可,例:
//
//	Mutate[blocks.UserAskBlock](acc, "user_ask:r-1", func(b *blocks.UserAskBlock) { ... })
func Mutate[B any](a *Accumulator, key string, fn func(*B)) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	idx, ok := a.mutateIndex[key]
	if !ok || idx >= len(a.finalBlocks) {
		return false
	}
	b, ok := any(a.finalBlocks[idx]).(*B)
	if !ok {
		return false
	}
	fn(b)
	return true
}

// AdoptMutateKey[B] 把 alias 这个 mutate key 挂到**已存在**的某一块上:按累积顺序找
// 第一个满足 pred 的 *B,把 alias 指向它,并就地调 fn。命中返回 true,没有满足 pred
// 的块时不注册 alias、返回 false。
//
// 用于「同一个逻辑对象换了新 ID」的场景:CLI 恢复一个子代理时用同一个 task_id 但新的
// tool_use_id 重发 task_started,后续 progress/done 帧都带新 ID。把新 ID 认领到原块上,
// 这些帧无需知道自己是恢复来的就能落在原卡上,调用方也不必维护一张别名表。
func AdoptMutateKey[B any](a *Accumulator, alias string, pred func(*B) bool, fn func(*B)) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, blk := range a.finalBlocks {
		b, ok := any(blk).(*B)
		if !ok || !pred(b) {
			continue
		}
		if alias != "" {
			a.mutateIndex[alias] = i
		}
		if fn != nil {
			fn(b)
		}
		return true
	}
	return false
}
