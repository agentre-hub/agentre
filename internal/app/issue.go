package app

import (
	"sort"

	"github.com/agentre-hub/agentre/internal/service/issue_svc"
)

// IssueItem issue 摘要（含标签），列表 / 看板 / 详情共用。
type IssueItem struct {
	ID          int64        `json:"id"`
	ProjectID   int64        `json:"projectID"`
	Title       string       `json:"title"`
	Body        string       `json:"body"`
	State       string       `json:"state"`
	AgentStatus string       `json:"agentStatus"`
	Source      string       `json:"source"`
	ClosedAt    int64        `json:"closedAt"`
	Createtime  int64        `json:"createtime"`
	Updatetime  int64        `json:"updatetime"`
	Labels      []*LabelItem `json:"labels"`
	Stage       string       `json:"stage"`
	Position    float64      `json:"position"`
	// 执行归属（Agent / 机器 / 模型）。本轮两端都不读它们，只是随任务一并往返。
	AssigneeAgentID int64  `json:"assigneeAgentID"`
	AgentBackendID  int64  `json:"agentBackendID"`
	LLMProviderKey  string `json:"llmProviderKey"`
	LLMModelKey     string `json:"llmModelKey"`
	SessionID       int64  `json:"sessionID"`
}

// LabelItem 标签 DTO。UsageCount 是「被 N 个任务使用」，删除前要说清的爆炸半径。
type LabelItem struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Tone       string `json:"tone"`
	UsageCount int64  `json:"usageCount"`
}

// ProjectIssueCount 项目选择器每一项右侧的计数：该项目**及其子树**里未完成的任务数
// （projectID = 0 是「未归属」）。不随筛选变化。
type ProjectIssueCount struct {
	ProjectID int64 `json:"projectID"`
	Count     int64 `json:"count"`
}

// IssueListRequest 看板的六个筛选条件：「项目」是 scope + projectID 这一对，其余
// 五个各占一格。
type IssueListRequest struct {
	Scope          string  `json:"scope"` // "" / all / unassigned / project
	ProjectID      int64   `json:"projectID"`
	Keyword        string  `json:"keyword"`
	LabelIDs       []int64 `json:"labelIDs"`
	LabelMatchAll  bool    `json:"labelMatchAll"`
	NoLabel        bool    `json:"noLabel"`
	UpdatedFrom    int64   `json:"updatedFrom"`
	UpdatedTo      int64   `json:"updatedTo"`
	CreatedFrom    int64   `json:"createdFrom"`
	CreatedTo      int64   `json:"createdTo"`
	DoneWithinDays int     `json:"doneWithinDays"`
	Sort           string  `json:"sort"`
}

type IssueListResponse struct {
	Issues []*IssueItem `json:"issues"`
	// StageCounts / StageTotals 是列头的「命中 / 全部」：前者吃全部筛选条件，
	// 后者只吃项目范围。
	StageCounts map[string]int64 `json:"stageCounts"`
	StageTotals map[string]int64 `json:"stageTotals"`
	// ProjectCounts 项目选择器的子树计数。
	ProjectCounts []*ProjectIssueCount `json:"projectCounts"`
}

type IssueCreateRequest struct {
	ProjectID       int64   `json:"projectID"`
	Title           string  `json:"title"`
	Body            string  `json:"body"`
	LabelIDs        []int64 `json:"labelIDs"`
	Stage           string  `json:"stage"`
	AssigneeAgentID int64   `json:"assigneeAgentID"`
	AgentBackendID  int64   `json:"agentBackendID"`
	LLMProviderKey  string  `json:"llmProviderKey"`
	LLMModelKey     string  `json:"llmModelKey"`
}

type IssueMoveRequest struct {
	ID      int64  `json:"id"`
	Stage   string `json:"stage"`
	AfterID int64  `json:"afterID"`
}

