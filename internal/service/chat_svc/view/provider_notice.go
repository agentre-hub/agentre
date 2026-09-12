// provider_notice.go 持有「供应商/模型 notice」的编解码与「哪一条 assistant 才是真
// 正一轮」的判定 —— 两者都是纯投影(实体 → 结构化负载 / 下标),从 chat_svc/chat.go
// 的读侧迁入。
package view

import (
	"encoding/json"
	"strings"

	"github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
)

// NoticeOnlyMessage 报告一条消息是不是「只承载供应商切换 notice 的旁白行」。
//
// 切换 notice 是独立落库的一条消息(session_provider.go 的 appendProviderSwitchNotice):
// role 是 assistant、块只有一个 NoticeBlock,但它不是一轮对话 —— 用户可以在轮中切换
// 供应商(决策 8),NextSeq 就把它排在**在跑的那条 assistant 之后**。所以凡是「末条
// assistant = 在跑的那一轮」的推导都必须跳过它。
//
// 判据是 kind == switch,而不是「块全是 notice」:回退 notice 由 turnRun.finalize
// (chat_svc/turn_run.go)追加进**这一轮
// 自己**的 assistant 消息,零内容收尾(发完立刻点停止)时那条消息的块正好只剩它 ——
// 按「块全是 notice」判,一轮真实对话就会被当成旁白行跳过。
//
// 没有块 ≠ 旁白行:轮刚起时 assistant 行的 BlocksJSON 恒为 "[]",那是真实的一轮,必须
// 认到它。解码失败同样不算旁白行 —— 一条读不出块的消息宁可当成真实轮,也不该把在跑的
// turn 让给它后面的行。
// 与前端 lib/notice-message.ts 的 isNoticeOnlyMessage 同一口径(那边跳的是同一批行)。
func NoticeOnlyMessage(m *chat_entity.Message) bool {
	if m == nil {
		return false
	}
	bs, err := m.GetBlocks()
	if err != nil || len(bs) == 0 {
		return false
	}
	for _, b := range bs {
		var text string
		switch tb := b.(type) {
		case blocks.NoticeBlock:
			text = tb.Text
		case *blocks.NoticeBlock:
			if tb == nil {
				return false
			}
			text = tb.Text
		default:
			return false
		}
		if p, ok := DecodeProviderNotice(text); !ok || !isSessionSwitchKind(p.Kind) {
			return false
		}
	}
	return true
}

// LastTurnAssistantIndex 返回最后一条**真实** assistant 消息的下标(没有 → -1)。
// 供应商切换 notice 那类旁白行跳过,见 NoticeOnlyMessage。
// 先筛 role 再解块:旁白行必是 assistant,user 行不必为此付一次 blocks 解码。
func LastTurnAssistantIndex(msgs []*chat_entity.Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i] == nil || msgs[i].Role != "assistant" {
			continue
		}
		if NoticeOnlyMessage(msgs[i]) {
			continue
		}
		return i
	}
	return -1
}

