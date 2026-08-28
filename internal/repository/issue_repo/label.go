package issue_repo

import (
	"context"
	"errors"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/consts"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/issue_entity"
)

//go:generate mockgen -source label.go -destination mock_issue_repo/mock_label.go

// LabelRepo 标签目录仓储。
type LabelRepo interface {
	Create(ctx context.Context, l *issue_entity.Label) error
	// Update 只写标签自己的字段（改名 + 换色是同一次写入），不动任何任务。
	Update(ctx context.Context, l *issue_entity.Label) error
	// Delete 软删（labels.status 已有该语义）；把标签从任务上摘掉是
	// IssueLabelRepo.DeleteByLabel 的事，由 service 一并编排。
	Delete(ctx context.Context, id int64) error
	Find(ctx context.Context, id int64) (*issue_entity.Label, error)
	// FindByName 只看 active 行：软删掉的名字可以被重新用起来（唯一索引也是
	// WHERE status = 1 的部分索引）。
	FindByName(ctx context.Context, name string) (*issue_entity.Label, error)
	List(ctx context.Context) ([]*issue_entity.Label, error)
	ListByIDs(ctx context.Context, ids []int64) ([]*issue_entity.Label, error)
}

var defaultLabel LabelRepo

func Label() LabelRepo             { return defaultLabel }
func RegisterLabel(impl LabelRepo) { defaultLabel = impl }
func NewLabel() LabelRepo          { return &labelRepo{} }

type labelRepo struct{}

func (r *labelRepo) Create(ctx context.Context, l *issue_entity.Label) error {
	now := time.Now().UnixMilli()
	if l.Createtime == 0 {
		l.Createtime = now
	}
	l.Updatetime = now
	// 同步标识在行创建时就地生成，未登录期间也照常写入（R1/R12a）。
	l.EnsureSyncID()
	return db.Ctx(ctx).Create(l).Error
}

func (r *labelRepo) Update(ctx context.Context, l *issue_entity.Label) error {
	l.Updatetime = time.Now().UnixMilli()
	// 还没有标识的历史行在下一次落库时补齐（JIT），已有标识的行原样保留（R1）。
	l.EnsureSyncID()
	return db.Ctx(ctx).Model(&issue_entity.Label{}).
		Where("id = ? AND status = ?", l.ID, consts.ACTIVE).
		Updates(map[string]any{
			"name":       l.Name,
			"tone":       l.Tone,
			"sync_id":    l.SyncID,
			"updatetime": l.Updatetime,
		}).Error
}

func (r *labelRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Model(&issue_entity.Label{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     consts.DELETE,
			"updatetime": time.Now().UnixMilli(),
		}).Error
}

func (r *labelRepo) FindByName(ctx context.Context, name string) (*issue_entity.Label, error) {
	out := &issue_entity.Label{}
	err := db.Ctx(ctx).Where("name = ? AND status = ?", name, consts.ACTIVE).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *labelRepo) Find(ctx context.Context, id int64) (*issue_entity.Label, error) {
	out := &issue_entity.Label{}
	err := db.Ctx(ctx).Where("id = ? AND status = ?", id, consts.ACTIVE).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *labelRepo) List(ctx context.Context) ([]*issue_entity.Label, error) {
	var rows []*issue_entity.Label
	err := db.Ctx(ctx).Where("status = ?", consts.ACTIVE).
		Order("sort_order ASC, id ASC").Find(&rows).Error
	return rows, err
}

func (r *labelRepo) ListByIDs(ctx context.Context, ids []int64) ([]*issue_entity.Label, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []*issue_entity.Label
	err := db.Ctx(ctx).Where("id IN ? AND status = ?", ids, consts.ACTIVE).
		Order("sort_order ASC, id ASC").Find(&rows).Error
	return rows, err
}

// IssueLabelRepo issue ↔ label 关联仓储。
type IssueLabelRepo interface {
	SetLabels(ctx context.Context, issueID int64, labelIDs []int64) error
	ListByIssue(ctx context.Context, issueID int64) ([]int64, error)
	// ListRowsByIssue 返回一条任务当前的全部关联行（整行，带同步元数据）。摘掉一个
	// 标签是硬删，墓碑只能凭那一行**自己的**同步标识上行（R6）——只回 label_id 的
	// ListByIssue 拿不出标识，SetLabels 之前必须先用它看清现状。
	ListRowsByIssue(ctx context.Context, issueID int64) ([]*issue_entity.IssueLabel, error)
	ListByIssues(ctx context.Context, issueIDs []int64) (map[int64][]int64, error)
	// DeleteByLabel 把一个标签从全部任务上摘掉（软删标签时的爆炸半径落点）。
	DeleteByLabel(ctx context.Context, labelID int64) error
	// CountByLabel 「被 N 个任务使用」：只数还在的任务（issues.status = ACTIVE）。
	CountByLabel(ctx context.Context) (map[int64]int64, error)
	// UpsertFromSync 按同步标识落地一条下行来的关联行，沿用它自己的同步标识（R1）。
	// 同一对 (issue, label) 上已经有行时只把标识对上——联合主键上硬插会撞，而两端
	// 各自给同一件事挂标签本来就会带着两个不同的标识落在同一个自然键上。
	UpsertFromSync(ctx context.Context, row *issue_entity.IssueLabel) error
	// DeleteBySyncID 按同步标识删掉一条关联行（墓碑到达，R6）。关联表是联合主键，
	// 本地 ID 在另一台机器上指向完全不同的两行，指认不了它。
	DeleteBySyncID(ctx context.Context, syncID string) error
}

