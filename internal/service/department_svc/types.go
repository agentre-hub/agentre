// Package department_svc 暴露部门的应用服务接口与请求/响应类型。
//
// 类型定义直接被 Wails 绑定层引用，会被 wails dev / wails build 提取为 TypeScript
// 类型暴露给前端，因此字段名要稳定、json tag 要明确。
package department_svc

// DepartmentItem 单条部门记录（已 join lead Agent 摘要 + 汇总计数）。
type DepartmentItem struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Description        string `json:"description"`
	Icon               string `json:"icon"`
	AccentColor        string `json:"accentColor"`
	ParentID           int64  `json:"parentId"`
	LeadAgentID        int64  `json:"leadAgentId"`
	LeadAgentName      string `json:"leadAgentName"`
	SortOrder          int    `json:"sortOrder"`
	DirectAgentCount   int    `json:"directAgentCount"`
	SubdepartmentCount int    `json:"subdepartmentCount"`
	MemberCount        int    `json:"memberCount"`
	Createtime         int64  `json:"createtime"`
	Updatetime         int64  `json:"updatetime"`
}

// BackendSummary 是 agent_backend_svc.BackendItem 的只读子集，给 AgentItem 内嵌使用。
type BackendSummary struct {
	ID                int64  `json:"id"`
	Type              string `json:"type"`
	Name              string `json:"name"`
	LLMProviderName   string `json:"llmProviderName"`
	LLMProviderModel  string `json:"llmProviderModel"`
	LLMProviderActive bool   `json:"llmProviderActive"`
}

// AgentSkillDTO 与 agent_entity.AgentSkillItem 同结构(plugin id 开关)。
type AgentSkillDTO struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

// AgentExecTargetItem 单条执行目标（R15）：backend 摘要 + 这一档自己的技能授权
// （R15e / 决策 33，存放位置已下沉到执行目标行）。列表顺序即 sort_order。
type AgentExecTargetItem struct {
	ID             int64           `json:"id"`
	AgentBackendID int64           `json:"agentBackendId"`
	Skills         []AgentSkillDTO `json:"skills"`
}

// AgentToolDTO 与 agent_entity.AgentToolItem 同结构，避免前端引用 entity 包。
type AgentToolDTO struct {
	Key     string `json:"key"`
	Enabled bool   `json:"enabled"`
}

// AgentItem 单条 Agent 记录（已 join 部门名 + backend 摘要）。
type AgentItem struct {
	ID              int64           `json:"id"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	AvatarColor     string          `json:"avatarColor"`
	AvatarIcon      string          `json:"avatarIcon"`
	AvatarDataURL   string          `json:"avatarDataUrl"`
	SystemBadge     string          `json:"systemBadge"`
	DepartmentID    int64           `json:"departmentId"`
	DepartmentName  string          `json:"departmentName"`
	ParentAgentID   int64           `json:"parentAgentId"`
	ParentAgentName string          `json:"parentAgentName"`
	Backend         *BackendSummary `json:"backend,omitempty"`
	SortOrder       int             `json:"sortOrder"`
	Prompt          []string        `json:"prompt"`
	// ExecTargets 是 R15 的有序执行目标列表（任务 12：组织架构页展示/编辑用），
	// 也是这条 Agent 的后端与技能授权的**唯一**出口：①（sort_order 最小的那一档）
	// 即从前那对 AgentBackendID/Skills 派生字段说的东西。写口同样只认它
	// （agent_svc.UpdateAgentRequest）。
	ExecTargets []AgentExecTargetItem `json:"execTargets"`
	Tools       []AgentToolDTO        `json:"tools"`
	Createtime  int64                 `json:"createtime"`
	Updatetime  int64                 `json:"updatetime"`
}

// LoadOrgRequest 占位。
type LoadOrgRequest struct{}

// LoadOrgResponse 一次性返回部门 + Agent 全量（前端首屏使用）。
type LoadOrgResponse struct {
	Departments    []*DepartmentItem `json:"departments"`
	Agents         []*AgentItem      `json:"agents"`
	AvailableTools []string          `json:"availableTools"`
}

// CreateDepartmentRequest 新建部门。
type CreateDepartmentRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	AccentColor string `json:"accentColor"`
	ParentID    int64  `json:"parentId"`
}

type CreateDepartmentResponse struct {
	Item *DepartmentItem `json:"item"`
}

// UpdateDepartmentRequest 更新部门（不允许改 parent_id，那走 Move）。
type UpdateDepartmentRequest struct {
	ID          int64  `json:"id" binding:"required"`
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	AccentColor string `json:"accentColor"`
	LeadAgentID int64  `json:"leadAgentId"`
}

type UpdateDepartmentResponse struct {
	Item *DepartmentItem `json:"item"`
}

// MoveDepartmentRequest 改父部门 + 同级排序。
type MoveDepartmentRequest struct {
	ID           int64 `json:"id" binding:"required"`
	NewParentID  int64 `json:"newParentId"`
	NewSortOrder int   `json:"newSortOrder"`
}

type MoveDepartmentResponse struct {
	Item *DepartmentItem `json:"item"`
}

// DeleteDepartmentRequest 软删部门。Strategy: "reparent"（默认） | "cascade"。
type DeleteDepartmentRequest struct {
	ID       int64  `json:"id" binding:"required"`
	Strategy string `json:"strategy"`
}

type DeleteDepartmentResponse struct{}

// ReorderDepartmentsRequest 同级密集重排:orderedIds 必须是该父级下的完整集合。
type ReorderDepartmentsRequest struct {
	ParentID   int64   `json:"parentId"`
	OrderedIDs []int64 `json:"orderedIds"`
}
