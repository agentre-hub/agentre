package chat_import_svc

import (
	"context"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// gapCodes 把契约里的缺口种类映射到说明文案的 i18n code。认不出的种类不落块 ——
// 编一句"未知缺口"对用户没有任何意义。
var gapCodes = map[transcriptimport.GapKind]int{
	transcriptimport.GapThinkingUnavailable: code.ChatImportGapThinking,
	transcriptimport.GapSubagentInternals:   code.ChatImportGapSubagentInternals,
	transcriptimport.GapContentTruncated:    code.ChatImportGapTruncated,
	transcriptimport.GapUnclosedToolCall:    code.ChatImportGapUnclosedToolCall,
	transcriptimport.GapUnparsableRecords:   code.ChatImportGapUnparsableRecords,
}

// gapText 是一条缺口的说明文案(转录内的说明块与预览顶部的提示同一份文案)。
func gapText(ctx context.Context, kind transcriptimport.GapKind) string {
	c, ok := gapCodes[kind]
	if !ok {
		return ""
	}
	return i18n.T(ctx, c)
}

// gapNotifier 决定缺口说明块落在哪一轮。
//
// 口径(spec 决策 11):同一条缺口在**导入前**与**转录内**各说一次 —— 转录内说的
// 那一次落在它第一次真的发生的那一轮,而不是每轮都重复一遍(42 轮的会话会变成
// 42 条灰字)。
//
// 「第一次发生」怎么判:
//   - 思维不可用:第一条没有任何思维内容的 assistant 消息。
//   - 未闭合的工具调用:第一轮还留着没等到结果的外层工具调用。
//   - 其余(子代理内部过程 / 截断 / 坏行):磁盘上定位不到具体位置,落在第一轮。
type gapNotifier struct {
	pending map[transcriptimport.GapKind]struct{}
	order   []transcriptimport.GapKind
}

func newGapNotifier(gaps []transcriptimport.Gap) *gapNotifier {
	g := &gapNotifier{pending: map[transcriptimport.GapKind]struct{}{}}
	for _, gap := range gaps {
		if _, ok := gapCodes[gap.Kind]; !ok {
			continue
		}
		if _, dup := g.pending[gap.Kind]; dup {
			continue
		}
		g.pending[gap.Kind] = struct{}{}
		g.order = append(g.order, gap.Kind)
	}
	return g
}

// appendTo 在这一轮该说的缺口上落说明块,并把它从待说清单里划掉。
func (g *gapNotifier) appendTo(ctx context.Context, acc *turn.Accumulator) {
	if g == nil || acc == nil || len(g.pending) == 0 {
		return
	}
	for _, kind := range g.order {
		if _, still := g.pending[kind]; !still {
			continue
		}
		if !g.appliesHere(kind, acc) {
			continue
		}
		text := gapText(ctx, kind)
		if text == "" {
			delete(g.pending, kind)
			continue
		}
		acc.AddBlock(&cagoblocks.NoticeBlock{Level: "info", Text: text}, "")
		delete(g.pending, kind)
	}
}

func (g *gapNotifier) appliesHere(kind transcriptimport.GapKind, acc *turn.Accumulator) bool {
	switch kind {
	case transcriptimport.GapThinkingUnavailable:
		return !hasThinking(acc)
	case transcriptimport.GapUnclosedToolCall:
		return acc.HasOpenToolUse()
	default:
		return true
	}
}

// hasThinking 看这一轮到底有没有思维内容。用 Snapshot 而不是 Finalize:后者会
// 消费掉缓冲区,而说明块还要排在它后面。
func hasThinking(acc *turn.Accumulator) bool {
	for _, b := range acc.Snapshot() {
		switch tb := b.(type) {
		case *cagoblocks.ThinkingBlock:
			if tb != nil && tb.Text != "" {
				return true
			}
		case cagoblocks.ThinkingBlock:
			if tb.Text != "" {
				return true
			}
		}
	}
	return false
}