// ProviderNotice 是供应商/模型相关的持久 notice 写进 blocks.NoticeBlock.Text 的
// 小 JSON。
// NoticeBlock 是 cago 库的 UI-only 块,只有 Level/Text 两个字段(库类型,不能加字段),所以
// 把结构化信息编码进 Text;前端投影(noticeBlockToChatBlock)解回 ChatBlock 的
// ProviderKey / ProviderName / ModelKey / ModelName / NoticeKind 再用 t() 渲染。该块
// 从不发给 LLM。
// 旧数据 / 非结构化文本的 NoticeBlock 走 Text 原样渲染兜底。
//
// 两种 kind:
//   - ""（无 kind 字段,含全部旧数据）= 供应商回退提示(2026-08-09 决策 8):会话所选
//     供应商缺失/停用/不兼容,本轮回退 agent 绑定,ProviderKey 是被回退掉的那个 key;
//   - "switch" = 用户在会话里切换了 ModelTarget(2026-08-10 决策 9 / 2026-08-11 决策 1):
//     ProviderKey 是切换后的会话级 key,**空串表示改回跟随 agent 绑定 / CLI 登录态** ——
//     所以这一种不能靠 "ProviderKey 非空" 判定负载有效,kind 字段本身才是判据。
//
// ProviderName / ModelName 是展示名(2026-08-10 显示缺陷修复决策 1/2):后端按当前解析到
// 的实体填入,查不到(供应商已删)时留空 —— 前端优先渲染它,为空则回退到 key。名字只有
// 产出 notice 的后端手里有,不能让前端按 key 反查(供应商列表可能未拉/已缺项)。
type ProviderNotice struct {
	ProviderKey  string `json:"providerKey,omitempty"`
	ProviderName string `json:"providerName,omitempty"`
	ModelKey     string `json:"modelKey,omitempty"`
	ModelName    string `json:"modelName,omitempty"`
	Kind         string `json:"kind,omitempty"`
	// ReasoningEffort 只在 kind=reasoning_effort 时有意义:切换后的档位,空串表示
	// 改回跟随后端配置(spec 2026-09-01 决策 7)。档位是自明的序数,没有展示名要带
	// —— 前端按它选 t() 文案。
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

// NoticeKindSwitch 见 ProviderNotice 的 kind 说明。回退提示不写 kind,
// 与旧数据同形。
const NoticeKindSwitch = "switch"

// NoticeKindReasoningEffort 是会话级思考力度切换的 notice(spec 2026-09-01 决策 7):
// 与 ModelTarget 切换同一条通道、同一份负载结构,只是换一个 kind + 带 reasoningEffort。
// **空的 reasoningEffort 表示改回跟随后端配置**,所以这一种同样只能靠 kind 判定负载
// 有效,不能看字段是否非空。
const NoticeKindReasoningEffort = "reasoning_effort"

// isSessionSwitchKind 报告这个 kind 是不是「用户改了某个会话级设置」那类旁白行。
// 两种切换 notice 都由 chat_svc 独立落库、允许发生在轮中(NextSeq 把它排在在跑的那条
// assistant 之后),因此都必须被「末条 assistant = 在跑的那一轮」的推导跳过。回退提示
// (kind 为空)不在此列:它是轮次自己收尾时追加进那一轮的消息里的。
func isSessionSwitchKind(kind string) bool {
	return kind == NoticeKindSwitch || kind == NoticeKindReasoningEffort
}

// ProviderDisplayName 取供应商展示名。prov 为 nil(查不到实体 / 未选任何供应商)时
// 返回空串,由调用方据此决定 notice 前端渲染时回退到 key 还是「跟随 agent 绑定」的
// 专用文案(2026-08-10 显示缺陷修复决策 1/2)。
func ProviderDisplayName(prov *llm_provider_entity.LLMProvider) string {
	if prov == nil {
		return ""
	}
	return prov.Name
}

// ModelDisplayName 取模型展示名。model 为 nil（未解析 / 非 fixed-model）时返回空串。
func ModelDisplayName(model *llm_provider_model_entity.LLMProviderModel) string {
	if model == nil {
		return ""
	}
	return model.Name
}

func EncodeProviderFallback(providerKey, providerName string) string {
	b, _ := json.Marshal(ProviderNotice{ProviderKey: providerKey, ProviderName: providerName})
	return string(b)
}

// EncodeProviderSwitch 编码「本会话自此改用某 ModelTarget」的持久 notice(2026-08-10
// 决策 9 / 2026-08-11 决策 1)。providerKey 为空 = 改回跟随 agent 绑定,此时 providerName
// 恒为空；modelKey 为空 = provider-default,modelName 恒为空。仍用 kind=switch,仅扩展
// 负载。
func EncodeProviderSwitch(providerKey, modelKey, providerName, modelName string) string {
	b, _ := json.Marshal(ProviderNotice{
		ProviderKey: providerKey, ProviderName: providerName,
		ModelKey: modelKey, ModelName: modelName,
		Kind: NoticeKindSwitch,
	})
	return string(b)
}

// EncodeReasoningEffortSwitch 编码「本会话自此改用某思考力度」的持久 notice(spec
// 2026-09-01 决策 7)。effort 为空 = 改回跟随后端配置 —— 负载里那个字段随之省略,
// kind 才是判据。
func EncodeReasoningEffortSwitch(effort string) string {
	b, _ := json.Marshal(ProviderNotice{
		Kind:            NoticeKindReasoningEffort,
		ReasoningEffort: effort,
	})
	return string(b)
}

// DecodeProviderNotice 把 NoticeBlock.Text 还原成结构化负载。
// ok=false 表示文本不是本功能产出的结构化负载(旧数据/其它来源的 notice),调用方应
// 原样渲染 Text。
func DecodeProviderNotice(text string) (payload ProviderNotice, ok bool) {
	var p ProviderNotice
	if err := json.Unmarshal([]byte(text), &p); err != nil {
		return ProviderNotice{}, false
	}
	if p.ProviderKey == "" && p.Kind == "" {
		return ProviderNotice{}, false
	}
	return p, true
}

// FirstNonEmpty 返回第一个非空白参数(全空白 → "")。会话级 provider_key 优先于
// agent 绑定取 effectiveProviderKey 用(决策 3/9)。
func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
