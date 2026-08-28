// Package issue_svc 提供 Issue 模块的应用服务。
package issue_svc

import (
	"context"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/issue_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/issue_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
)

const (
	positionStep = 65536.0
	millisPerDay = 24 * 60 * 60 * 1000
)

// IssueSvc Issue 模块应用服务。
type IssueSvc interface {
	Create(ctx context.Context, req *CreateIssueRequest) (*IssueDetail, error)
	Update(ctx context.Context, req *UpdateIssueRequest) (*IssueDetail, error)
	Delete(ctx context.Context, id int64) error
	Get(ctx context.Context, id int64) (*IssueDetail, error)
	List(ctx context.Context, req *ListIssuesRequest) (*ListIssuesResponse, error)
	Move(ctx context.Context, req *MoveIssueRequest) (*IssueDetail, error)
	ListLabels(ctx context.Context) ([]*LabelDetail, error)
	CreateLabel(ctx context.Context, req *LabelRequest) (*LabelDetail, error)
	UpdateLabel(ctx context.Context, req *LabelRequest) (*LabelDetail, error)
	DeleteLabel(ctx context.Context, id int64) error
}

type issueSvc struct {
	now func() int64
}

var defaultIssue IssueSvc = &issueSvc{now: func() int64 { return time.Now().UnixMilli() }}

// Default 取默认服务单例。
func Default() IssueSvc { return defaultIssue }

// SetDefault 注入服务实现（测试 / bootstrap 装配用）。
func SetDefault(svc IssueSvc) { defaultIssue = svc }

// New 构造默认实现。
func New() IssueSvc {
	return &issueSvc{now: func() int64 { return time.Now().UnixMilli() }}
}

func (s *issueSvc) Create(ctx context.Context, req *CreateIssueRequest) (*IssueDetail, error) {
	now := s.now()
	labelIDs := uniqueInt64s(req.LabelIDs)
	stage := req.Stage
	if stage == "" {
		stage = issue_entity.StageTodo
	}
	issue := &issue_entity.Issue{
		ProjectID:   req.ProjectID,
		Title:       strings.TrimSpace(req.Title),
		Body:        req.Body,
		State:       issue_entity.StateOpen,
		Stage:       stage,
		AgentStatus: issue_entity.AgentStatusIdle,
		Source:      issue_entity.SourceManual,
		Status:      consts.ACTIVE,
		Createtime:  now,
		Updatetime:  now,
	}
	applyExecution(issue, req.Execution)
	if stage == issue_entity.StageDone {
		issue.SetStage(issue_entity.StageDone, now)
	}
	if err := issue.Check(ctx); err != nil {
		return nil, err
	}
	pos, err := s.appendPosition(ctx, stage)
	if err != nil {
		return nil, err
	}
	issue.Position = pos
	labels, err := s.resolveLabels(ctx, labelIDs)
	if err != nil {
		return nil, err
	}
	if err := issue_repo.Issue().Create(ctx, issue); err != nil {
		return nil, err
	}
	// TODO(v1): Create 与 SetLabels 目前非原子——SetLabels 失败会留下无标签的 issue 行。
	// 维持非事务以保证 service 可纯 mock 单测（项目规约：service 单测不接 DB）；
	// 若后续标签写入可靠性变重要，按 agent_svc.Delete 的 db.Ctx(ctx).Transaction 模式包裹。
	if err := issue_repo.IssueLabel().SetLabels(ctx, issue.ID, labelIDs); err != nil {
		logger.Ctx(ctx).Warn("issue_svc.Create: set labels failed",
			zap.Int64("issueId", issue.ID), zap.Error(err))
		return nil, err
	}
	// 落库成功之后交给同步层（R3/R8）：入队 + 当场上行，失败不回传。排在 SetLabels
	// 之后而不是紧接 Create —— 那一步刚生成的 issue_labels 行也是独立的同步对象，
	// 它们尚未归属账号，由本次触发的这一轮 claimUnowned 一并收走（sync_svc.SyncOnce
	// 里认领排在推送之前），标签因此和任务同一轮到达对端。
	//
	// session_id / agent_status / source 是本机运行态，压根不进载荷（见
	// sync_svc.issuePayload）；它们随这一行落库，但改变它们的不是这条路径。
	sync_svc.NotifyCreate(ctx, syncwire.KindIssue, issue.ID, issue.SyncMeta)
	return &IssueDetail{Issue: issue, Labels: labels}, nil
}

