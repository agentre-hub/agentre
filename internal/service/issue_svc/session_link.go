package issue_svc

import (
	"context"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/issue_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/issue_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/session_issue_link_repo"
)

// session_link.go 是会话 ↔ 看板任务的多对多关联(spec 2026-11-24 决策 23):三个入口(看板
// 卡片「开始会话」、关联窗关联已有任务、关联窗新建任务)最终都落在这里。关联只存本机、
// 不同步,也从不改变任务阶段;任务 / 会话删除时的清理分别在 issueSvc.Delete、
// sync_svc 的任务墓碑下行和 chat_svc.Delete。

// LinkSession 关联一对(会话,任务)。会话或任务不存在时报错而不写关联;已关联时幂等。
// 只写关联行,不读写任务本身——阶段、位置、更新时间都不动。
func (s *issueSvc) LinkSession(ctx context.Context, sessionID, issueID int64) error {
	if _, err := s.requireSession(ctx, sessionID); err != nil {
		return err
	}
	issue, err := issue_repo.Issue().Find(ctx, issueID)
	if err != nil {
		return err
	}
	if issue == nil {
		return i18n.NewError(ctx, code.IssueNotFound)
	}
	return session_issue_link_repo.SessionIssueLink().Link(ctx, sessionID, issueID)
}

// UnlinkSession 取消一对关联;本来就没有时不是错误。
func (s *issueSvc) UnlinkSession(ctx context.Context, sessionID, issueID int64) error {
	return session_issue_link_repo.SessionIssueLink().Unlink(ctx, sessionID, issueID)
}

// CreateLinked 在「待办」阶段创建任务并立即关联到会话:项目取会话所属项目,标题缺省取会话
// 标题。创建成功而关联失败时把错误交给调用方(任务已存在,不假装整体成功)。
func (s *issueSvc) CreateLinked(ctx context.Context, req *CreateLinkedRequest) (*IssueDetail, error) {
	sess, err := s.requireSession(ctx, req.SessionID)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = sess.Title
	}
	detail, err := s.Create(ctx, &CreateIssueRequest{
		ProjectID: sess.ProjectID,
		Title:     title,
		Stage:     issue_entity.StageTodo,
	})
	if err != nil {
		return nil, err
	}
	if err := session_issue_link_repo.SessionIssueLink().Link(ctx, sess.ID, detail.Issue.ID); err != nil {
		logger.Ctx(ctx).Warn("issue_svc.CreateLinked: link created issue failed",
			zap.Int64("sessionId", sess.ID), zap.Int64("issueId", detail.Issue.ID), zap.Error(err))
		return nil, err
	}
	return detail, nil
}

// ListBySession 关联窗里本会话已关联的任务(先关联的在前)。已不存在的任务跳过;每项带
// 「另有 N 个会话」= 关联了它的会话总数 - 1(本会话自己)。
func (s *issueSvc) ListBySession(ctx context.Context, sessionID int64) ([]*LinkedIssueView, error) {
	links, err := session_issue_link_repo.SessionIssueLink().ListBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	issues := make([]*issue_entity.Issue, 0, len(links))
	ids := make([]int64, 0, len(links))
	for _, l := range links {
		issue, err := issue_repo.Issue().Find(ctx, l.IssueID)
		if err != nil {
			return nil, err
		}
		if issue == nil {
			continue
		}
		issues = append(issues, issue)
		ids = append(ids, issue.ID)
	}
	out := make([]*LinkedIssueView, 0, len(issues))
	if len(issues) == 0 {
		return out, nil
	}
	counts, err := session_issue_link_repo.SessionIssueLink().CountByIssues(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, issue := range issues {
		out = append(out, &LinkedIssueView{Issue: issue, OtherSessionCount: max(counts[issue.ID]-1, 0)})
	}
	return out, nil
}

// SessionsOf 看板卡片上某任务的关联会话(先关联的在前)。已不存在的会话跳过。
func (s *issueSvc) SessionsOf(ctx context.Context, issueID int64) ([]*LinkedSessionView, error) {
	links, err := session_issue_link_repo.SessionIssueLink().ListByIssue(ctx, issueID)
	if err != nil {
		return nil, err
	}
	out := make([]*LinkedSessionView, 0, len(links))
	for _, l := range links {
		sess, err := chat_repo.Session().Find(ctx, l.SessionID)
		if err != nil {
			return nil, err
		}
		if sess == nil {
			continue
		}
		out = append(out, &LinkedSessionView{
			SessionID: sess.ID, Title: sess.Title,
			AgentID: sess.AgentID, AgentName: agentName(ctx, sess.AgentID),
			AgentStatus: sess.AgentStatus,
		})
	}
	return out, nil
}

// SearchForSession 「关联任务」弹层的搜索:范围是本会话所属项目及其全部子项目的任务,会话
// 不属于任何项目时是全部任务;关键词匹配标题 / 描述 / #编号;已关联的 Linked = true。
func (s *issueSvc) SearchForSession(ctx context.Context, sessionID int64, keyword string) ([]*LinkCandidate, error) {
	sess, err := s.requireSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	filter := issue_repo.ListFilter{Keyword: strings.TrimSpace(keyword), Sort: "position"}
	if sess.ProjectID > 0 {
		tree, err := project_repo.Project().List(ctx)
		if err != nil {
			return nil, err
		}
		filter.ProjectIDs = collectProjectSubtree(tree, sess.ProjectID)
	}
	issues, err := issue_repo.Issue().List(ctx, filter)
	if err != nil {
		return nil, err
	}
	links, err := session_issue_link_repo.SessionIssueLink().ListBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	linked := make(map[int64]struct{}, len(links))
	for _, l := range links {
		linked[l.IssueID] = struct{}{}
	}
	out := make([]*LinkCandidate, 0, len(issues))
	for _, issue := range issues {
		_, ok := linked[issue.ID]
		out = append(out, &LinkCandidate{Issue: issue, Linked: ok})
	}
	return out, nil
}

func (s *issueSvc) requireSession(ctx context.Context, sessionID int64) (*chat_entity.Session, error) {
	sess, err := chat_repo.Session().Find(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
	}
	return sess, nil
}

func agentName(ctx context.Context, agentID int64) string {
	if agentID <= 0 {
		return ""
	}
	a, err := agent_repo.Agent().Find(ctx, agentID)
	if err != nil || a == nil {
		return ""
	}
	return a.Name
}
