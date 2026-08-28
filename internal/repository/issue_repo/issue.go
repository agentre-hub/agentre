// Package issue_repo 提供 Issue / Label 的持久化访问。
package issue_repo

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/consts"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/issue_entity"
)

//go:generate mockgen -source issue.go -destination mock_issue_repo/mock_issue.go

// ListFilter List / StageCounts 共用的查询过滤条件。看板上的六个条件里，除「项目」
// 需要 service 先把子树展开成 id 集合外，其余五个逐条落在这里。
type ListFilter struct {
	State string // "" = 不筛选；open / closed
	Stage string // "" = 不筛选
	// ProjectID 是当前 ListIssues Wails 契约的单项目筛选；ProjectIDs 非空时优先。
	ProjectID int64
	// ProjectIDs 项目范围：空 = 全部项目（不加条件）；[0] = 未归属；否则是「选中
	// 项目 + 其整棵子树」的 id 集合。空与 [0] 是两件事，别用 0 兼作「不筛选」。
	ProjectIDs []int64
	// Keyword 关键词：匹配标题、描述，以及 `#编号`（`#179` 与 `179` 都命中 id=179）。
	Keyword string
	// LabelIDs 非空 = 按标签筛选；LabelMatchAll 决定是「任意一个」还是「全部满足」。
	LabelIDs      []int64
	LabelMatchAll bool
	// NoLabel 只看没有任何标签的任务。
	NoLabel bool
	// UpdatedFrom/To、CreatedFrom/To 是两段闭区间（毫秒 epoch，0 = 该端不限）。
	UpdatedFrom int64
	UpdatedTo   int64
	CreatedFrom int64
	CreatedTo   int64
	// DoneAfter 「已完成保留多久」的绝对下界（毫秒 epoch，0 = 全部）：只裁剪已完成
	// 的卡片，未完成的一张都不受它影响。
	DoneAfter int64
	Sort      string // "position" = 按 stage, position ASC, id ASC；否则 updatetime DESC
}

// IssueRepo Issue 仓储接口。
type IssueRepo interface {
	Create(ctx context.Context, i *issue_entity.Issue) error
	Update(ctx context.Context, i *issue_entity.Issue) error
	Find(ctx context.Context, id int64) (*issue_entity.Issue, error)
	List(ctx context.Context, filter ListFilter) ([]*issue_entity.Issue, error)
	// ReassignProject 把 project_id 从 fromProjectID 整批改挂到 toProjectID（R11a
	// 的项目合并）。刻意**不带 status 过滤**：软删的 issue 在 List 里看不见，逐行
	// 改挂会把它们留在原地指向一个已消失的项目。
	ReassignProject(ctx context.Context, fromProjectID, toProjectID int64) error
	StageCounts(ctx context.Context, filter ListFilter) (map[string]int64, error)
	CountByState(ctx context.Context, projectID int64) (open int64, closed int64, err error)
	// CountUnfinishedByProject 按 project_id 统计**未完成**的任务数（键 0 = 未归属）。
	// 项目选择器每一项右侧的计数由它喂养，因此刻意不吃 ListFilter —— 那个数的用途
	// 就是判断该切到哪，跟着当前筛选缩水就失去了用途。
	CountUnfinishedByProject(ctx context.Context) (map[int64]int64, error)
	Delete(ctx context.Context, id int64) error
}

var defaultIssue IssueRepo

func Issue() IssueRepo             { return defaultIssue }
func RegisterIssue(impl IssueRepo) { defaultIssue = impl }
func NewIssue() IssueRepo          { return &issueRepo{} }

type issueRepo struct{}

func (r *issueRepo) Create(ctx context.Context, i *issue_entity.Issue) error {
	now := time.Now().UnixMilli()
	if i.Createtime == 0 {
		i.Createtime = now
	}
	i.Updatetime = now
	// 同步标识在行创建时就地生成，未登录期间也照常写入（R1/R12a）。
	i.EnsureSyncID()
	return db.Ctx(ctx).Create(i).Error
}

func (r *issueRepo) Update(ctx context.Context, i *issue_entity.Issue) error {
	i.Updatetime = time.Now().UnixMilli()
	// 还没有标识的历史行在下一次落库时补齐（JIT），已有标识的行原样保留（R1）。
	i.EnsureSyncID()
	return db.Ctx(ctx).Model(&issue_entity.Issue{}).
		Where("id = ? AND status = ?", i.ID, consts.ACTIVE).
		Updates(map[string]any{
			"project_id":        i.ProjectID,
			"title":             i.Title,
			"body":              i.Body,
			"state":             i.State,
			"agent_status":      i.AgentStatus,
			"stage":             i.Stage,
			"position":          i.Position,
			"assignee_agent_id": i.AssigneeAgentID,
			"agent_backend_id":  i.AgentBackendID,
			"llm_provider_key":  i.LLMProviderKey,
			"llm_model_key":     i.LLMModelKey,
			"session_id":        i.SessionID,
			"closed_at":         i.ClosedAt,
			"sync_id":           i.SyncID,
			"updatetime":        i.Updatetime,
		}).Error
}

func (r *issueRepo) Find(ctx context.Context, id int64) (*issue_entity.Issue, error) {
	out := &issue_entity.Issue{}
	err := db.Ctx(ctx).Where("id = ? AND status = ?", id, consts.ACTIVE).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *issueRepo) List(ctx context.Context, filter ListFilter) ([]*issue_entity.Issue, error) {
	order := "updatetime DESC, id DESC"
	if filter.Sort == "position" {
		order = "stage, position ASC, id ASC"
	}
	var rows []*issue_entity.Issue
	err := r.scoped(ctx, filter).Order(order).Find(&rows).Error
	return rows, err
}