func (s *issueSvc) Update(ctx context.Context, req *UpdateIssueRequest) (*IssueDetail, error) {
	labelIDs := uniqueInt64s(req.LabelIDs)
	issue, err := issue_repo.Issue().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if issue == nil {
		return nil, i18n.NewError(ctx, code.IssueNotFound)
	}
	issue.ProjectID = req.ProjectID
	issue.Title = strings.TrimSpace(req.Title)
	issue.Body = req.Body
	applyExecution(issue, req.Execution)
	// 编辑态里阶段仍可改；走 SetStage 而不是直接赋值，state 与 closed_at 才跟着走。
	if req.Stage != "" && req.Stage != issue.Stage {
		if !issue_entity.IsKnownStage(req.Stage) {
			return nil, i18n.NewError(ctx, code.IssueInvalidState)
		}
		issue.SetStage(req.Stage, s.now())
	}
	if err := issue.Check(ctx); err != nil {
		return nil, err
	}
	labels, err := s.resolveLabels(ctx, labelIDs)
	if err != nil {
		return nil, err
	}
	if err := issue_repo.Issue().Update(ctx, issue); err != nil {
		return nil, err
	}
	// 关联行是硬删：被摘掉的那几行的同步标识只在行上，SetLabels 一动就没了，
	// 必须在它之前读一次（R6 的墓碑靠这些标识上行）。
	linksBefore := labelLinksBefore(ctx, issue.ID)
	// TODO(v1): Update 与 SetLabels 目前非原子——SetLabels 失败会留下标签未更新的 issue 行。
	// 维持非事务以保证 service 可纯 mock 单测（项目规约：service 单测不接 DB）；
	// 若后续标签写入可靠性变重要，按 agent_svc.Delete 的 db.Ctx(ctx).Transaction 模式包裹。
	if err := issue_repo.IssueLabel().SetLabels(ctx, issue.ID, labelIDs); err != nil {
		logger.Ctx(ctx).Warn("issue_svc.Update: set labels failed",
			zap.Int64("issueId", issue.ID), zap.Error(err))
		return nil, err
	}
	notifyDetachedLabels(ctx, linksBefore, labelIDs)
	// 基版本取 Find 那一刻行上的版本（R4a）：它就是「本端编辑时见到的那一版」，
	// 冲突判定靠它。
	sync_svc.NotifyUpdate(ctx, syncwire.KindIssue, issue.ID, issue.SyncMeta)
	return &IssueDetail{Issue: issue, Labels: labels}, nil
}

// labelLinksBefore 读出这条任务此刻挂着的全部关联行（整行，带同步标识）。
//
// 同步未装配时一次库都不查——这次读取只为墓碑服务（与 project_svc.memberSyncMeta
// 同一口径）。读失败也不让用户的编辑失败（R8）：本地写入本身还没发生，同步层没有
// 资格否决它，最坏的结果是这一轮少发几条墓碑，下一次编辑再对上。
func labelLinksBefore(ctx context.Context, issueID int64) []*issue_entity.IssueLabel {
	if !sync_svc.Active() {
		return nil
	}
	rows, err := issue_repo.IssueLabel().ListRowsByIssue(ctx, issueID)
	if err != nil {
		logger.Ctx(ctx).Warn("issue_svc.labelLinksBefore: list issue labels failed",
			zap.Int64("issueId", issueID), zap.Error(err))
		return nil
	}
	return rows
}

