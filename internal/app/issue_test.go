package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/issue_entity"
	"github.com/agentre-hub/agentre/internal/service/issue_svc"
)

func TestToIssueItem(t *testing.T) {
	item := toIssueItem(&issue_svc.IssueDetail{
		Issue:  &issue_entity.Issue{ID: 4, Title: "t", State: "open", AgentStatus: "idle"},
		Labels: []*issue_entity.Label{{ID: 1, Name: "bug", Tone: "bug"}},
	})
	require.NotNil(t, item)
	assert.Equal(t, int64(4), item.ID)
	assert.Equal(t, "open", item.State)
	require.Len(t, item.Labels, 1)
	assert.Equal(t, "bug", item.Labels[0].Tone)
}

func TestToIssueItem_NoLabels(t *testing.T) {
	item := toIssueItem(&issue_svc.IssueDetail{Issue: &issue_entity.Issue{ID: 1, Title: "x", State: "open"}})
	assert.NotNil(t, item.Labels) // 非 nil 空切片，便于前端
	assert.Len(t, item.Labels, 0)
}

func TestToIssueItem_MapsStagePosition(t *testing.T) {
	d := &issue_svc.IssueDetail{Issue: &issue_entity.Issue{
		ID: 1, Stage: issue_entity.StageDoing, Position: 12.5, AssigneeAgentID: 3, SessionID: 4,
	}}
	item := toIssueItem(d)
	assert.Equal(t, "doing", item.Stage)
	assert.Equal(t, 12.5, item.Position)
	assert.Equal(t, int64(3), item.AssigneeAgentID)
	assert.Equal(t, int64(4), item.SessionID)
}

// TestToIssueItem_MapsExecutionAssignment 执行归属三个字段必须原样进出 DTO：本轮
// 没有任何路径读它们，映射漏一个不会有别的用例变红，但表单就存了等于没存。
func TestToIssueItem_MapsExecutionAssignment(t *testing.T) {
	item := toIssueItem(&issue_svc.IssueDetail{Issue: &issue_entity.Issue{
		ID: 1, AssigneeAgentID: 3, AgentBackendID: 11,
		LLMProviderKey: "openai", LLMModelKey: "gpt-5",
	}})
	assert.Equal(t, int64(3), item.AssigneeAgentID)
	assert.Equal(t, int64(11), item.AgentBackendID)
	assert.Equal(t, "openai", item.LLMProviderKey)
	assert.Equal(t, "gpt-5", item.LLMModelKey)
}

// TestToLabelItem_ReportsUsage 标签管理列表里的「被 N 个任务使用」。
func TestToLabelItem_ReportsUsage(t *testing.T) {
	item := toLabelItem(&issue_svc.LabelDetail{
		Label:      &issue_entity.Label{ID: 2, Name: "bug", Tone: issue_entity.ToneRed},
		UsageCount: 4,
	})
	assert.Equal(t, "bug", item.Name)
	assert.Equal(t, issue_entity.ToneRed, item.Tone)
	assert.Equal(t, int64(4), item.UsageCount)
}

// TestToListIssuesRequest_CarriesEveryFilterCondition 六个筛选条件全都要过得去绑定层
// —— 少映射一个的表现是「筛选面板点了没反应」。
func TestToListIssuesRequest_CarriesEveryFilterCondition(t *testing.T) {
	req := toListIssuesRequest(&IssueListRequest{
		Scope: issue_svc.ScopeProject, ProjectID: 7,
		Keyword: "#179", LabelIDs: []int64{1, 2}, LabelMatchAll: true, NoLabel: true,
		UpdatedFrom: 10, UpdatedTo: 20, CreatedFrom: 30, CreatedTo: 40,
		DoneWithinDays: 90, Sort: "position",
	})
	assert.Equal(t, issue_svc.ScopeProject, req.Scope)
	assert.Equal(t, int64(7), req.ProjectID)
	assert.Equal(t, "#179", req.Keyword)
	assert.Equal(t, []int64{1, 2}, req.LabelIDs)
	assert.True(t, req.LabelMatchAll)
	assert.True(t, req.NoLabel)
	assert.Equal(t, int64(10), req.UpdatedFrom)
	assert.Equal(t, int64(20), req.UpdatedTo)
	assert.Equal(t, int64(30), req.CreatedFrom)
	assert.Equal(t, int64(40), req.CreatedTo)
	assert.Equal(t, 90, req.DoneWithinDays)
	assert.Equal(t, "position", req.Sort)
}

// TestToIssueListResponse 列头的「命中 / 全部」与选择器的子树计数都要到得了前端。
func TestToIssueListResponse(t *testing.T) {
	resp := toIssueListResponse(&issue_svc.ListIssuesResponse{
		Issues:        []*issue_svc.IssueDetail{{Issue: &issue_entity.Issue{ID: 1}}},
		StageCounts:   map[string]int64{issue_entity.StageTodo: 1},
		StageTotals:   map[string]int64{issue_entity.StageTodo: 9},
		ProjectCounts: map[int64]int64{0: 3, 7: 4},
	})
	require.Len(t, resp.Issues, 1)
	assert.Equal(t, int64(1), resp.StageCounts[issue_entity.StageTodo])
	assert.Equal(t, int64(9), resp.StageTotals[issue_entity.StageTodo])
	counts := map[int64]int64{}
	for _, c := range resp.ProjectCounts {
		counts[c.ProjectID] = c.Count
	}
	assert.Equal(t, int64(3), counts[0])
	assert.Equal(t, int64(4), counts[7])
}

// TestToCreateIssueRequest_CarriesExecutionAssignment 建任务时三颗执行 pill 的值。
func TestToCreateIssueRequest_CarriesExecutionAssignment(t *testing.T) {
	req := toCreateIssueRequest(&IssueCreateRequest{
		Title: "t", Stage: issue_entity.StageDoing,
		AssigneeAgentID: 3, AgentBackendID: 11,
		LLMProviderKey: "openai", LLMModelKey: "gpt-5",
	})
	assert.Equal(t, issue_entity.StageDoing, req.Stage)
	assert.Equal(t, int64(3), req.Execution.AssigneeAgentID)
	assert.Equal(t, int64(11), req.Execution.AgentBackendID)
	assert.Equal(t, "openai", req.Execution.LLMProviderKey)
	assert.Equal(t, "gpt-5", req.Execution.LLMModelKey)
}

// TestToUpdateIssueRequest_CarriesStageAndExecution 编辑态里阶段仍可改。
func TestToUpdateIssueRequest_CarriesStageAndExecution(t *testing.T) {
	req := toUpdateIssueRequest(&IssueUpdateRequest{
		ID: 3, Title: "t", Stage: issue_entity.StageDone, AgentBackendID: 11,
	})
	assert.Equal(t, issue_entity.StageDone, req.Stage)
	assert.Equal(t, int64(11), req.Execution.AgentBackendID)
}