// scoped 把 ListFilter 的每个条件译成 WHERE 子句。List 与 StageCounts 共用它 ——
// 列头的「命中」数与列表里的卡片必须出自同一套条件，各写一遍迟早对不上。
func (r *issueRepo) scoped(ctx context.Context, filter ListFilter) *gorm.DB {
	q := db.Ctx(ctx).Model(&issue_entity.Issue{}).Where("status = ?", consts.ACTIVE)
	if filter.State != "" {
		q = q.Where("state = ?", filter.State)
	}
	if filter.Stage != "" {
		q = q.Where("stage = ?", filter.Stage)
	}
	if len(filter.ProjectIDs) > 0 {
		q = q.Where("project_id IN ?", filter.ProjectIDs)
	} else if filter.ProjectID > 0 {
		q = q.Where("project_id = ?", filter.ProjectID)
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		like := "%" + escapeLike(keyword) + "%"
		if number, ok := issueNumber(keyword); ok {
			q = q.Where(`title LIKE ? ESCAPE '\' OR body LIKE ? ESCAPE '\' OR id = ?`, like, like, number)
		} else {
			q = q.Where(`title LIKE ? ESCAPE '\' OR body LIKE ? ESCAPE '\'`, like, like)
		}
	}
	if len(filter.LabelIDs) > 0 {
		sub := db.Ctx(ctx).Model(&issue_entity.IssueLabel{}).
			Select("issue_id").Where("label_id IN ?", filter.LabelIDs)
		if filter.LabelMatchAll {
			// 「全部满足」= 这个 issue 上出现了全部被选中的标签。DISTINCT 是为了
			// 让计数不受重复关联行影响。
			sub = sub.Group("issue_id").Having("COUNT(DISTINCT label_id) = ?", len(filter.LabelIDs))
		}
		q = q.Where("id IN (?)", sub)
	}
	if filter.NoLabel {
		q = q.Where("id NOT IN (?)", db.Ctx(ctx).Model(&issue_entity.IssueLabel{}).Select("issue_id"))
	}
	if filter.UpdatedFrom > 0 {
		q = q.Where("updatetime >= ?", filter.UpdatedFrom)
	}
	if filter.UpdatedTo > 0 {
		q = q.Where("updatetime <= ?", filter.UpdatedTo)
	}
	if filter.CreatedFrom > 0 {
		q = q.Where("createtime >= ?", filter.CreatedFrom)
	}
	if filter.CreatedTo > 0 {
		q = q.Where("createtime <= ?", filter.CreatedTo)
	}
	if filter.DoneAfter > 0 {
		// 只裁剪已完成的卡片。历史行可能没记下关闭时间（closed_at = 0），退回
		// updatetime —— 否则保留窗口会把它们静默吞掉。
		q = q.Where("stage <> ? OR (CASE WHEN closed_at > 0 THEN closed_at ELSE updatetime END) >= ?",
			issue_entity.StageDone, filter.DoneAfter)
	}
	return q
}

// escapeLike 把用户输入里的 LIKE 通配符转义成字面量（配合 ESCAPE '\'）：搜一个
// `_` 不该把整个库都搜出来。
func escapeLike(s string) string {
	return likeEscaper.Replace(s)
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)

// issueNumber 从关键词里解析 `#编号`：`#179` 与 `179` 都解析成 179，其余返回 false。
func issueNumber(keyword string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimPrefix(keyword, "#"), 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// ReassignProject 见接口注释：WHERE 里只有 project_id，没有 status。
func (r *issueRepo) ReassignProject(ctx context.Context, fromProjectID, toProjectID int64) error {
	return db.Ctx(ctx).Model(&issue_entity.Issue{}).
		Where("project_id = ?", fromProjectID).
		Updates(map[string]any{
			"project_id": toProjectID,
			"updatetime": time.Now().UnixMilli(),
		}).Error
}

func (r *issueRepo) CountUnfinishedByProject(ctx context.Context) (map[int64]int64, error) {
	type agg struct {
		ProjectID int64
		Cnt       int64
	}
	var rows []agg
	if err := db.Ctx(ctx).Model(&issue_entity.Issue{}).
		Select("project_id, count(*) as cnt").
		Where("status = ?", consts.ACTIVE).
		Where("stage <> ?", issue_entity.StageDone).
		Group("project_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]int64, len(rows))
	for _, row := range rows {
		out[row.ProjectID] = row.Cnt
	}
	return out, nil
}

func (r *issueRepo) CountByState(ctx context.Context, projectID int64) (int64, int64, error) {
	type agg struct {
		State string
		Cnt   int64
	}
	q := db.Ctx(ctx).Model(&issue_entity.Issue{}).
		Select("state, count(*) as cnt").
		Where("status = ?", consts.ACTIVE)
	if projectID > 0 {
		q = q.Where("project_id = ?", projectID)
	}
	var rows []agg
	if err := q.Group("state").Scan(&rows).Error; err != nil {
		return 0, 0, err
	}
	var open, closed int64
	for _, row := range rows {
		switch row.State {
		case issue_entity.StateOpen:
			open = row.Cnt
		case issue_entity.StateClosed:
			closed = row.Cnt
		}
	}
	return open, closed, nil
}

func (r *issueRepo) StageCounts(ctx context.Context, filter ListFilter) (map[string]int64, error) {
	type agg struct {
		Stage string
		Cnt   int64
	}
	var rows []agg
	if err := r.scoped(ctx, filter).Select("stage, count(*) as cnt").
		Group("stage").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int64, 4)
	for _, row := range rows {
		out[row.Stage] = row.Cnt
	}
	return out, nil
}

func (r *issueRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Model(&issue_entity.Issue{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     consts.DELETE,
			"updatetime": time.Now().UnixMilli(),
		}).Error
}