// notifyDetachedLabels 给被摘掉的每一条关联行落一条墓碑（R6）。留下来的那些一条
// 改动都不发：它们连一次写入都没有过，标识也就没变（R1）。
func notifyDetachedLabels(ctx context.Context, before []*issue_entity.IssueLabel, kept []int64) {
	if len(before) == 0 {
		return
	}
	want := make(map[int64]struct{}, len(kept))
	for _, id := range kept {
		want[id] = struct{}{}
	}
	for _, row := range before {
		if _, keep := want[row.LabelID]; keep {
			continue
		}
		// 关联表是联合主键，没有本地自增 ID 可交（与 project_agent 的成员关系同形）。
		sync_svc.NotifyDelete(ctx, syncwire.KindIssueLabel, 0, row.SyncMeta)
	}
}

func (s *issueSvc) Delete(ctx context.Context, id int64) error {
	issue, err := issue_repo.Issue().Find(ctx, id)
	if err != nil {
		return err
	}
	if issue == nil {
		return i18n.NewError(ctx, code.IssueNotFound)
	}
	if err := issue_repo.Issue().Delete(ctx, id); err != nil {
		return err
	}
	// 删除靠墓碑到达各端（R6）：不入队，这台机器上删掉的卡在别处永远留着。
	sync_svc.NotifyDelete(ctx, syncwire.KindIssue, id, issue.SyncMeta)
	return nil
}

func (s *issueSvc) Get(ctx context.Context, id int64) (*IssueDetail, error) {
	issue, err := issue_repo.Issue().Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if issue == nil {
		return nil, i18n.NewError(ctx, code.IssueNotFound)
	}
	return s.hydrate(ctx, issue)
}

func (s *issueSvc) List(ctx context.Context, req *ListIssuesRequest) (*ListIssuesResponse, error) {
	scopeIDs, tree, err := s.resolveScope(ctx, req)
	if err != nil {
		return nil, err
	}
	// scopeFilter 只有项目范围；filter 在它之上再叠其余五个条件。列头的「命中 / 全部」
	// 就是这两把尺子各量一次。
	scopeFilter := issue_repo.ListFilter{ProjectIDs: scopeIDs}
	filter := s.applyConditions(scopeFilter, req)
	listFilter := filter
	listFilter.Sort = req.Sort

	issues, err := issue_repo.Issue().List(ctx, listFilter)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(issues))
	for _, it := range issues {
		ids = append(ids, it.ID)
	}
	labelMap, err := issue_repo.IssueLabel().ListByIssues(ctx, ids)
	if err != nil {
		return nil, err
	}
	allLabels, err := issue_repo.Label().List(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*issue_entity.Label{}
	for _, l := range allLabels {
		byID[l.ID] = l
	}
	details := make([]*IssueDetail, 0, len(issues))
	for _, it := range issues {
		labels := make([]*issue_entity.Label, 0)
		for _, lid := range labelMap[it.ID] {
			if l := byID[lid]; l != nil {
				labels = append(labels, l)
			}
		}
		details = append(details, &IssueDetail{Issue: it, Labels: labels})
	}
	stageCounts, err := issue_repo.Issue().StageCounts(ctx, filter)
	if err != nil {
		return nil, err
	}
	stageTotals, err := issue_repo.Issue().StageCounts(ctx, scopeFilter)
	if err != nil {
		return nil, err
	}
	perProject, err := issue_repo.Issue().CountUnfinishedByProject(ctx)
	if err != nil {
		return nil, err
	}
	return &ListIssuesResponse{
		Issues:        details,
		StageCounts:   stageCounts,
		StageTotals:   stageTotals,
		ProjectCounts: rollUpProjectCounts(tree, perProject),
	}, nil
}

// applyConditions 把除「项目」之外的五个筛选条件译到仓储过滤条件上。
func (s *issueSvc) applyConditions(filter issue_repo.ListFilter, req *ListIssuesRequest) issue_repo.ListFilter {
	filter.Keyword = strings.TrimSpace(req.Keyword)
	filter.LabelIDs = uniqueInt64s(req.LabelIDs)
	filter.LabelMatchAll = req.LabelMatchAll
	filter.NoLabel = req.NoLabel
	filter.UpdatedFrom = req.UpdatedFrom
	filter.UpdatedTo = req.UpdatedTo
	filter.CreatedFrom = req.CreatedFrom
	filter.CreatedTo = req.CreatedTo
	if req.DoneWithinDays > 0 {
		filter.DoneAfter = s.now() - int64(req.DoneWithinDays)*millisPerDay
	}
	return filter
}

