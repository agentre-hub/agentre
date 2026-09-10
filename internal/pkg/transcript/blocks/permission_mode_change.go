package blocks

import cagoblocks "github.com/cago-frame/agents/agent/blocks"

// PermissionModeChangeBlock 记录 runtime 通报的 permission_mode 切换。
// At 是 unix 毫秒;From 为空表示首次设定(无旧值)。
type PermissionModeChangeBlock struct {
	From string `json:"from,omitempty"`
	To   string `json:"to"`
	At   int64  `json:"at"`
}

func (PermissionModeChangeBlock) Type() string                      { return "permission_mode_change" }
func (PermissionModeChangeBlock) Audience() cagoblocks.AudienceMask { return cagoblocks.ToUI }

func init() { cagoblocks.RegisterFactory[PermissionModeChangeBlock]() }