var defaultIssueLabel IssueLabelRepo

func IssueLabel() IssueLabelRepo             { return defaultIssueLabel }
func RegisterIssueLabel(impl IssueLabelRepo) { defaultIssueLabel = impl }
func NewIssueLabel() IssueLabelRepo          { return &issueLabelRepo{} }

type issueLabelRepo struct{}

// SetLabels 用一次事务把 issue 的标签关联对齐到 labelIDs：**只动真正变化的行**。
//
// 整批删掉再重建看着更省事，代价是每个幸存的关联行都会拿到一个新的同步标识——
// 标识终身不变（R1），换一次就等于在账号里丢下一个孤儿旧对象、再堆一个重复的新
// 对象。摘掉的那些行由 service 凭它们自己的标识落墓碑（R6），所以这里的删除范围
// 必须精确到「被摘掉的标签」。
func (r *issueLabelRepo) SetLabels(ctx context.Context, issueID int64, labelIDs []int64) error {
	labelIDs = uniqueInt64s(labelIDs)
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		var existing []issue_entity.IssueLabel
		if err := tx.Where("issue_id = ?", issueID).Find(&existing).Error; err != nil {
			return err
		}
		want := make(map[int64]struct{}, len(labelIDs))
		for _, id := range labelIDs {
			want[id] = struct{}{}
		}
		held := make(map[int64]struct{}, len(existing))
		removed := make([]int64, 0, len(existing))
		for _, row := range existing {
			held[row.LabelID] = struct{}{}
			if _, keep := want[row.LabelID]; !keep {
				removed = append(removed, row.LabelID)
			}
		}
		if len(removed) > 0 {
			if err := tx.Where("issue_id = ? AND label_id IN ?", issueID, removed).
				Delete(&issue_entity.IssueLabel{}).Error; err != nil {
				return err
			}
		}
		rows := make([]issue_entity.IssueLabel, 0, len(labelIDs))
		for _, id := range labelIDs {
			if _, already := held[id]; already {
				continue // 幸存行原样留着，连同它那个标识
			}
			row := issue_entity.IssueLabel{IssueID: issueID, LabelID: id}
			// 关联行自己也是同步对象，建行时就地生成标识（R1）。
			row.EnsureSyncID()
			rows = append(rows, row)
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Create(&rows).Error
	})
}

func (r *issueLabelRepo) ListByIssue(ctx context.Context, issueID int64) ([]int64, error) {
	var ids []int64
	err := db.Ctx(ctx).Model(&issue_entity.IssueLabel{}).
		Where("issue_id = ?", issueID).
		Order("label_id ASC").
		Pluck("label_id", &ids).Error
	return ids, err
}

func (r *issueLabelRepo) ListRowsByIssue(ctx context.Context, issueID int64) ([]*issue_entity.IssueLabel, error) {
	var rows []*issue_entity.IssueLabel
	err := db.Ctx(ctx).Where("issue_id = ?", issueID).
		Order("label_id ASC").Find(&rows).Error
	return rows, err
}

func (r *issueLabelRepo) ListByIssues(ctx context.Context, issueIDs []int64) (map[int64][]int64, error) {
	out := map[int64][]int64{}
	if len(issueIDs) == 0 {
		return out, nil
	}
	var rows []issue_entity.IssueLabel
	if err := db.Ctx(ctx).Where("issue_id IN ?", issueIDs).
		Order("issue_id ASC, label_id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.IssueID] = append(out[row.IssueID], row.LabelID)
	}
	return out, nil
}

func (r *issueLabelRepo) UpsertFromSync(ctx context.Context, row *issue_entity.IssueLabel) error {
	if row == nil || row.SyncID == "" {
		// 没有同步标识就不是一次下行落地：本地路径走 SetLabels。
		return nil
	}
	existing := &issue_entity.IssueLabel{}
	err := db.Ctx(ctx).Where("issue_id = ? AND label_id = ?", row.IssueID, row.LabelID).
		First(existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Ctx(ctx).Create(row).Error
	}
	// 自然键就是身份：本机那一行接管账号里胜出的同步标识，不为同一件事再插一行。
	return db.Ctx(ctx).Model(&issue_entity.IssueLabel{}).
		Where("issue_id = ? AND label_id = ?", row.IssueID, row.LabelID).
		Update("sync_id", row.SyncID).Error
}

func (r *issueLabelRepo) DeleteBySyncID(ctx context.Context, syncID string) error {
	if syncID == "" {
		return nil
	}
	return db.Ctx(ctx).Where("sync_id = ?", syncID).
		Delete(&issue_entity.IssueLabel{}).Error
}

func (r *issueLabelRepo) DeleteByLabel(ctx context.Context, labelID int64) error {
	return db.Ctx(ctx).Where("label_id = ?", labelID).
		Delete(&issue_entity.IssueLabel{}).Error
}

func (r *issueLabelRepo) CountByLabel(ctx context.Context) (map[int64]int64, error) {
	type agg struct {
		LabelID int64
		Cnt     int64
	}
	var rows []agg
	if err := db.Ctx(ctx).Model(&issue_entity.IssueLabel{}).
		Select("label_id, count(*) as cnt").
		Joins("JOIN issues ON issues.id = issue_labels.issue_id").
		Where("issues.status = ?", consts.ACTIVE).
		Group("label_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]int64, len(rows))
	for _, row := range rows {
		out[row.LabelID] = row.Cnt
	}
	return out, nil
}

func uniqueInt64s(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