// resolveScope 把「项目范围」这一档展开成 id 集合，并把项目树一并带出来（选择器的
// 子树计数要用同一份）。全部项目 → 空集合（不加条件）；未归属 → [0]。
func (s *issueSvc) resolveScope(ctx context.Context, req *ListIssuesRequest) ([]int64, []*project_entity.Project, error) {
	tree, err := project_repo.Project().List(ctx)
	if err != nil {
		return nil, nil, err
	}
	switch req.Scope {
	case ScopeUnassigned:
		return []int64{0}, tree, nil
	case ScopeProject:
		return collectProjectSubtree(tree, req.ProjectID), tree, nil
	default:
		return nil, tree, nil
	}
}

// collectProjectSubtree 收集 rootID 自身 + 全部递归后代（深度优先，同级按仓储返回的
// 顺序）。rootID 不在树里时只返回它自己 —— 范围仍然是一个确定的集合，不会静默变成
// 「全部项目」。
func collectProjectSubtree(all []*project_entity.Project, rootID int64) []int64 {
	children := map[int64][]int64{}
	for _, p := range all {
		children[p.ParentID] = append(children[p.ParentID], p.ID)
	}
	out := make([]int64, 0, len(all))
	seen := map[int64]struct{}{}
	var walk func(id int64)
	walk = func(id int64) {
		if _, ok := seen[id]; ok {
			return // 数据异常成环时不至于转不出来
		}
		seen[id] = struct{}{}
		out = append(out, id)
		for _, child := range children[id] {
			walk(child)
		}
	}
	walk(rootID)
	return out
}

// rollUpProjectCounts 把「每个项目自己的未完成任务数」按项目树汇总成「该项目及其
// 子树」的数；键 0（未归属）自成一档，不挂在任何项目下。
func rollUpProjectCounts(all []*project_entity.Project, own map[int64]int64) map[int64]int64 {
	out := make(map[int64]int64, len(all)+1)
	if unassigned, ok := own[0]; ok {
		out[0] = unassigned
	}
	children := map[int64][]int64{}
	for _, p := range all {
		children[p.ParentID] = append(children[p.ParentID], p.ID)
	}
	visiting := map[int64]struct{}{}
	var total func(id int64) int64
	total = func(id int64) int64 {
		if v, ok := out[id]; ok {
			return v
		}
		if _, ok := visiting[id]; ok {
			return 0 // 数据异常成环时不至于转不出来
		}
		visiting[id] = struct{}{}
		sum := own[id]
		for _, child := range children[id] {
			sum += total(child)
		}
		delete(visiting, id)
		out[id] = sum
		return sum
	}
	for _, p := range all {
		total(p.ID)
	}
	return out
}

// applyExecution 把执行归属（Agent / 机器 / 供应商 / 模型）整组落到实体上。四个字段
// 一起来一起走，别在两处各写一半。
func applyExecution(issue *issue_entity.Issue, exec ExecutionAssignment) {
	issue.AssigneeAgentID = exec.AssigneeAgentID
	issue.AgentBackendID = exec.AgentBackendID
	issue.LLMProviderKey = exec.LLMProviderKey
	issue.LLMModelKey = exec.LLMModelKey
}

func (s *issueSvc) ListLabels(ctx context.Context) ([]*LabelDetail, error) {
	labels, err := issue_repo.Label().List(ctx)
	if err != nil {
		return nil, err
	}
	return s.withUsage(ctx, labels)
}

