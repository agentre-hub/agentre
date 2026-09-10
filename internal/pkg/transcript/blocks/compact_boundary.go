package blocks

import cagoblocks "github.com/cago-frame/agents/agent/blocks"

// CompactBoundaryBlock 记录 runtime 通报的会话上下文压缩边界
// (claudecode system.compact_boundary;auto / manual 同等)。
//
// PreTokens 是压缩前上下文 token 数(用于 UI 文案);Trigger 为 "auto"|"manual"。
// At 是 unix 毫秒;字段缺失保持零值,前端按零值退化展示。
//
// Audience ToUI:不喂给 LLM history,只用于前端折叠 + 分隔卡片。
type CompactBoundaryBlock struct {
	PreTokens int    `json:"preTokens,omitempty"`
	Trigger   string `json:"trigger,omitempty"`
	At        int64  `json:"at"`
}

func (CompactBoundaryBlock) Type() string                      { return "compact_boundary" }
func (CompactBoundaryBlock) Audience() cagoblocks.AudienceMask { return cagoblocks.ToUI }

func init() { cagoblocks.RegisterFactory[CompactBoundaryBlock]() }
