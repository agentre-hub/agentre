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
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/issue_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncqueue_repo"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
)

// Merge 见 ProjectSvc 接口注释（R11a）。
//
// 流程：①挑赢家(keep)/输家(drop) ②keep 借 drop 的本机路径(若 keep 自己还没有)
// ③五类引用逐一改挂到 keep ④复用既有 Delete() 软删 drop——Delete 内部的
// HasActiveChildren / CountActiveByProject 守卫在这里等于免费的二次校验：如果
// 上一步漏改了哪一类引用，Delete 会拒绝，合并失败但不留下半改的烂摊子。
func (s *projectSvc) Merge(ctx context.Context, req *MergeProjectsRequest) (*project_entity.Project, error) {
	if req == nil || req.SourceID <= 0 || req.TargetID <= 0 || req.SourceID == req.TargetID {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	a, err := project_repo.Project().Find(ctx, req.SourceID)
	if err != nil {
		return nil, err
	}
	b, err := project_repo.Project().Find(ctx, req.TargetID)
	if err != nil {
		return nil, err
	}
	if a == nil || b == nil {
		return nil, i18n.NewError(ctx, code.ProjectNotFound)
	}

	keep, drop := chooseMergeWinner(a, b)

	// 保留本机项目的本机路径：keep 自己已经配了路径就不动，没配才借 drop 的。
	if keep.LocalPathMissing && !drop.LocalPathMissing {
		keep.Path = drop.Path
		keep.LocalPathMissing = false
	}
	// keep 今天挂在即将消失的 drop 底下——不能留一个指向已消失项目的 parent_id，
	// 改挂到 drop 自己的父级（drop 是顶层时这里落 0，keep 随之升为顶层）。
	if keep.ParentID == drop.ID {
		keep.ParentID = drop.ParentID
	}
	if err := project_repo.Project().Update(ctx, keep); err != nil {
		return nil, err
	}

	if err := s.sessions.ReassignProject(ctx, drop.ID, keep.ID); err != nil {
		return nil, err
	}
	if err := reassignProjectAgents(ctx, drop.ID, keep.ID); err != nil {
		return nil, err
	}
	if err := reassignChildProjects(ctx, drop.ID, keep.ID); err != nil {
		return nil, err
	}
	// 改挂之前先把它们读出来：project_id 是任务同步载荷里的 project_sync_id
	// （sync_svc.issuePayload），整批改写之后不为每条任务各发一次上行，对端的卡
	// 就还挂在这个马上要消失的项目上。标识只在行上，整批 UPDATE 不回传它们。
	moved := issuesToReannounce(ctx, drop.ID)
	if err := issue_repo.Issue().ReassignProject(ctx, drop.ID, keep.ID); err != nil {
		return nil, err
	}
	for _, it := range moved {
		sync_svc.NotifyUpdate(ctx, syncwire.KindIssue, it.ID, it.SyncMeta)
	}
	if err := s.reassignProjectLocations(ctx, keep, drop); err != nil {
		return nil, err
	}

	if err := s.Delete(ctx, drop.ID); err != nil {
		return nil, err
	}
	return keep, nil
}

// issuesToReannounce 列出还挂在 projectID 名下的存活任务（整行，带同步标识）。
//
// 同步未装配时一次库都不查（与 project_svc.memberSyncMeta 同一口径）。软删的任务
// 不在其中，也不需要：它们各自的墓碑早已上行，account 侧从此不再有活着的对象，
// ReassignProject 那一下只是不让本机留下指向已消失项目的悬空引用。
//
// 读失败不回传：这一次读取只为同步层服务，同步层的任何失败都不该否决用户的合并
// （R8，与 project_svc.memberSyncMeta / issue_svc.labelLinksBefore 同一口径）。此刻
// 项目、会话、成员与子项目都已经改挂完毕，在这里返回错误只会留下一个半合并的库。
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
		ProjectSyncID:       keep.SyncID,
		AgentredFingerprint: loser.DeviceFingerprint,
		OccurredAt:          now,
		Createtime:          now,
	})
}