func (s *issueSvc) CreateLabel(ctx context.Context, req *LabelRequest) (*LabelDetail, error) {
	label := &issue_entity.Label{
		Name:   strings.TrimSpace(req.Name),
		Tone:   req.Tone,
		Status: consts.ACTIVE,
	}
	if err := label.Check(ctx); err != nil {
		return nil, err
	}
	if err := s.assertNameFree(ctx, label.Name, 0); err != nil {
		return nil, err
	}
	if err := issue_repo.Label().Create(ctx, label); err != nil {
		return nil, err
	}
	logger.Ctx(ctx).Info("issue_svc.CreateLabel: created",
		zap.Int64("labelId", label.ID), zap.String("tone", label.Tone))
	// 标签目录是账号级的：一台机器上建的标签在每台机器上都要看得见。
	sync_svc.NotifyCreate(ctx, syncwire.KindLabel, label.ID, label.SyncMeta)
	return s.oneWithUsage(ctx, label)
}

func (s *issueSvc) UpdateLabel(ctx context.Context, req *LabelRequest) (*LabelDetail, error) {
	label, err := issue_repo.Label().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if label == nil {
		return nil, i18n.NewError(ctx, code.IssueLabelNotFound)
	}
	label.Name = strings.TrimSpace(req.Name)
	label.Tone = req.Tone
	if err := label.Check(ctx); err != nil {
		return nil, err
	}
	if err := s.assertNameFree(ctx, label.Name, label.ID); err != nil {
		return nil, err
	}
	if err := issue_repo.Label().Update(ctx, label); err != nil {
		return nil, err
	}
	// 改名与换色是同一次写入，两个字段都在载荷里。
	sync_svc.NotifyUpdate(ctx, syncwire.KindLabel, label.ID, label.SyncMeta)
	return s.oneWithUsage(ctx, label)
}

// DeleteLabel 软删标签：先把它从全部任务上摘掉（这正是删除前要说清的爆炸半径），
// 再把目录行置为已删除。顺序反了会留下指向一个已消失标签的关联行。
func (s *issueSvc) DeleteLabel(ctx context.Context, id int64) error {
	label, err := issue_repo.Label().Find(ctx, id)
	if err != nil {
		return err
	}
	if label == nil {
		return i18n.NewError(ctx, code.IssueLabelNotFound)
	}
	if err := issue_repo.IssueLabel().DeleteByLabel(ctx, id); err != nil {
		return err
	}
	logger.Ctx(ctx).Info("issue_svc.DeleteLabel: detached from issues", zap.Int64("labelId", id))
	if err := issue_repo.Label().Delete(ctx, id); err != nil {
		return err
	}
	// 只为标签目录行落墓碑。刚被 DeleteByLabel 摘掉的那些关联行**没有**在这里入队：
	// 它们的同步标识不经过本层（SetLabels / DeleteByLabel 都只回一个 error），
	// 拿不到就编不出墓碑。对端不会因此显示一个已删标签——它自己的
	// sync_svc.labelAdapter.remove 收到这条墓碑时同样会把标签从全部任务上摘掉。
	sync_svc.NotifyDelete(ctx, syncwire.KindLabel, id, label.SyncMeta)
	return nil
}

// labelNameTaken 目录里已经有一个同名标签（labels 上的 uniq_labels_name_active 也是
// 这么定的，这里只是把它在触达数据库之前说清楚——否则用户看到的是驱动抛出的那句
// 未翻译的唯一约束报错）。
func labelNameTaken(ctx context.Context) error {
	return i18n.NewError(ctx, code.IssueLabelNameDuplicated)
}

// assertNameFree 目录里同名只能有一个（唯一索引也是这么定的）；改回自己原来的名字
// 不算重名。只看 active 行，软删掉的名字可以被重新用起来。
func (s *issueSvc) assertNameFree(ctx context.Context, name string, selfID int64) error {
	existing, err := issue_repo.Label().FindByName(ctx, name)
	if err != nil {
		return err
	}
	if existing != nil && existing.ID != selfID {
		return labelNameTaken(ctx)
	}
	return nil
}

func (s *issueSvc) oneWithUsage(ctx context.Context, label *issue_entity.Label) (*LabelDetail, error) {
	details, err := s.withUsage(ctx, []*issue_entity.Label{label})
	if err != nil {
		return nil, err
	}
	return details[0], nil
}

