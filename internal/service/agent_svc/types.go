// Package agent_svc 暴露 Agent 的应用服务接口与请求/响应类型。
package agent_svc

import "github.com/agentre-hub/agentre/internal/service/department_svc"

// 复用 department_svc 里的 AgentItem 作为 service 返回 — 避免重复定义。
type AgentItem = department_svc.AgentItem

// CreateAgentRequest 新建 Agent。SystemBadge 由 service 强制忽略（CEO 仅 seed 写入）。
type CreateAgentRequest struct {
	Name           string                         `json:"name" binding:"required"`
	Description    string                         `json:"description"`
	AvatarColor    string                         `json:"avatarColor"`
	AvatarIcon     string                         `json:"avatarIcon"`
	DepartmentID   int64                          `json:"departmentId"`
	ParentAgentID  int64                          `json:"parentAgentId"`
	AgentBackendID int64                          `json:"agentBackendId"`
	Prompt         []string                       `json:"prompt"`
	Skills         []department_svc.AgentSkillDTO `json:"skills"`
	Tools          []department_svc.AgentToolDTO  `json:"tools"`
}

type CreateAgentResponse struct {
	Item *AgentItem `json:"item"`
}

// ExecTargetInputDTO 是 UpdateAgentRequest.ExecTargets 里的一项：R15 的执行目标 +
// 这一档自己的技能授权（R15e / 决策 33）。数组顺序即 sort_order。
type ExecTargetInputDTO struct {
	AgentBackendID int64                          `json:"agentBackendId" binding:"required"`
	Skills         []department_svc.AgentSkillDTO `json:"skills"`
}

// UpdateAgentRequest 更新 Agent；禁止改 system_badge / department_id。
//
// ExecTargets 是 R15 的有序执行目标列表——每次保存都是整份替换（与 Name/Prompt/Tools 等其它字段一样，
// 这里从来都是全量快照式写入，不是增量 patch）。至少要有一项：列表为空的 Agent
// 不能起会话，这条校验在 svc.Update 里做，界面在保存前用同一条件禁用保存
// （R15：「列表为空的 Agent 不能起会话——界面在保存时就要求至少一项」）。
type UpdateAgentRequest struct {
	ID          int64                         `json:"id" binding:"required"`
	Name        string                        `json:"name" binding:"required"`
	Description string                        `json:"description"`
	AvatarColor string                        `json:"avatarColor"`
	AvatarIcon  string                        `json:"avatarIcon"`
	Prompt      []string                      `json:"prompt"`
	ExecTargets []ExecTargetInputDTO          `json:"execTargets"`
	Tools       []department_svc.AgentToolDTO `json:"tools"`
	// OrderOverride 非 nil 时，Update 只写**本端**执行目标顺序覆盖（R14 / R16），
	// 不碰账号默认顺序、不同步：非空数组 = 按此顺序存覆盖，空数组 = 清除覆盖
	// （「恢复为账号默认顺序」）。nil = 走既有账号默认全量写入。其余字段在此路径
	// 下被忽略——顺序覆盖只表达排列，不增删档。
	OrderOverride []int64 `json:"orderOverride,omitempty"`
}

type UpdateAgentResponse struct {
	Item *AgentItem `json:"item"`
}

// MoveAgentRequest 换挂载位置 + 同级排序。CEO 拒绝。
type MoveAgentRequest struct {
	ID               int64 `json:"id" binding:"required"`
	NewDepartmentID  int64 `json:"newDepartmentId"`
	NewParentAgentID int64 `json:"newParentAgentId"`
	NewSortOrder     int   `json:"newSortOrder"`
}

type MoveAgentResponse struct {
	Item *AgentItem `json:"item"`
}

// DeleteAgentRequest 软删 Agent。CEO 拒绝。
type DeleteAgentRequest struct {
	ID int64 `json:"id" binding:"required"`
}

type DeleteAgentResponse struct{}

// UploadAvatarRequest 写入 Agent 头像（base64 data URL）。
// DataURL 形如 "data:image/png;base64,..." —— service 会校验前缀与字节数。
type UploadAvatarRequest struct {
	ID      int64  `json:"id" binding:"required"`
	DataURL string `json:"dataUrl" binding:"required"`
}