type IssueUpdateRequest struct {
	ID              int64   `json:"id"`
	ProjectID       int64   `json:"projectID"`
	Title           string  `json:"title"`
	Body            string  `json:"body"`
	LabelIDs        []int64 `json:"labelIDs"`
	Stage           string  `json:"stage"` // "" = 不改阶段
	AssigneeAgentID int64   `json:"assigneeAgentID"`
	AgentBackendID  int64   `json:"agentBackendID"`
	LLMProviderKey  string  `json:"llmProviderKey"`
	LLMModelKey     string  `json:"llmModelKey"`
}

// IssueLabelRequest 建标签（id = 0）与改名 / 换色（id != 0）共用。
type IssueLabelRequest struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Tone string `json:"tone"`
}

func toLabelItem(d *issue_svc.LabelDetail) *LabelItem {
	return &LabelItem{
		ID: d.Label.ID, Name: d.Label.Name, Tone: d.Label.Tone, UsageCount: d.UsageCount,
	}
}

func toListIssuesRequest(req *IssueListRequest) *issue_svc.ListIssuesRequest {
	return &issue_svc.ListIssuesRequest{
		Scope: req.Scope, ProjectID: req.ProjectID,
		Keyword: req.Keyword, LabelIDs: req.LabelIDs,
		LabelMatchAll: req.LabelMatchAll, NoLabel: req.NoLabel,
		UpdatedFrom: req.UpdatedFrom, UpdatedTo: req.UpdatedTo,
		CreatedFrom: req.CreatedFrom, CreatedTo: req.CreatedTo,
		DoneWithinDays: req.DoneWithinDays, Sort: req.Sort,
	}
}

func toCreateIssueRequest(req *IssueCreateRequest) *issue_svc.CreateIssueRequest {
	return &issue_svc.CreateIssueRequest{
		ProjectID: req.ProjectID, Title: req.Title, Body: req.Body,
		LabelIDs: req.LabelIDs, Stage: req.Stage,
		Execution: issue_svc.ExecutionAssignment{
			AssigneeAgentID: req.AssigneeAgentID, AgentBackendID: req.AgentBackendID,
			LLMProviderKey: req.LLMProviderKey, LLMModelKey: req.LLMModelKey,
		},
	}
}

func toUpdateIssueRequest(req *IssueUpdateRequest) *issue_svc.UpdateIssueRequest {
	return &issue_svc.UpdateIssueRequest{
		ID: req.ID, ProjectID: req.ProjectID, Title: req.Title, Body: req.Body,
		LabelIDs: req.LabelIDs, Stage: req.Stage,
		Execution: issue_svc.ExecutionAssignment{
			AssigneeAgentID: req.AssigneeAgentID, AgentBackendID: req.AgentBackendID,
			LLMProviderKey: req.LLMProviderKey, LLMModelKey: req.LLMModelKey,
		},
	}
}

func toIssueListResponse(resp *issue_svc.ListIssuesResponse) *IssueListResponse {
	items := make([]*IssueItem, 0, len(resp.Issues))
	for _, d := range resp.Issues {
		items = append(items, toIssueItem(d))
	}
	// map 的遍历序不定，按 projectID 排一下，前端拿到的顺序才是稳定的。
	projectIDs := make([]int64, 0, len(resp.ProjectCounts))
	for id := range resp.ProjectCounts {
		projectIDs = append(projectIDs, id)
	}
	sort.Slice(projectIDs, func(i, j int) bool { return projectIDs[i] < projectIDs[j] })
	counts := make([]*ProjectIssueCount, 0, len(projectIDs))
	for _, id := range projectIDs {
		counts = append(counts, &ProjectIssueCount{ProjectID: id, Count: resp.ProjectCounts[id]})
	}
	return &IssueListResponse{
		Issues:        items,
		StageCounts:   resp.StageCounts,
		StageTotals:   resp.StageTotals,
		ProjectCounts: counts,
	}
}