// withUsage 给每个标签补上「被 N 个任务使用」。一次分组查询喂全部标签，别按行去数。
func (s *issueSvc) withUsage(ctx context.Context, labels []*issue_entity.Label) ([]*LabelDetail, error) {
	usage, err := issue_repo.IssueLabel().CountByLabel(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*LabelDetail, 0, len(labels))
	for _, l := range labels {
		out = append(out, &LabelDetail{Label: l, UsageCount: usage[l.ID]})
	}
	return out, nil
}

func (s *issueSvc) resolveLabels(ctx context.Context, ids []int64) ([]*issue_entity.Label, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	labels, err := issue_repo.Label().ListByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	if len(labels) != len(ids) {
		return nil, i18n.NewError(ctx, code.IssueLabelNotFound)
	}
	return labels, nil
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

func (s *issueSvc) hydrate(ctx context.Context, issue *issue_entity.Issue) (*IssueDetail, error) {
	ids, err := issue_repo.IssueLabel().ListByIssue(ctx, issue.ID)
	if err != nil {
		return nil, err
	}
	labels, err := issue_repo.Label().ListByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return &IssueDetail{Issue: issue, Labels: labels}, nil
}

// appendPosition 返回目标 stage 末位之后的 position。
func (s *issueSvc) appendPosition(ctx context.Context, stage string) (float64, error) {
	rows, err := issue_repo.Issue().List(ctx, issue_repo.ListFilter{Stage: stage, Sort: "position"})
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return positionStep, nil
	}
	return rows[len(rows)-1].Position + positionStep, nil
}

// Move 改 stage + 计算列内 position（AfterID 之后 / 顶部）。
func (s *issueSvc) Move(ctx context.Context, req *MoveIssueRequest) (*IssueDetail, error) {
	if !issue_entity.IsKnownStage(req.Stage) || req.Stage == "" {
		return nil, i18n.NewError(ctx, code.IssueInvalidState)
	}
	issue, err := issue_repo.Issue().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if issue == nil {
		return nil, i18n.NewError(ctx, code.IssueNotFound)
	}
	siblings, err := issue_repo.Issue().List(ctx, issue_repo.ListFilter{Stage: req.Stage, Sort: "position"})
	if err != nil {
		return nil, err
	}
	pos := computePosition(siblings, req.ID, req.AfterID)
	issue.SetStage(req.Stage, s.now())
	issue.Position = pos
	logger.Ctx(ctx).Info("issue_svc.Move: reposition",
		zap.Int64("issueId", req.ID), zap.String("stage", req.Stage), zap.Float64("position", pos))
	if err := issue_repo.Issue().Update(ctx, issue); err != nil {
		return nil, err
	}
	// 拖一张卡改的是 stage 与 position，两个都在载荷里。
	sync_svc.NotifyUpdate(ctx, syncwire.KindIssue, issue.ID, issue.SyncMeta)
	return s.hydrate(ctx, issue)
}

// computePosition 在 siblings（按 position 升序，可能含自身）中，
// 把卡片放到 afterID 之后。afterID=0 → 顶部。落点相邻两卡取中点；顶/底外扩 step。
func computePosition(siblings []*issue_entity.Issue, movingID, afterID int64) float64 {
	// 过滤掉自身，得到目标列的稳定序列。
	seq := make([]*issue_entity.Issue, 0, len(siblings))
	for _, it := range siblings {
		if it.ID != movingID {
			seq = append(seq, it)
		}
	}
	if len(seq) == 0 {
		return positionStep
	}
	if afterID == 0 {
		return seq[0].Position - positionStep
	}
	for idx, it := range seq {
		if it.ID != afterID {
			continue
		}
		if idx == len(seq)-1 {
			return it.Position + positionStep
		}
		return (it.Position + seq[idx+1].Position) / 2
	}
	// afterID 不在目标列（异常）→ 落底。
	return seq[len(seq)-1].Position + positionStep
}