type UploadAvatarResponse struct {
	Item *AgentItem `json:"item"`
}

// ReorderAgentsRequest 同级密集重排:orderedIds 必须是该组的完整集合。
type ReorderAgentsRequest struct {
	DepartmentID  int64   `json:"departmentId"`
	ParentAgentID int64   `json:"parentAgentId"`
	OrderedIDs    []int64 `json:"orderedIds"`
}

// DeleteAvatarRequest 清空 Agent 头像，回退到首字母。
type DeleteAvatarRequest struct {
	ID int64 `json:"id" binding:"required"`
}

type DeleteAvatarResponse struct {
	Item *AgentItem `json:"item"`
}

// SetPinnedRequest 切换 Agent 用户置顶（侧栏混排列表浮顶）。
type SetPinnedRequest struct {
	ID     int64 `json:"id" binding:"required"`
	Pinned bool  `json:"pinned"`
}

type SetPinnedResponse struct {
	ID     int64 `json:"id"`
	Pinned bool  `json:"pinned"`
}

// SetMemoryEnabledRequest 切换 Agent 的记忆开关。
type SetMemoryEnabledRequest struct {
	ID      int64 `json:"id" binding:"required"`
	Enabled bool  `json:"enabled"`
}

type SetMemoryEnabledResponse struct {
	ID      int64 `json:"id"`
	Enabled bool  `json:"enabled"`
}

// ExecConstraintDTO 成员「在哪跑」约束：mode = any / labels / devices；labels 模式下设备须带
// 全部所选标签，devices 模式下只在这几台设备（规范指纹）上跑。
type ExecConstraintDTO struct {
	Mode    string   `json:"mode"`
	Labels  []string `json:"labels"`
	Devices []string `json:"devices"`
}

// GetMemberEngineRequest 读成员的执行设置。
type GetMemberEngineRequest struct {
	AgentID int64 `json:"agentId" binding:"required"`
}

// SetMemberEngineRequest 设成员引用的引擎配置；EngineProfileID 0 = 跟随部门。
type SetMemberEngineRequest struct {
	AgentID         int64 `json:"agentId" binding:"required"`
	EngineProfileID int64 `json:"engineProfileId"`
}

// SetMemberExecConstraintRequest 设成员的「在哪跑」约束。
type SetMemberExecConstraintRequest struct {
	AgentID    int64             `json:"agentId" binding:"required"`
	Constraint ExecConstraintDTO `json:"constraint"`
}

// SetMemberSkillGrantsRequest 整份替换成员的技能授权（继承 / 强制开 / 强制关：不在列表里 = 继承）。
// 传入只含有效授权的列表即「一键移除失效授权」。
type SetMemberSkillGrantsRequest struct {
	AgentID int64                          `json:"agentId" binding:"required"`
	Skills  []department_svc.AgentSkillDTO `json:"skills"`
}

// MemberEngineView 成员的执行设置。EngineProfileID 是成员自己的引用（0 = 跟随）；Inherited
// 是跟随时会落到的那份（选择弹层第一项「跟随 <部门> · <配置名>」），Effective 是现在生效的
// 那份。SkillGrants 是与生效运行时对得上的授权，InvalidSkillGrants 是对不上、待移除的那些。
type MemberEngineView struct {
	AgentID            int64                               `json:"agentId"`
	EngineProfileID    int64                               `json:"engineProfileId"`
	Constraint         ExecConstraintDTO                   `json:"constraint"`
	SkillGrants        []department_svc.AgentSkillDTO      `json:"skillGrants"`
	InvalidSkillGrants []department_svc.AgentSkillDTO      `json:"invalidSkillGrants"`
	Inherited          *department_svc.EffectiveEngineView `json:"inherited"`
	Effective          *department_svc.EffectiveEngineView `json:"effective"`
}

// ResolveMemberEngineRequest 解析成员生效的引擎配置。
type ResolveMemberEngineRequest struct {
	AgentID int64 `json:"agentId" binding:"required"`
}

// ResolveMemberEngineResponse 生效的引擎配置及它从哪一级继承来；无处可用时 Effective 为 nil。
type ResolveMemberEngineResponse struct {
	Effective *department_svc.EffectiveEngineView `json:"effective"`
}
