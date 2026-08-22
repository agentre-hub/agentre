// Package project_svc 提供 Project 模块的业务逻辑层。
//
// Project 是「工作上下文」一等公民：名字 + 本地路径 + 成员 Agent。
package project_svc

import "github.com/agentre-ai/agentre/internal/model/entity/project_entity"

// CreateProjectRequest 新建项目入参。
//
// Path 必填、绝对路径；Git 仓库检测由 ProjectDetectGitRepo 完成。
type CreateProjectRequest struct {
	ParentID    int64
	Name        string
	Icon        string
	Color       string
	Description string
	Path        string
	// InitialAgentIDs 创建后立即写入 project_agents 的直接成员；可为空。
	InitialAgentIDs []int64
}

// UpdateProjectRequest 更新项目入参。
//
// 不允许跨树移动（改 ParentID）—— 那件事走 MoveProjectRequest。两者分开是因为
// 判据完全不同：改字段只管重名，换父级还要管父级在不在、停没停用、以及会不会成环。
type UpdateProjectRequest struct {
	ID          int64
	Name        string
	Icon        string
	Color       string
	Description string
}

// MoveProjectRequest 把一个项目挂到另一个父项目下（`NewParentID == 0` = 挂到根上）。
//
// 与 ReorderProjectsRequest 分开：那一条的 SQL 带 `AND parent_id = ?`，只在同一个
// 父下排序，拿它反父级会 RowsAffected != 1 直接报错。
type MoveProjectRequest struct {
	ID          int64
	NewParentID int64
}

// ReorderProjectsRequest 调整同一 parent 下项目展示顺序。
type ReorderProjectsRequest struct {
	ParentID   int64
	OrderedIDs []int64
}

// MergeProjectsRequest 合并两个本地项目行（R11a）。SourceID / TargetID 不区分谁是
// 「主」谁是「被合并」——两者对等地各自可能是账号侧/本机侧、可能是较早/较晚创建，
// 由 Merge 内部按规则挑赢家，与用户先选中哪一个无关。
type MergeProjectsRequest struct {
	SourceID int64
	TargetID int64
}

// ProjectAgentMember 项目成员视图，区分直接成员 vs 继承成员。
type ProjectAgentMember struct {
	AgentID       int64
	JoinedAt      int64
	FromProjectID int64  // 继承来源；== ProjectDetail.ID 时即直接成员
	FromName      string // 继承来源项目名；直接成员留空
	AgentName     string
	AvatarColor   string
	AvatarIcon    string
	AvatarDataURL string
}

// ProjectDetail Get() 返回的项目详情 + 成员列表。
type ProjectDetail struct {
	Project          *project_entity.Project
	DirectMembers    []*ProjectAgentMember
	InheritedMembers []*ProjectAgentMember
}

// ProjectNode 项目树节点 —— ListTree() 返回的形态，子项目嵌套挂在 Children。
type ProjectNode struct {
	Project  *project_entity.Project
	Children []*ProjectNode
}

// GitRepoInfo 路径下 Git 仓库探测结果，新建项目模态用。
type GitRepoInfo struct {
	IsGitRepo     bool
	CurrentBranch string
	Origin        string
}
