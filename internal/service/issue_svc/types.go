package issue_svc

import "github.com/agentre-hub/agentre/internal/model/entity/issue_entity"

// 项目范围的三档（见 spec「项目范围」）。空串等同 ScopeAll。
const (
	ScopeAll        = "all"        // 全部项目：不加任何项目条件
	ScopeUnassigned = "unassigned" // 未归属：project_id = 0 那一档
	ScopeProject    = "project"    // 某个项目**及其整棵子树**
)

// ExecutionAssignment 执行归属（Agent / 机器 / 模型）。本轮两端都不读它们，也不会
// 因此启动任何执行（决策 9）——只是随任务一并保存。
type ExecutionAssignment struct {
	AssigneeAgentID int64
	AgentBackendID  int64
	LLMProviderKey  string
	LLMModelKey     string
}

type CreateIssueRequest struct {
	ProjectID int64
	Title     string
	Body      string
	Stage     string // "" = todo
	LabelIDs  []int64
	// Execution 执行归属；零值 = 不指定（跟随 Agent 绑定）。
	Execution ExecutionAssignment
}

type MoveIssueRequest struct {
	ID      int64
	Stage   string
	AfterID int64 // 0 = 落在目标列顶部
}

type UpdateIssueRequest struct {
	ID        int64
	ProjectID int64
	Title     string
	Body      string
	// Stage "" = 不改阶段。编辑态里阶段仍可改（表单从列继承默认值，之后仍能换列）。
	Stage    string
	LabelIDs []int64
	// Execution 执行归属；零值 = 不指定（跟随 Agent 绑定）。
	Execution ExecutionAssignment
}

// ListIssuesRequest 看板的六个筛选条件。「项目」是 Scope + ProjectID 这一对，其余
// 五个逐条落到 issue_repo.ListFilter 上。
type ListIssuesRequest struct {
	Scope     string // "" / ScopeAll / ScopeUnassigned / ScopeProject
	ProjectID int64  // 只在 Scope == ScopeProject 时有意义
	// Keyword 匹配标题、描述与 `#编号`。
	Keyword string
	// LabelIDs + LabelMatchAll = 「任意一个」/「全部满足」；NoLabel = 只看没有标签的。
	LabelIDs      []int64
	LabelMatchAll bool
	NoLabel       bool
	// 更新时间 / 创建时间各一段闭区间（毫秒 epoch，0 = 该端不限）。
	UpdatedFrom int64
	UpdatedTo   int64
	CreatedFrom int64
	CreatedTo   int64
	// DoneWithinDays 「已完成保留多久」：30 / 90，0 = 全部。它替代了写死的
	// 「只显示最近 N 个」，是一个能被摘掉的条件。
	DoneWithinDays int
	Sort           string
}

// IssueDetail issue + 已水合标签。
type IssueDetail struct {
	Issue  *issue_entity.Issue
	Labels []*issue_entity.Label
}

// LabelDetail 标签 + 「被 N 个任务使用」。删除标签前要说清的爆炸半径就是这个数。
type LabelDetail struct {
	Label      *issue_entity.Label
	UsageCount int64
}

// LabelRequest 建标签（ID = 0）与改名 / 换色（ID != 0）共用。
type LabelRequest struct {
	ID   int64
	Name string
	Tone string
}

// ListIssuesResponse 看板一次查询的全部结果。
type ListIssuesResponse struct {
	Issues []*IssueDetail
	// StageCounts 各列的**命中**数（吃全部筛选条件）。
	StageCounts map[string]int64
	// StageTotals 各列的**全部**数（只吃项目范围）。列头显示「命中 / 全部」，两个
	// 数一起随筛选缩水的话分母就没有意义了。
	StageTotals map[string]int64
	// ProjectCounts 每个项目**及其子树**里未完成的任务数（键 0 = 未归属），
	// **不随筛选变化** —— 打开项目选择器的目的就是判断该切到哪。
	ProjectCounts map[int64]int64
}
