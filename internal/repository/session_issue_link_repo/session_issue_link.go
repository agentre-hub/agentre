// Package session_issue_link_repo 提供会话 ↔ 看板任务关联(session_issue_links)的持久化访问。
package session_issue_link_repo

import (
	"context"
	"time"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm/clause"

	"github.com/agentre-hub/agentre/internal/model/entity/session_issue_link_entity"
)

//go:generate mockgen -source session_issue_link.go -destination mock_session_issue_link_repo/mock_session_issue_link.go

// SessionIssueLinkRepo 是会话 ↔ 任务关联的读写口。
type SessionIssueLinkRepo interface {
	// Link 关联一对(会话,任务);已关联时不报错也不重复写(幂等)。
	Link(ctx context.Context, sessionID, issueID int64) error
	// Unlink 取消一对关联;本来就没有时不报错(幂等)。
	Unlink(ctx context.Context, sessionID, issueID int64) error
	// ListBySession 交回本会话的全部关联,先关联的在前。
	ListBySession(ctx context.Context, sessionID int64) ([]*session_issue_link_entity.SessionIssueLink, error)
	// ListByIssue 交回关联了本任务的全部会话,先关联的在前。
	ListByIssue(ctx context.Context, issueID int64) ([]*session_issue_link_entity.SessionIssueLink, error)
	// CountByIssues 一次分组查询数出每个任务关联的会话数;没有关联的任务不在结果里。
	CountByIssues(ctx context.Context, issueIDs []int64) (map[int64]int64, error)
	// DeleteBySession 清掉本会话的全部关联(会话被删除时)。
	DeleteBySession(ctx context.Context, sessionID int64) error
	// DeleteByIssue 清掉本任务的全部关联(任务被删除时,含同步下行的删除)。
	DeleteByIssue(ctx context.Context, issueID int64) error
}

var defaultSessionIssueLink SessionIssueLinkRepo

func SessionIssueLink() SessionIssueLinkRepo             { return defaultSessionIssueLink }
func RegisterSessionIssueLink(impl SessionIssueLinkRepo) { defaultSessionIssueLink = impl }
func NewSessionIssueLink() SessionIssueLinkRepo          { return &sessionIssueLinkRepo{} }

type sessionIssueLinkRepo struct{}

func (r *sessionIssueLinkRepo) Link(ctx context.Context, sessionID, issueID int64) error {
	row := &session_issue_link_entity.SessionIssueLink{
		SessionID: sessionID, IssueID: issueID, Createtime: time.Now().UnixMilli(),
	}
	return db.Ctx(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(row).Error
}

func (r *sessionIssueLinkRepo) Unlink(ctx context.Context, sessionID, issueID int64) error {
	return db.Ctx(ctx).
		Where("session_id = ? AND issue_id = ?", sessionID, issueID).
		Delete(&session_issue_link_entity.SessionIssueLink{}).Error
}

func (r *sessionIssueLinkRepo) ListBySession(ctx context.Context, sessionID int64) ([]*session_issue_link_entity.SessionIssueLink, error) {
	var rows []*session_issue_link_entity.SessionIssueLink
	if err := db.Ctx(ctx).Where("session_id = ?", sessionID).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *sessionIssueLinkRepo) ListByIssue(ctx context.Context, issueID int64) ([]*session_issue_link_entity.SessionIssueLink, error) {
	var rows []*session_issue_link_entity.SessionIssueLink
	if err := db.Ctx(ctx).Where("issue_id = ?", issueID).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *sessionIssueLinkRepo) CountByIssues(ctx context.Context, issueIDs []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	if len(issueIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		IssueID int64 `gorm:"column:issue_id"`
		N       int64 `gorm:"column:n"`
	}
	if err := db.Ctx(ctx).Model(&session_issue_link_entity.SessionIssueLink{}).
		Select("issue_id, COUNT(*) AS n").
		Where("issue_id IN ?", issueIDs).
		Group("issue_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.IssueID] = row.N
	}
	return out, nil
}

func (r *sessionIssueLinkRepo) DeleteBySession(ctx context.Context, sessionID int64) error {
	return db.Ctx(ctx).Where("session_id = ?", sessionID).
		Delete(&session_issue_link_entity.SessionIssueLink{}).Error
}

func (r *sessionIssueLinkRepo) DeleteByIssue(ctx context.Context, issueID int64) error {
	return db.Ctx(ctx).Where("issue_id = ?", issueID).
		Delete(&session_issue_link_entity.SessionIssueLink{}).Error
}
