package project_svc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/procattr"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
)

// ProjectSvc Project 模块的应用服务。
type ProjectSvc interface {
	Create(ctx context.Context, req *CreateProjectRequest) (*project_entity.Project, error)
	Update(ctx context.Context, req *UpdateProjectRequest) (*project_entity.Project, error)
	// Move 改父项目，含环检测。见 MoveProjectRequest。
	Move(ctx context.Context, req *MoveProjectRequest) (*project_entity.Project, error)
	Reorder(ctx context.Context, req *ReorderProjectsRequest) error
	Delete(ctx context.Context, id int64) error
	Get(ctx context.Context, id int64) (*ProjectDetail, error)
	ListTree(ctx context.Context) ([]*ProjectNode, error)
	// SetLocalPath 就地指定「本机未配置路径」（R10）项目的本机路径，解除该状态；
	// 路径必须存在。指定之后这个项目与本机创建的项目无任何差别（R10 末段）。
	SetLocalPath(ctx context.Context, id int64, path string) (*project_entity.Project, error)
	// ClearLocalPath 把这个项目打回「本机未配置路径」（规格 2026-08-21 决策 6）：
	// 清掉本机路径并置上该状态。**机器上的目录一个字节都不动**，去掉的只是
	// 「这个项目在本机落在哪」这条记录。已经是该状态时是幂等的。
	ClearLocalPath(ctx context.Context, id int64) (*project_entity.Project, error)
	// Merge 把 sourceID / targetID 两个本地项目行合并成一个（R11a）：沿用账号侧的
	// 同步标识（两边都没有时沿用先创建的那个的），保留本机项目的本机路径；
	// chat_sessions / project_agents / projects.parent_id / issues /
	// project_locations 五类引用全部改挂到保留下来的那一行，另一行随后被软删。
	Merge(ctx context.Context, req *MergeProjectsRequest) (*project_entity.Project, error)
	AddMember(ctx context.Context, projectID, agentID int64) error
	RemoveMember(ctx context.Context, projectID, agentID int64) error
	DetectGitRepo(ctx context.Context, path string) (*GitRepoInfo, error)

	// cwd
	ResolveSessionCwd(ctx context.Context, session *chat_entity.Session) (string, error)
	ResolveProjectCwd(ctx context.Context, projectID int64, deviceID string) (string, error)
}

type projectSvc struct {
	now      func() int64
	sessions SessionPort
	agents   AgentPort
}

var defaultProject ProjectSvc = New()

// Default 取默认服务单例。
func Default() ProjectSvc { return defaultProject }

// SetDefault 注入服务实现（测试用 / bootstrap 替换 stub git client 时用）。
func SetDefault(svc ProjectSvc) { defaultProject = svc }

// Option 定制 New 构造出的实例；未给出的窄依赖落到生产仓储单例(ISP,决策 5)。
type Option func(*projectSvc)

// WithSessionPort 供测试注入窄 chat_repo.SessionRepo mock。
func WithSessionPort(p SessionPort) Option { return func(s *projectSvc) { s.sessions = p } }

// WithAgentPort 供测试注入窄 agent_repo.AgentRepo mock。
func WithAgentPort(p AgentPort) Option { return func(s *projectSvc) { s.agents = p } }