func toIssueItem(d *issue_svc.IssueDetail) *IssueItem {
	labels := make([]*LabelItem, 0, len(d.Labels))
	for _, l := range d.Labels {
		// 卡片上的标签只需要字形与色调；使用数是标签管理那一屏的事。
		labels = append(labels, toLabelItem(&issue_svc.LabelDetail{Label: l}))
	}
	return &IssueItem{
		ID:              d.Issue.ID,
		ProjectID:       d.Issue.ProjectID,
		Title:           d.Issue.Title,
		Body:            d.Issue.Body,
		State:           d.Issue.State,
		AgentStatus:     d.Issue.AgentStatus,
		Source:          d.Issue.Source,
		ClosedAt:        d.Issue.ClosedAt,
		Createtime:      d.Issue.Createtime,
		Updatetime:      d.Issue.Updatetime,
		Labels:          labels,
		Stage:           d.Issue.Stage,
		Position:        d.Issue.Position,
		AssigneeAgentID: d.Issue.AssigneeAgentID,
		AgentBackendID:  d.Issue.AgentBackendID,
		LLMProviderKey:  d.Issue.LLMProviderKey,
		LLMModelKey:     d.Issue.LLMModelKey,
		SessionID:       d.Issue.SessionID,
	}
}

// IssueList 列出 issue。
func (a *App) IssueList(req *IssueListRequest) (*IssueListResponse, error) {
	resp, err := issue_svc.Default().List(a.ctx, toListIssuesRequest(req))
	if err != nil {
		return nil, err
	}
	return toIssueListResponse(resp), nil
}

// IssueGet 取单条 issue。
func (a *App) IssueGet(id int64) (*IssueItem, error) {
	d, err := issue_svc.Default().Get(a.ctx, id)
	if err != nil {
		return nil, err
	}
	return toIssueItem(d), nil
}

// IssueCreate 创建 issue。
func (a *App) IssueCreate(req *IssueCreateRequest) (*IssueItem, error) {
	d, err := issue_svc.Default().Create(a.ctx, toCreateIssueRequest(req))
	if err != nil {
		return nil, err
	}
	return toIssueItem(d), nil
}

// IssueUpdate 更新 issue。
func (a *App) IssueUpdate(req *IssueUpdateRequest) (*IssueItem, error) {
	d, err := issue_svc.Default().Update(a.ctx, toUpdateIssueRequest(req))
	if err != nil {
		return nil, err
	}
	return toIssueItem(d), nil
}

// IssueDelete 软删 issue。
func (a *App) IssueDelete(id int64) error {
	return issue_svc.Default().Delete(a.ctx, id)
}

// IssueListLabels 列出全部标签（含「被 N 个任务使用」）。
func (a *App) IssueListLabels() ([]*LabelItem, error) {
	labels, err := issue_svc.Default().ListLabels(a.ctx)
	if err != nil {
		return nil, err
	}
	items := make([]*LabelItem, 0, len(labels))
	for _, l := range labels {
		items = append(items, toLabelItem(l))
	}
	return items, nil
}

// IssueCreateLabel 新建标签。
func (a *App) IssueCreateLabel(req *IssueLabelRequest) (*LabelItem, error) {
	d, err := issue_svc.Default().CreateLabel(a.ctx, &issue_svc.LabelRequest{
		Name: req.Name, Tone: req.Tone,
	})
	if err != nil {
		return nil, err
	}
	return toLabelItem(d), nil
}

// IssueUpdateLabel 改名 / 换色。
func (a *App) IssueUpdateLabel(req *IssueLabelRequest) (*LabelItem, error) {
	d, err := issue_svc.Default().UpdateLabel(a.ctx, &issue_svc.LabelRequest{
		ID: req.ID, Name: req.Name, Tone: req.Tone,
	})
	if err != nil {
		return nil, err
	}
	return toLabelItem(d), nil
}

// IssueDeleteLabel 软删标签，并把它从全部任务上摘掉。
func (a *App) IssueDeleteLabel(id int64) error {
	return issue_svc.Default().DeleteLabel(a.ctx, id)
}

// IssueMove 拖拽:改 stage + 列内 position。
func (a *App) IssueMove(req *IssueMoveRequest) (*IssueItem, error) {
	d, err := issue_svc.Default().Move(a.ctx, &issue_svc.MoveIssueRequest{
		ID: req.ID, Stage: req.Stage, AfterID: req.AfterID,
	})
	if err != nil {
		return nil, err
	}
	return toIssueItem(d), nil
}
