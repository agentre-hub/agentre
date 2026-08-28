// Package transcriptimport 定义「把 CLI 留在磁盘上的会话读回 agentre」这件事的
// 后端中立契约:发现(Scan)、打开(Open)、回放(Turns)。
//
// 分工:本包只描述形状,不认识任何一家的方言;claude 的 JSONL、codex 的 rollout、
// pi 的 session file 各自的知识留在对应 runtime 包里,经 Register 挂进注册表。
// 消费方(导入服务、daemon 的 RPC 壳)只依赖注册表与这几个类型,不引用具体构造器。
//
// 两条硬约束刻在契约里:
//   - **磁盘档案只读**。实现只允许 open/read/stat,不得创建、修改、删除、重命名或
//     截断 CLI 的任何会话文件与索引文件。
//   - **不假设整份转录装得下内存**。Turns 是推式流:读取器逐轮回调,消费方逐轮落库;
//     42 轮 / 402 次工具调用的会话在本机是常态,而同一份实现还要在 daemon 里按 RPC
//     逐轮发出去。
package transcriptimport

import (
	"context"
	"strings"
	"time"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/agents/provider"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// Locator 是候选条目的不透明定位符:由产出它的 Source 自己解释(claude 是
// projects 根下的相对路径,codex / pi 各按自家索引编码),消费方只负责原样回传。
// 声明成字符串是为了能原样穿过 wire 走到 daemon 侧的同一个读取器。
type Locator string

// Origin 是候选的来源标记,只用于展示。判重一律以库里的 provider_session_id 为准
// —— pi 的会话文件在磁盘上没有任何来源标记,靠它判重会漏。
type Origin string

const (
	// OriginUnknown 磁盘上没有可用的来源线索。
	OriginUnknown Origin = ""
	// OriginTerminal 用户在终端里自己起的会话。
	OriginTerminal Origin = "terminal"
	// OriginAgentre 本来就是 agentre 起的会话(claude 的 entrypoint / codex 的 originator)。
	OriginAgentre Origin = "agentre"
)

// Filter 是扫描期的过滤条件。零值表示不过滤。
//
// 没有设备维度:一个 Source 只认识它所在这台机器上的档案,选哪台机器是上层聚合
// (本地 Source 还是经 daemon RPC 的远端 Source)的事,不是 Source 自己的入参。
type Filter struct {
	// CwdPrefix 只保留工作目录以它开头的候选。
	CwdPrefix string
	// Since 只保留最后活动时间不早于它的候选;零值不过滤。
	Since time.Time
	// TitleQuery 标题关键词,大小写不敏感的子串匹配;空串不过滤。
	TitleQuery string
	// Limit 最多返回多少条(按最后活动时间倒序取);<=0 表示不限。
	Limit int
}

// Matches 回答「这条候选过不过得了这道筛」。
//
// 判据住在契约这一侧而不是各读取器里:三家各抄一份的话,「零值不过滤」「标题大小写
// 不敏感」这些口径迟早在某一家上走样,而走样的表现只是那一家的列表少了几行 ——
// 没有任何一处会报错。Limit 不在这里:它要在**合并排序之后**才裁得对。
func (f Filter) Matches(c Candidate) bool {
	if f.CwdPrefix != "" && !strings.HasPrefix(c.Cwd, f.CwdPrefix) {
		return false
	}
	if !f.Since.IsZero() && c.EndedAt.Before(f.Since) {
		return false
	}
	if f.TitleQuery != "" && !strings.Contains(strings.ToLower(c.Title), strings.ToLower(f.TitleQuery)) {
		return false
	}
	return true
}

// Candidate 是一条可导入的磁盘会话。**只由元信息构成** —— 出这一行不允许解全文。
type Candidate struct {
	Backend           agent_backend_entity.BackendType
	ProviderSessionID string
	Title             string
	Cwd               string
	StartedAt         time.Time
	EndedAt           time.Time
	// Turns 是轮数;元信息里拿不到时留 0,表示"未知",不要拿它当"空会话"。
	Turns   int
	Origin  Origin
	Locator Locator
}

// GapKind 是缺口的种类。缺口是契约的一等公民,不是日志里的备注:UI 拿它渲染
// 导入前的提示与转录内的灰字说明。
type GapKind string

const (
	// GapThinkingUnavailable 思维链在磁盘上不可用(Anthropic 模型只落签名,正文是空的)。
	GapThinkingUnavailable GapKind = "thinking_unavailable"
	// GapSubagentInternals 子代理的内部过程缺失(子文件不在了)。
	GapSubagentInternals GapKind = "subagent_internals_missing"
	// GapContentTruncated 内容被后端截断。
	GapContentTruncated GapKind = "content_truncated"
	// GapUnclosedToolCall 有工具调用没有对应的结果(会话被中断)。
	GapUnclosedToolCall GapKind = "unclosed_tool_call"
	// GapUnparsableRecords 单行坏数据:只丢那一行,并如实计数。
	GapUnparsableRecords GapKind = "unparsable_records"
)

// Gap 是一条缺口声明。Count 是受影响的条目数,Detail 是可选的补充说明(不进 i18n,
// 只放不可翻译的动态信息如文件名)。
type Gap struct {
	Kind   GapKind
	Count  int
	Detail string
}

// Meta 是一份已打开转录的元信息。
type Meta struct {
	Backend           agent_backend_entity.BackendType
	ProviderSessionID string
	Title             string
	Cwd               string
	Model             string
	Turns             int
	ToolCalls         int
	Compactions       int
	StartedAt         time.Time
	EndedAt           time.Time
	Origin            Origin
	Gaps              []Gap
}

// Turn 是回放出的一轮。
type Turn struct {
	// Index 从 0 开始,按回放顺序递增。
	Index int
	// UserText / UserImages 是这一轮用户发出的内容。
	UserText   string
	UserImages []blocks.ImageBlock
	// Events 是这一轮的事件序列,交给既有 chat_svc/turn.Dispatcher 落块 ——
	// 不得另开第二条 blocks_json 生成路径。
	Events []agentruntime.Event
	// Usage 是这一轮最后一次 API call 的用量;磁盘上拿不到时为 nil。
	Usage *provider.Usage
	Model string
	// StartedAt / EndedAt 取磁盘上的时间,不是导入时间。
	StartedAt time.Time
	EndedAt   time.Time
	// ForkAnchor 是续跑时该锚回哪一条后端记录(claude 是上一条 assistant 的 uuid)。
	ForkAnchor string
	// ErrorText 仅在这一轮以失败或中断收尾时非空。
	ErrorText string
}

// Transcript 是一份可回放的转录。Meta 在 Open 时就位;Turns 可重复调用,每次从头
// 流一遍(预览取前 N 轮,导入取全部)。用完必须 Close。
type Transcript interface {
	Meta() Meta
	// Turns 按顺序逐轮回调 yield。yield 返回非 nil 时立刻停止回放并原样返回该错误
	// (预览取前 N 轮就靠它提前收工)。
	Turns(ctx context.Context, yield func(Turn) error) error
	Close() error
}

// Source 是某个后端在这台机器上的磁盘读取器。
type Source interface {
	Backend() agent_backend_entity.BackendType
	// Scan 只读元信息:文件名、stat、文件头尾的少量记录、后端自带的索引。
	// 不得为了出一行候选而解全文。
	Scan(ctx context.Context, f Filter) ([]Candidate, error)
	// Open 把定位符变成一份可回放的转录。定位符不可信,实现必须校验它没有越出
	// 自己的根目录。
	Open(ctx context.Context, loc Locator) (Transcript, error)
}