// New 构造默认实现。
func New(opts ...Option) ProjectSvc {
	s := &projectSvc{
		now:      func() int64 { return time.Now().UnixMilli() },
		sessions: sessionRepoDelegate{},
		agents:   agentRepoDelegate{},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ──────────────────────────────────────────────────────────────────────────────
// CRUD
// ──────────────────────────────────────────────────────────────────────────────

func (s *projectSvc) Create(ctx context.Context, req *CreateProjectRequest) (*project_entity.Project, error) {
	now := s.now()
	path := strings.TrimSpace(req.Path)
	p := &project_entity.Project{
		ParentID:    req.ParentID,
		Name:        strings.TrimSpace(req.Name),
		Icon:        strings.TrimSpace(req.Icon),
		Color:       strings.TrimSpace(req.Color),
		Description: strings.TrimSpace(req.Description),
		Path:        path,
		// 路径不必填（规格 2026-08-22 决策 9）：没填就落成「本机未配置路径」那一档
		// （R10），与从账号同步下来、本机还没配路径的项目行是**同一种状态**——
		// 组头那枚可点的「未配置」角标就是它的出口。
		//
		// 由这里推导而不是让调用方传一个标志位：两者互斥（Check 保证 Path 非空与
		// LocalPathMissing 不同时成立），多一个入参就多一种说不通的组合。
		LocalPathMissing: path == "",
		Status:           consts.ACTIVE,
		Createtime:       now,
		Updatetime:       now,
	}
	if err := p.Check(ctx); err != nil {
		return nil, err
	}
	// 路径必须存在 —— 避免用户填错路径后 cwd 解析时才发现。「本机未配置路径」
	// (LocalPathMissing，R10)的项目行没有路径可校验，跳过这一步(R11 读取点)；
	// Check() 已保证此时 Path 非空，两者互斥。
	if !p.LocalPathMissing {
		if _, err := os.Stat(p.Path); err != nil {
			return nil, i18n.NewError(ctx, code.ProjectPathNotExist)
		}
	}
	// 父项目存在且 active。
	if p.ParentID > 0 {
		parent, err := project_repo.Project().Find(ctx, p.ParentID)
		if err != nil {
			return nil, err
		}
		if parent == nil {
			return nil, i18n.NewError(ctx, code.ProjectParentNotFound)
		}
		if !parent.IsActive() {
			return nil, i18n.NewError(ctx, code.ProjectParentInactive)
		}
	}
	// 同级名字唯一。
	dup, err := project_repo.Project().FindByName(ctx, p.ParentID, p.Name)
	if err != nil {
		return nil, err
	}
	if dup != nil {
		return nil, i18n.NewError(ctx, code.ProjectNameDuplicated)
	}
	next, err := project_repo.Project().NextSortOrder(ctx, p.ParentID)
	if err != nil {
		return nil, err
	}
	p.SortOrder = next

	if err := project_repo.Project().Create(ctx, p); err != nil {
		return nil, err
	}
	sync_svc.NotifyCreate(ctx, syncwire.KindProject, p.ID, p.SyncMeta)

	// 初始成员 —— 失败不回滚（用户可以在设置里再加），但记日志。
	for _, agentID := range req.InitialAgentIDs {
		if agentID <= 0 {
			continue
		}
		if err := project_repo.ProjectAgent().Add(ctx, p.ID, agentID); err != nil {
			logger.Ctx(ctx).Warn("project_svc.Create: initial agent add failed",
				zap.Int64("projectId", p.ID),
				zap.Int64("agentId", agentID), zap.Error(err))
			continue
		}
		notifyMemberChange(ctx, p.ID, agentID, sync_svc.OpCreate)
	}
	return p, nil
}

func (s *projectSvc) Update(ctx context.Context, req *UpdateProjectRequest) (*project_entity.Project, error) {
	existing, err := project_repo.Project().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, i18n.NewError(ctx, code.ProjectNotFound)
	}
	newName := strings.TrimSpace(req.Name)
	if newName != existing.Name {
		dup, err := project_repo.Project().FindByName(ctx, existing.ParentID, newName)
		if err != nil {
			return nil, err
		}
		if dup != nil && dup.ID != existing.ID {
			return nil, i18n.NewError(ctx, code.ProjectNameDuplicated)
		}
	}
	existing.Name = newName
	existing.Icon = strings.TrimSpace(req.Icon)
	existing.Color = strings.TrimSpace(req.Color)
	existing.Description = strings.TrimSpace(req.Description)
	if err := existing.Check(ctx); err != nil {
		return nil, err
	}
	if err := project_repo.Project().Update(ctx, existing); err != nil {
		return nil, err
	}
	sync_svc.NotifyUpdate(ctx, syncwire.KindProject, existing.ID, existing.SyncMeta)
	return existing, nil
}

// Move 改父项目，含环检测（规格 2026-08-22 B 段「基本」里的「父项目」那一格）。
//
// 形状照部门那份 `department_svc.Move`：父级存在 + active + 环检测。同一条判据不该
// 在两棵树上长出两个样子。
//
// 环检测**必须在服务端**：设置弹窗把自己从候选里剔掉了，但禁用一个下拉项拦不住
// 直接打端点，而一个环会让每一端的树遍历都走不完。
func (s *projectSvc) Move(ctx context.Context, req *MoveProjectRequest) (*project_entity.Project, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	existing, err := project_repo.Project().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, i18n.NewError(ctx, code.ProjectNotFound)
	}
	if existing.ParentID == req.NewParentID {
		return existing, nil
	}
	if req.NewParentID > 0 {
		parent, err := project_repo.Project().Find(ctx, req.NewParentID)
		if err != nil {
			return nil, err
		}
		if parent == nil {
			return nil, i18n.NewError(ctx, code.ProjectParentNotFound)
		}
		if !parent.IsActive() {
			return nil, i18n.NewError(ctx, code.ProjectParentInactive)
		}
		all, err := project_repo.Project().List(ctx)
		if err != nil {
			return nil, err
		}
		if hasProjectCycle(all, req.NewParentID, existing.ID) {
			return nil, i18n.NewError(ctx, code.ProjectCircularReference)
		}
	}
	// 换了一层之后同级重名要重新判：原来那一层下不重名，不代表新的一层下也不重名。
	dup, err := project_repo.Project().FindByName(ctx, req.NewParentID, existing.Name)
	if err != nil {
		return nil, err
	}
	if dup != nil && dup.ID != existing.ID {
		return nil, i18n.NewError(ctx, code.ProjectNameDuplicated)
	}
	existing.ParentID = req.NewParentID
	existing.Updatetime = s.now()
	if err := project_repo.Project().Update(ctx, existing); err != nil {
		return nil, err
	}
	sync_svc.NotifyUpdate(ctx, syncwire.KindProject, existing.ID, existing.SyncMeta)
	return existing, nil
}

