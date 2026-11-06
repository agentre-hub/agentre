package project_svc

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/issue_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_location_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncqueue_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/issue_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncqueue_repo"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// Merge 见 ProjectSvc 接口注释（R11a）。
//
// 流程：①挑赢家(keep)/输家(drop) ②keep 借 drop 的本机路径(若 keep 自己还没有)
// ③五类引用逐一改挂到 keep ④用删单独一行的那一段（deleteRows）软删 drop——其中的
// HasActiveChildren / CountActiveByProject 守卫在这里等于免费的二次校验：如果
// 上一步漏改了哪一类引用，它会拒绝，合并随之失败。（Delete 自己遇到子项目是级联删
// 整棵子树；这里不能跟着级联，漏改挂的子项目该让合并失败，而不是被一并删掉。）
//
// 全部读写跑在同一个事务里（spec 2026-09-11 要求 4）：任一步失败整体回滚，库里不留
// 半合并的状态。同步通知在事务里只收集、不发出，提交成功之后再用外层 ctx 按原次序
// 发出：NotifyLocalChange 用拿到的 ctx 写出站队列，再以 context.WithoutCancel(ctx)
// 起后台同步，事务的 ctx 会把一个已经提交的 tx 交给它；回滚之后，发出去的通知也收不回来。
func (s *projectSvc) Merge(ctx context.Context, req *MergeProjectsRequest) (*project_entity.Project, error) {
	if req == nil || !validMergePair(req.SourceID, req.TargetID) {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	var keep *project_entity.Project
	var changes []sync_svc.LocalChange
	if err := s.tx.RunInTx(ctx, func(txCtx context.Context) error {
		var err error
		keep, changes, err = s.mergeRows(txCtx, req)
		return err
	}); err != nil {
		return nil, err
	}
	for _, ch := range changes {
		sync_svc.Notify(ctx, ch)
	}
	// Merge 落库成功后 keep/drop 两行都变了（drop 软删、keep 借道路径/改挂引用）；
	// 五类资源里它触到的只有 project，其余 changes（issue、project_location…）不在
	// 这五类之列，不发。
	sync_svc.NotifyConfigChanged(syncwire.KindProject)
	return keep, nil
}

// mergeRows 是 Merge 在事务里的那一段，ctx 必须是事务的 ctx。返回保留下来的那一行，
// 以及提交成功后要按次序发出的同步通知。
func (s *projectSvc) mergeRows(
	ctx context.Context, req *MergeProjectsRequest,
) (*project_entity.Project, []sync_svc.LocalChange, error) {
	keep, drop, err := findMergePair(ctx, req.SourceID, req.TargetID)
	if err != nil {
		return nil, nil, err
	}

	// 保留本机项目的本机路径：keep 自己已经配了路径就不动，没配才借 drop 的。
	if keep.LocalPathMissing && !drop.LocalPathMissing {
		keep.Path = drop.Path
		keep.LocalPathMissing = false
	}
	// keep 今天挂在即将消失的 drop 底下（不论隔了几层）——让它顶到 drop 原来的位置上：
	// 改挂到 drop 自己的父级（drop 是顶层时这里落 0，keep 随之升为顶层）。
	//
	// 直接子项目不改挂，留下的是一个指向已消失项目的 parent_id；隔着几层不改挂，留下
	// 的是一个环——下面那一步把 drop 的子项目全部改挂到 keep 名下，其中就有 keep
	// 自己的祖先，而 keep 还挂在它底下。环上的项目从根上再也走不到，项目树里整段消失。
	underDrop, err := isUnderProject(ctx, keep, drop.ID)
	if err != nil {
		return nil, nil, err
	}
	if underDrop {
		keep.ParentID = drop.ParentID
	}
	if err := project_repo.Project().Update(ctx, keep); err != nil {
		return nil, nil, err
	}

	if err := s.sessions.ReassignProject(ctx, drop.ID, keep.ID); err != nil {
		return nil, nil, err
	}
	if err := reassignProjectAgents(ctx, drop.ID, keep.ID); err != nil {
		return nil, nil, err
	}
	if err := reassignChildProjects(ctx, drop.ID, keep.ID); err != nil {
		return nil, nil, err
	}
	// 改挂之前先把它们读出来：project_id 是任务同步载荷里的 project_sync_id
	// （sync_svc.issuePayload），整批改写之后不为每条任务各发一次上行，对端的卡
	// 就还挂在这个马上要消失的项目上。标识只在行上，整批 UPDATE 不回传它们。
	moved := issuesToReannounce(ctx, drop.ID)
	if err := issue_repo.Issue().ReassignProject(ctx, drop.ID, keep.ID); err != nil {
		return nil, nil, err
	}
	changes := make([]sync_svc.LocalChange, 0, len(moved)+1)
	for _, it := range moved {
		changes = append(changes, sync_svc.LocalChange{
			Kind: syncwire.KindIssue, LocalID: it.ID, Op: sync_svc.OpUpdate, Meta: it.SyncMeta,
		})
	}
	if err := s.reassignProjectLocations(ctx, keep, drop); err != nil {
		return nil, nil, err
	}

	deleted, err := s.deleteRows(ctx, drop.ID)
	if err != nil {
		return nil, nil, err
	}
	return keep, append(changes, deleted), nil
}

// MergePreview 见 ProjectSvc 接口注释（S4）。只读：不开事务、不发同步通知。
func (s *projectSvc) MergePreview(ctx context.Context, sourceID, targetID int64) (*MergePreview, error) {
	if !validMergePair(sourceID, targetID) {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	keep, drop, err := findMergePair(ctx, sourceID, targetID)
	if err != nil {
		return nil, err
	}

	var all []*project_entity.Project
	if keep.ParentID != 0 || drop.ParentID != 0 {
		if all, err = project_repo.Project().List(ctx); err != nil {
			return nil, err
		}
	}
	return &MergePreview{
		Keep: mergePreviewSide(keep, all),
		Drop: mergePreviewSide(drop, all),
	}, nil
}

// validMergePair 是 Merge 与 MergePreview 共用的入参口径：两个正 id，且不是同一个项目。
func validMergePair(sourceID, targetID int64) bool {
	return sourceID > 0 && targetID > 0 && sourceID != targetID
}

// findMergePair 读出两行并按 chooseMergeWinner 分出 keep / drop——Merge 与
// MergePreview 走的是这同一段，预览给出的结果因此不会与真的合并分叉。任一行不存在
// （或已软删）即 ProjectNotFound。
func findMergePair(ctx context.Context, sourceID, targetID int64) (keep, drop *project_entity.Project, err error) {
	a, err := project_repo.Project().Find(ctx, sourceID)
	if err != nil {
		return nil, nil, err
	}
	b, err := project_repo.Project().Find(ctx, targetID)
	if err != nil {
		return nil, nil, err
	}
	if a == nil || b == nil {
		return nil, nil, i18n.NewError(ctx, code.ProjectNotFound)
	}
	keep, drop = chooseMergeWinner(a, b)
	return keep, drop, nil
}

func mergePreviewSide(p *project_entity.Project, all []*project_entity.Project) MergePreviewSide {
	return MergePreviewSide{
		ID:         p.ID,
		Name:       p.Name,
		ParentPath: ancestorNames(all, p.ParentID),
		Claimed:    p.IsClaimed(),
	}
}

// ancestorNames 从 parentID 顺着父链往上走，返回根 → parentID 的名称链（顶层是空
// 切片而不是 nil，经 Wails 到前端是 [] 而不是 null）。父链断在
// 找不到的行（软删 / 悬空引用）就到此为止；遇到环也停，不会走不完。
func ancestorNames(all []*project_entity.Project, parentID int64) []string {
	index := make(map[int64]*project_entity.Project, len(all))
	for _, p := range all {
		index[p.ID] = p
	}
	names := []string{}
	seen := make(map[int64]struct{}, len(all))
	for cur := parentID; cur > 0; {
		if _, visited := seen[cur]; visited {
			break
		}
		seen[cur] = struct{}{}
		p, ok := index[cur]
		if !ok {
			break
		}
		names = append([]string{p.Name}, names...)
		cur = p.ParentID
	}
	return names
}

// isUnderProject 判 p 是不是挂在 ancestorID 的子树里（任意深度）。顶层项目与直接
// 子项目不必读库就有答案，只有隔着几层才要把整棵树读出来顺着父链往上爬。
func isUnderProject(ctx context.Context, p *project_entity.Project, ancestorID int64) (bool, error) {
	switch p.ParentID {
	case 0:
		return false, nil
	case ancestorID:
		return true, nil
	}
	all, err := project_repo.Project().List(ctx)
	if err != nil {
		return false, err
	}
	return hasProjectCycle(all, p.ParentID, ancestorID), nil
}

// issuesToReannounce 列出还挂在 projectID 名下的存活任务（整行，带同步标识）。
//
// 同步未装配时一次库都不查（与 project_svc.memberSyncMeta 同一口径）。软删的任务
// 不在其中，也不需要：它们各自的墓碑早已上行，account 侧从此不再有活着的对象，
// ReassignProject 那一下只是不让本机留下指向已消失项目的悬空引用。
//
// 读失败不回传：这一次读取只为同步层服务，同步层的任何失败都不该否决用户的合并
// （R8，与 project_svc.memberSyncMeta / issue_svc.labelLinksBefore 同一口径）。
// 最坏的结果是这一轮少发几条上行，任务下次被编辑时再对上。
func issuesToReannounce(ctx context.Context, projectID int64) []*issue_entity.Issue {
	if !sync_svc.Active() {
		return nil
	}
	rows, err := issue_repo.Issue().List(ctx, issue_repo.ListFilter{ProjectIDs: []int64{projectID}})
	if err != nil {
		logger.Ctx(ctx).Warn("project_svc.issuesToReannounce: list issues failed",
			zap.Int64("projectId", projectID), zap.Error(err))
		return nil
	}
	return rows
}

// chooseMergeWinner 决定合并后保留哪一行的身份（R11a）：账号侧已认领的一方优先；
// 两边都认领或都未认领时，沿用先创建的那个的（Createtime 更早）。
func chooseMergeWinner(a, b *project_entity.Project) (keep, drop *project_entity.Project) {
	aClaimed := a.IsClaimed()
	bClaimed := b.IsClaimed()
	if aClaimed != bClaimed {
		if aClaimed {
			return a, b
		}
		return b, a
	}
	if a.Createtime <= b.Createtime {
		return a, b
	}
	return b, a
}

// reassignProjectAgents 把 project_agents 从 dropID 改挂到 keepID，去重：一个
// agent 不能同时是同一个项目的两条成员记录，drop 那条在 keep 已有时直接丢弃。
func reassignProjectAgents(ctx context.Context, dropID, keepID int64) error {
	keepMembers, err := project_repo.ProjectAgent().ListByProject(ctx, keepID)
	if err != nil {
		return err
	}
	keepAgentIDs := make(map[int64]struct{}, len(keepMembers))
	for _, m := range keepMembers {
		keepAgentIDs[m.AgentID] = struct{}{}
	}
	dropMembers, err := project_repo.ProjectAgent().ListByProject(ctx, dropID)
	if err != nil {
		return err
	}
	for _, m := range dropMembers {
		if _, already := keepAgentIDs[m.AgentID]; !already {
			if err := project_repo.ProjectAgent().Add(ctx, keepID, m.AgentID); err != nil {
				return err
			}
		}
		if err := project_repo.ProjectAgent().Remove(ctx, dropID, m.AgentID); err != nil {
			return err
		}
	}
	return nil
}

// reassignChildProjects 把子项目的 projects.parent_id 从 dropID 改挂到 keepID。
func reassignChildProjects(ctx context.Context, dropID, keepID int64) error {
	children, err := project_repo.Project().ListByParent(ctx, dropID)
	if err != nil {
		return err
	}
	for _, c := range children {
		c.ParentID = keepID
		if err := project_repo.Project().Update(ctx, c); err != nil {
			return err
		}
	}
	// 上面那一轮只走得到**活跃**的子项目（它们要各自发一次同步通知）；软删的子项目
	// ListByParent 看不见，parent_id 却照样指着 dropID。R11a 不允许合并后留下任何
	// 指向已消失项目的引用，因此再扫一次，不带 status 判据。
	return project_repo.Project().ReassignParent(ctx, dropID, keepID)
}

// reassignProjectLocations 把 project_locations 从 drop 改挂到 keep（R4b 的自然键
// 处理）：两边对同一台 agentred（按指纹）各有一行时不能都改挂，否则撞
// (project, fingerprint) 自然键 —— keep 侧那行胜出（它就是合并保留下来的那个身份），
// **落败的那行按 R5 记一条「被覆盖」再软删**，用户在设置的「没能同步的改动」里还能
// 把那条路径找回来（R11a 末段点名了这一条）。
//
// 收尾整批再改挂一次：软删的路径记录在 ListByProject 里看不见，但它们的 project_id
// 照样指着即将消失的 drop —— R11a 要求一处不落。自然键的唯一索引只管 status=1 的行，
// 因此这一批（含刚刚落败被软删的那行）不会撞键。
func (s *projectSvc) reassignProjectLocations(ctx context.Context, keep, drop *project_entity.Project) error {
	dropLocs, err := project_location_repo.ProjectLocation().ListByProject(ctx, drop.ID)
	if err != nil {
		return err
	}
	for _, loc := range dropLocs {
		if loc.DeviceFingerprint != "" {
			existing, ferr := project_location_repo.ProjectLocation().FindByProjectAndFingerprint(ctx, keep.ID, loc.DeviceFingerprint)
			if ferr != nil && !errors.Is(ferr, gorm.ErrRecordNotFound) {
				return ferr
			}
			if existing != nil {
				if err := s.recordLostLocation(ctx, keep, loc); err != nil {
					return err
				}
				if err := project_location_repo.ProjectLocation().Delete(ctx, loc.ID); err != nil {
					return err
				}
				continue
			}
		}
		loc.ProjectID = keep.ID
		if err := project_location_repo.ProjectLocation().Update(ctx, loc); err != nil {
			return err
		}
	}
	return project_location_repo.ProjectLocation().ReassignProject(ctx, drop.ID, keep.ID)
}

// recordLostLocation 把 R4b 里落败的那一行路径记录写进 R5 的「没能同步的改动」
// 列表：与同步引擎写的是同一张表、同一个仓储（syncqueue_repo.LostChange），因此
// 用户看到的是同一份列表、同一套恢复动作（决策 12）。
//
// 未认领的行（SyncAccountID = 0，登录前建的或换过账号）不写：R12 / R13a 下同步侧
// 一行都不该产生，那份内容本来也不会出现在任何账号的列表里。
//
// ProjectSyncID 落 keep 的标识而不是 drop 的：drop 马上就没了，恢复只可能落回
// 保留下来的那个项目（R5a）。
func (s *projectSvc) recordLostLocation(
	ctx context.Context, keep *project_entity.Project, loser *project_location_entity.ProjectLocation,
) error {
	if loser.SyncAccountID == 0 {
		return nil
	}
	// 正文形状与同步引擎给 project_location 的一致（只有 path）；自然键的另两项
	// 走 LostChange 的顶层字段，不进正文。
	payload, err := json.Marshal(struct {
		Path string `json:"path"`
	}{Path: loser.Path})
	if err != nil {
		return err
	}
	now := s.now()
	return syncqueue_repo.LostChange().Create(ctx, &syncqueue_entity.LostChange{
		SyncAccountID:       loser.SyncAccountID,
		EntityType:          syncwire.KindProjectLocation,
		EntitySyncID:        loser.SyncID,
		BaseVersion:         loser.SyncVersion,
		Reason:              syncqueue_entity.ReasonOverwritten,
		PayloadJSON:         string(payload),
		ScopeSyncID:         keep.SyncID,
		AgentredFingerprint: loser.DeviceFingerprint,
		OccurredAt:          now,
		Createtime:          now,
	})
}