// hasProjectCycle 从 startParentID 沿 parent 链向上爬，命中 selfID 即成环。
// 「挂到自己身上」是这条链上最短的那个环，不必单独判。
func hasProjectCycle(all []*project_entity.Project, startParentID, selfID int64) bool {
	index := make(map[int64]*project_entity.Project, len(all))
	for _, p := range all {
		index[p.ID] = p
	}
	cur := startParentID
	for cur > 0 {
		if cur == selfID {
			return true
		}
		next, ok := index[cur]
		if !ok {
			return false
		}
		cur = next.ParentID
	}
	return false
}

// SetLocalPath 见 ProjectSvc 接口注释（R10）。
//
// 校验与 Create 的路径守卫一致：非空 + 目录存在。不调用 sync_svc.NotifyUpdate——
// 本机路径本就不参与同步载荷（决策 6），指定/更换它是纯本地事件，不应该让这一行
// 在账号侧显得「又改了」。
func (s *projectSvc) SetLocalPath(ctx context.Context, id int64, path string) (*project_entity.Project, error) {
	existing, err := project_repo.Project().Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, i18n.NewError(ctx, code.ProjectNotFound)
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, i18n.NewError(ctx, code.ProjectInvalidPath)
	}
	if _, err := os.Stat(trimmed); err != nil {
		return nil, i18n.NewError(ctx, code.ProjectPathNotExist)
	}
	existing.Path = trimmed
	existing.LocalPathMissing = false
	if err := project_repo.Project().Update(ctx, existing); err != nil {
		return nil, err
	}
	return existing, nil
}

// ClearLocalPath 见 ProjectSvc 接口注释（规格 2026-08-21 决策 6）。
//
// 与 SetLocalPath 同样不调用 sync_svc.NotifyUpdate——本机路径本就不参与同步载荷，
// 去掉它是纯本地事件，不该让这一行在账号侧显得「又改了」。
func (s *projectSvc) ClearLocalPath(ctx context.Context, id int64) (*project_entity.Project, error) {
	existing, err := project_repo.Project().Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, i18n.NewError(ctx, code.ProjectNotFound)
	}
	existing.Path = ""
	existing.LocalPathMissing = true
	if err := project_repo.Project().Update(ctx, existing); err != nil {
		return nil, err
	}
	return existing, nil
}

func (s *projectSvc) Reorder(ctx context.Context, req *ReorderProjectsRequest) error {
	if req == nil || req.ParentID < 0 || len(req.OrderedIDs) == 0 {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	siblings, err := project_repo.Project().ListByParent(ctx, req.ParentID)
	if err != nil {
		return err
	}
	if len(siblings) != len(req.OrderedIDs) {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	allowed := make(map[int64]struct{}, len(siblings))
	for _, p := range siblings {
		allowed[p.ID] = struct{}{}
	}
	seen := make(map[int64]struct{}, len(req.OrderedIDs))
	for _, id := range req.OrderedIDs {
		if id <= 0 {
			return i18n.NewError(ctx, code.InvalidParameter)
		}
		if _, ok := allowed[id]; !ok {
			return i18n.NewError(ctx, code.InvalidParameter)
		}
		if _, ok := seen[id]; ok {
			return i18n.NewError(ctx, code.InvalidParameter)
		}
		seen[id] = struct{}{}
	}
	if err := project_repo.Project().ReorderSiblings(ctx, req.ParentID, req.OrderedIDs); err != nil {
		return err
	}
	for _, sibling := range siblings {
		sync_svc.NotifyUpdate(ctx, syncwire.KindProject, sibling.ID, sibling.SyncMeta)
	}
	return nil
}

func (s *projectSvc) Delete(ctx context.Context, id int64) error {
	existing, err := project_repo.Project().Find(ctx, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return i18n.NewError(ctx, code.ProjectNotFound)
	}
	hasChildren, err := project_repo.Project().HasActiveChildren(ctx, id)
	if err != nil {
		return err
	}
	if hasChildren {
		return i18n.NewError(ctx, code.ProjectHasChildren)
	}
	// 还有 running / waiting 会话时拒绝；idle / error 等允许（用户主动归档）。
	n, err := s.sessions.CountActiveByProject(ctx, id, []string{"running", "waiting"})
	if err != nil {
		return err
	}
	if n > 0 {
		return i18n.NewError(ctx, code.ProjectHasActiveSessions)
	}
	// 名下幸存的（idle / error）会话改挂成自由会话，而不是留下指向已删项目的悬空
	// project_id。ReassignProject 刻意不带 status / purpose 过滤（见 chat_repo 那边
	// 的注释），软删会话与子 agent 委派会话一并摘干净，与 R11a 合并同一条要求。
	//
	// 顺序是「先摘引用、再删项目行」：中途失败时项目还在，用户可以重试；反过来会
	// 留下一批指向不存在项目的会话。失败即整体失败，不留半个状态。
	if err := s.sessions.ReassignProject(ctx, id, 0); err != nil {
		return err
	}
	if err := project_repo.Project().Delete(ctx, id); err != nil {
		return err
	}
	// 名下的路径记录与成员关系随它一并落墓碑，级联在同步层展开（R6）。
	sync_svc.NotifyDelete(ctx, syncwire.KindProject, existing.ID, existing.SyncMeta)
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Read / Tree
// ──────────────────────────────────────────────────────────────────────────────

func (s *projectSvc) Get(ctx context.Context, id int64) (*ProjectDetail, error) {
	p, err := project_repo.Project().Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, i18n.NewError(ctx, code.ProjectNotFound)
	}
	direct, inherited, err := s.aggregateMembers(ctx, p)
	if err != nil {
		return nil, err
	}
	if err := s.hydrateMemberAgents(ctx, direct, inherited); err != nil {
		return nil, err
	}
	return &ProjectDetail{Project: p, DirectMembers: direct, InheritedMembers: inherited}, nil
}

func (s *projectSvc) ListTree(ctx context.Context) ([]*ProjectNode, error) {
	rows, err := project_repo.Project().List(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*ProjectNode, len(rows))
	roots := make([]*ProjectNode, 0)
	for _, p := range rows {
		byID[p.ID] = &ProjectNode{Project: p}
	}
	for _, p := range rows {
		node := byID[p.ID]
		if p.ParentID == 0 {
			roots = append(roots, node)
			continue
		}
		parent, ok := byID[p.ParentID]
		if !ok {
			// 父项目被软删 / 不存在 —— 当顶层挂出，避免「漂浮」节点丢失。
			roots = append(roots, node)
			continue
		}
		parent.Children = append(parent.Children, node)
	}
	return roots, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Members
// ──────────────────────────────────────────────────────────────────────────────

func (s *projectSvc) AddMember(ctx context.Context, projectID, agentID int64) error {
	if projectID <= 0 || agentID <= 0 {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	p, err := project_repo.Project().Find(ctx, projectID)
	if err != nil {
		return err
	}
	if p == nil {
		return i18n.NewError(ctx, code.ProjectNotFound)
	}
	a, err := s.agents.Find(ctx, agentID)
	if err != nil {
		return err
	}
	if a == nil {
		return i18n.NewError(ctx, code.ProjectAgentNotFound)
	}
	if err := project_repo.ProjectAgent().Add(ctx, projectID, agentID); err != nil {
		return err
	}
	notifyMemberChange(ctx, projectID, agentID, sync_svc.OpCreate)
	return nil
}

func (s *projectSvc) RemoveMember(ctx context.Context, projectID, agentID int64) error {
	if projectID <= 0 || agentID <= 0 {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	// 成员关系是硬删：同步标识必须在行消失之前读出来（R6 的墓碑靠它上行）。
	meta := memberSyncMeta(ctx, projectID, agentID)
	if err := project_repo.ProjectAgent().Remove(ctx, projectID, agentID); err != nil {
		return err
	}
	sync_svc.NotifyDelete(ctx, syncwire.KindProjectAgent, 0, meta)
	return nil
}

// notifyMemberChange / memberSyncMeta 把成员关系那一行的同步元数据交给同步层。
// 同步未装配时一次库都不查（Add 不回传落库后的行，只能反查一次）。
func notifyMemberChange(ctx context.Context, projectID, agentID int64, op string) {
	if !sync_svc.Active() {
		return
	}
	sync_svc.Notify(ctx, sync_svc.LocalChange{
		Kind: syncwire.KindProjectAgent, Op: op,
		Meta: memberSyncMeta(ctx, projectID, agentID),
	})
}

func memberSyncMeta(ctx context.Context, projectID, agentID int64) syncmeta_entity.SyncMeta {
	if !sync_svc.Active() {
		return syncmeta_entity.SyncMeta{}
	}
	rows, err := project_repo.ProjectAgent().ListByProject(ctx, projectID)
	if err != nil {
		logger.Ctx(ctx).Warn("project_svc.memberSyncMeta: read membership failed",
			zap.Int64("projectId", projectID), zap.Error(err))
		return syncmeta_entity.SyncMeta{}
	}
	for _, row := range rows {
		if row.AgentID == agentID {
			return row.SyncMeta
		}
	}
	return syncmeta_entity.SyncMeta{}
}

// ──────────────────────────────────────────────────────────────────────────────
// Git detect（轻量探测，不进入热路径）
// ──────────────────────────────────────────────────────────────────────────────

// DetectGitRepo 探测 path 是否 git 仓库，返回当前分支 / origin。
// 新建项目模态用 —— 用户选完目录后立刻探测一次。git 子命令失败（无 origin / detached HEAD）
// 不算硬错，只是少填字段；只在 Stat 出错时返回 IsGitRepo=false。
//
// path 是用户从「目录选择器」拿到的字符串 —— 已限定为本地路径，不会被远端 inject。
// gosec G204 在这里属于误报，但仍把 path 转成 absolute 再透传，避免 git 解释成
// option（如 --no-pager）。
func (s *projectSvc) DetectGitRepo(ctx context.Context, path string) (*GitRepoInfo, error) {
	path = strings.TrimSpace(path)
	out := &GitRepoInfo{}
	if path == "" {
		return out, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return out, nil //nolint:nilerr // 路径非法对 UI 是"不是 git 仓库"等价信号
	}
	if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
		return out, nil //nolint:nilerr // 同上，缺 .git 等价"非 git 仓库"
	}
	out.IsGitRepo = true
	branchCmd := exec.CommandContext( //nolint:gosec // abs 来自本地目录选择器
		ctx, "git", "-C", abs, "rev-parse", "--abbrev-ref", "HEAD")
	procattr.ApplyNoConsoleWindow(branchCmd)
	if branch, err := branchCmd.Output(); err == nil {
		out.CurrentBranch = strings.TrimSpace(string(branch))
	}
	originCmd := exec.CommandContext( //nolint:gosec // 同上
		ctx, "git", "-C", abs, "remote", "get-url", "origin")
	procattr.ApplyNoConsoleWindow(originCmd)
	if origin, err := originCmd.Output(); err == nil {
		out.Origin = strings.TrimSpace(string(origin))
	}
	return out, nil
}
