// Package chat_repo 提供 chat session / message 的持久化访问。
package chat_repo

import (
	"context"
	"errors"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/consts"
	"gorm.io/gorm"

	"github.com/agentre-ai/agentre/internal/model/entity/chat_entity"
)

//go:generate mockgen -source session.go -destination mock_chat_repo/mock_session.go

type SessionRepo interface {
	Find(ctx context.Context, id int64) (*chat_entity.Session, error)
	ListByAgent(ctx context.Context, agentID int64, limit int) ([]*chat_entity.Session, error)
	ListByAgentIncludingGroups(ctx context.Context, agentID int64, limit int) ([]*chat_entity.Session, error)
	ListByAgentPaged(ctx context.Context, agentID int64, offset, limit int) ([]*chat_entity.Session, error)
	ListByAgentPagedIncludingGroups(ctx context.Context, agentID int64, offset, limit int) ([]*chat_entity.Session, error)
	ListIDsByAgents(ctx context.Context, agentIDs []int64) (map[int64][]int64, error)
	ListIDsByAgentsIncludingGroups(ctx context.Context, agentIDs []int64) (map[int64][]int64, error)
	ListAttentionByAgent(ctx context.Context, agentID int64, limit int) ([]*chat_entity.Session, error)
	ListAttentionByAgentIncludingGroups(ctx context.Context, agentID int64, limit int) ([]*chat_entity.Session, error)
	ListByProject(ctx context.Context, projectID int64) ([]*chat_entity.Session, error)
	// ListRecentPaged 按 last_message_at DESC 翻页返回全部未删除会话，**不限 agent、
	// 不限项目**。单一会话索引的「按时间」档要的就是这条跨维度的最近活动流 ——
	// 按 agent 的变体各自只看一个 agent，把它们并起来只能得到一个窗口而不是全量。
	ListRecentPaged(ctx context.Context, offset, limit int) ([]*chat_entity.Session, error)
	// ListFreePaged 同上，但只要**未挂项目**（project_id = 0）的会话，即索引里的
	// 「随手对话」组。它等价于 ListByProject(0)，独立成方法是因为服务层刻意把
	// ListSessions 挡在 projectID > 0：0 不是一个项目，不该从项目那条路进来。
	ListFreePaged(ctx context.Context, offset, limit int) ([]*chat_entity.Session, error)
	// ListByProjectPaged 是 ListByProject 的分页版。索引的项目组默认只展开前几条，
	// 其余走「查看全部 N」——一次性拉一个项目的全部会话（ListByProject 的口径）在
	// 侧栏这条路上没有必要。
	ListByProjectPaged(ctx context.Context, projectID int64, offset, limit int) ([]*chat_entity.Session, error)
	// CountAll / CountFree / CountByProject 是上面几个列表各自的总数，
	// 供「还有 N 条」与翻页终止判断。
	CountAll(ctx context.Context) (int64, error)
	CountFree(ctx context.Context) (int64, error)
	CountByProject(ctx context.Context, projectID int64) (int64, error)
	// ReassignProject 把 project_id 从 fromProjectID 整批改挂到 toProjectID（R11a
	// 的项目合并）。刻意**不带 status / purpose 过滤**：软删的会话与子 agent 委派
	// 会话在 ListByProject 里都看不见（后者被 nonSubagentScope 排除），逐行改挂必然
	// 把它们漏在原地，而 R11a 要求合并后不留下任何指向已消失项目的引用。
	ReassignProject(ctx context.Context, fromProjectID, toProjectID int64) error
	CountByAgent(ctx context.Context, agentID int64) (int64, error)
	CountByAgentIncludingGroups(ctx context.Context, agentID int64) (int64, error)
	CountByAgents(ctx context.Context, agentIDs []int64) (map[int64]int64, error)
	CountByAgentsIncludingGroups(ctx context.Context, agentIDs []int64) (map[int64]int64, error)
	CountRunningByAgents(ctx context.Context, agentIDs []int64) (map[int64]int, error)
	CountActiveByProject(ctx context.Context, projectID int64, agentStatuses []string) (int64, error)
	CountActive(ctx context.Context, agentStatuses []string) (int64, error)
	Create(ctx context.Context, s *chat_entity.Session) error
	Update(ctx context.Context, s *chat_entity.Session) error
	UpdatePermissionMode(ctx context.Context, sessionID int64, mode string) error
	// UpdatePermissionModeAtLaunch sets the launched-mode snapshot for a session.
	// Called by the claudecode runner after spawning the CLI subprocess. Never
	// invoked through the user-facing SetPermissionMode IPC — that one only
	// touches permission_mode.
	UpdatePermissionModeAtLaunch(ctx context.Context, sessionID int64, mode string) error
	// UpdateModelTarget 改写会话级 LLM ModelTarget(provider_key + model_key 同一条
	// 原子语句,spec 2026-08-11 决策 1)。
	//   - providerKey 空 + modelKey 空 = inherit-agent(跟随 agent 绑定);
	//   - providerKey 非空 + modelKey 空 = provider-default(每轮解析该 Provider 当前默认);
	//   - 两者都非空 = fixed-model(解析指定启用子模型)。
	// 由 chat_svc.SetChatSessionModelTarget 调用;切换允许发生在轮中,所以只碰这两列,
	// 不走整行 Save —— 后者会把并发轮次正在写的状态列一起盖掉。同理这两列都在 Update
	// 的 Omit 清单里:轮次收尾的整行回写拿的是轮次开始时读出的旧实体。
	UpdateModelTarget(ctx context.Context, sessionID int64, providerKey, modelKey string) error
	// UpdateContextWindow 落库 runtime 探到的 model 上下文窗口。轮内随时可能到帧,
	// 且**带外轮**(自主续轮 / 后台 subagent 活动轮)也会写它 —— 而带外轮手里的实体
	// 是它起步时读出的快照。走整行 Save 的话,用户在带外轮进行中发的新一轮刚写好的
	// agent_status=running / last_message_at 会被那份旧快照原样拍回去,会话在库里退
	// 回 idle(sess-2974)。所以这里只碰 context_window 一列。
	UpdateContextWindow(ctx context.Context, sessionID int64, tokens int) error
	// UpdateExecDaemon 记录执行该会话的配对 daemon(paired_agentreds.id)及其实例标识
	// (sha256:<hex>)、以及这条会话钉住的执行目标档(agentBackendID,R15b / 决策36)。
	// deviceID=0 + 空标识表示回到本机执行；agentBackendID=0 表示尚未钉住。三列同一条
	// 语句一并写入、一并加进 Update 的 Omit 清单，不拆成两个写入点——这是已经踩过的坑
	// (见 session.go Update 上的注释)。实例标识变了(改绑到别的 daemon / 改回本机)时,
	// event_cursor 在同一条语句里归零 —— 游标只在它所属的那条通知日志里有意义,不能
	// 跟着会话漂到另一台 daemon 上。标识不变则原样保留游标。
	UpdateExecDaemon(ctx context.Context, sessionID int64, deviceID int64, daemonFingerprint string, agentBackendID int64) error
	// UpdateEventCursor 记录桌面端已消费到的 daemon 通知 seq。只碰这一列,执行位置与
	// 实例标识由 UpdateExecDaemon 负责。daemonFingerprint 是 seq 所属的那条通知日志的
	// daemon 实例标识,进 WHERE 做守卫:会话已改绑后老连接迟到的写入落空(不报错,同
	// MarkRead 的「写不进也算成功」),下次重连至多重复拉取,而不会跳过新日志的开头。
	UpdateEventCursor(ctx context.Context, sessionID int64, daemonFingerprint string, seq int64) error
	// ListRemoteExecSessions 列出记录了远端执行位置的活跃会话(exec_device_id > 0 且
	// 带实例标识)。App 启动后的补齐靠它回答「该连谁」——(会话, daemon, 游标) 三者
	// 都在这一行上,不必再回头遍历 agent / backend 才知道会话跑在哪。
	//
	// 实例标识为空的行一并排除:游标只在它所属的那条通知日志里有意义,标识为空时
	// LoadCursor 一律判失效,对它发起补齐只是白跑一轮 RPC。
	//
	// 取材是**有界**的(见 catchUpLimit):补齐会为每条会话装一个轮次消费方、加一份池
	// 连接引用、开一条自主轮监视,所以它不能返回「历史上曾远端执行过的每一条」。界是
	// 条数,不是时间 —— 一条本地停在 idle、远端却由后台任务续过轮的老会话,日志今天还
	// 在(agentred 不再回收),按时间挡掉它就是永远补不回来。落在界外的会话不会被补齐、
	// 也不会被判定(一行都不碰),下次用户在它上面发消息时照常走 borrow → attach,该拉
	// 的日志一条不少。
	ListRemoteExecSessions(ctx context.Context) ([]*chat_entity.Session, error)
	// MarkRead 单调推进 last_read_at: 仅当 ts 严格大于当前值时写入。
	// 避免 stream-done 与 LoadSession 乱序时把已读时间冲回旧值。
	// 会话不存在 / 已软删 / ts 不更新 都算成功（不返回 ErrRecordNotFound）。
	MarkRead(ctx context.Context, sessionID int64, ts int64) error
	SoftDelete(ctx context.Context, id int64) error
	// ResetActiveSessions 启动期把所有 agent_status IN ('running','waiting') 且
	// 未软删除的 session 翻成 'error'。app crash / 强行
	// 重启 / wails dev hot-reload 都会留下 turn goroutine 死了但 DB 状态没收
	// 尾的"重启遗孤",前端 sidebar 会一直亮"运行中"。该清理不能在 bootstrap.Init
	// 里直接调用；主 Wails 实例 Startup 后再调,确保第二实例不会误伤仍在运行的 turn。
	// 返回受影响行数,仅供日志使用。
	//
	// **跑在远端 daemon 上的会话(exec_device_id > 0 且带实例标识)不在清理范围内**:
	// 它们的执行者是另一台机器上的进程,不随桌面 App 退出而消亡(R4)。在连上那台
	// daemon 之前无从知道它是不是还在跑,翻成 error 就是报一个假失败 —— 而一条在桌面端
	// 离线期间没产出任何新内容的会话不会被重放改写,那条假失败会永久留在界面上。
	// 它们改由 chat_svc.CatchUpRemoteSessions 按 daemon 交回的生命周期逐条收尾
	// (见 ResetActiveSessionsByIDs)。
	ResetActiveSessions(ctx context.Context) (int64, error)
	// ResetActiveSessionsByIDs 把点名的这些会话里还停在 running / waiting 的翻成
	// 'error',返回受影响行数。空列表不发 SQL;id 多了按 resetIDChunk 分批发,
	// 一条塞满的 IN ? 会撞上 SQLite 的预编译参数上限。
	//
	// 它是启动期清理在远端会话那一半的落点:补齐连上 daemon、拿到会话清单之后,
	// daemon 说不在跑的那些才由它收尾。
	ResetActiveSessionsByIDs(ctx context.Context, ids []int64) (int64, error)
}

var defaultSession SessionRepo

func Session() SessionRepo             { return defaultSession }
func RegisterSession(impl SessionRepo) { defaultSession = impl }
func NewSession() SessionRepo          { return &sessionRepo{} }

// nonSubagentScope 排除子 agent 委派会话(purpose='subagent_call')。这类会话由 agent_call
// 同步委派出来、一次性隔离, 不是用户顶层会话, 不应出现在任何 agent/项目的会话列表或计数里。
// 本 scope 必须无条件挂在每个列表/计数查询上, 否则它会从侧栏(走 IncludingGroups 变体)漏出来。
func nonSubagentScope(db *gorm.DB) *gorm.DB {
	return db.Where("purpose <> ?", chat_entity.SessionPurposeSubagent)
}

type sessionRepo struct{}

func (r *sessionRepo) Find(ctx context.Context, id int64) (*chat_entity.Session, error) {
	out := &chat_entity.Session{}
	err := db.Ctx(ctx).Where("id = ? AND status = ?", id, consts.ACTIVE).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out.ApplyDerivedFields()
	return out, nil
}

func (r *sessionRepo) ListByAgent(ctx context.Context, agentID int64, limit int) ([]*chat_entity.Session, error) {
	return r.listByAgent(ctx, agentID, limit, true)
}

func (r *sessionRepo) ListByAgentIncludingGroups(ctx context.Context, agentID int64, limit int) ([]*chat_entity.Session, error) {
	return r.listByAgent(ctx, agentID, limit, false)
}

func (r *sessionRepo) listByAgent(ctx context.Context, agentID int64, limit int, ordinaryOnly bool) ([]*chat_entity.Session, error) {
	if limit <= 0 {
		limit = 5
	}
	var rows []*chat_entity.Session
	q := db.Ctx(ctx).
		Where("agent_id = ? AND status = ?", agentID, consts.ACTIVE).
		Scopes(nonSubagentScope)
	err := q.
		Order("last_message_at DESC, id DESC").
		Limit(limit).
		Find(&rows).Error
	applySessionDerivedFields(rows)
	return rows, err
}

// ListByAgentPaged 按 last_message_at DESC 翻页返回 agent 的未删除会话。
// 服务层负责对 offset/limit 做边界裁剪；repo 只忠实按参数查。
func (r *sessionRepo) ListByAgentPaged(ctx context.Context, agentID int64, offset, limit int) ([]*chat_entity.Session, error) {
	return r.listByAgentPaged(ctx, agentID, offset, limit, true)
}

func (r *sessionRepo) ListByAgentPagedIncludingGroups(ctx context.Context, agentID int64, offset, limit int) ([]*chat_entity.Session, error) {
	return r.listByAgentPaged(ctx, agentID, offset, limit, false)
}

func (r *sessionRepo) listByAgentPaged(ctx context.Context, agentID int64, offset, limit int, ordinaryOnly bool) ([]*chat_entity.Session, error) {
	var rows []*chat_entity.Session
	q := db.Ctx(ctx).
		Where("agent_id = ? AND status = ?", agentID, consts.ACTIVE).
		Scopes(nonSubagentScope)
	err := q.
		Order("last_message_at DESC, id DESC").
		Offset(offset).
		Limit(limit).
		Find(&rows).Error
	applySessionDerivedFields(rows)
	return rows, err
}

func (r *sessionRepo) ListIDsByAgents(ctx context.Context, agentIDs []int64) (map[int64][]int64, error) {
	return r.listIDsByAgents(ctx, agentIDs, true)
}

func (r *sessionRepo) ListIDsByAgentsIncludingGroups(ctx context.Context, agentIDs []int64) (map[int64][]int64, error) {
	return r.listIDsByAgents(ctx, agentIDs, false)
}

func (r *sessionRepo) listIDsByAgents(ctx context.Context, agentIDs []int64, ordinaryOnly bool) (map[int64][]int64, error) {
	out := make(map[int64][]int64, len(agentIDs))
	if len(agentIDs) == 0 {
		return out, nil
	}
	rows := []struct {
		AgentID int64 `gorm:"column:agent_id"`
		ID      int64 `gorm:"column:id"`
	}{}
	q := db.Ctx(ctx).
		Table("chat_sessions").
		Select("agent_id, id").
		Where("agent_id IN ? AND status = ?", agentIDs, consts.ACTIVE).
		Scopes(nonSubagentScope)
	err := q.
		Order("agent_id ASC, last_message_at DESC, id DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.AgentID] = append(out[row.AgentID], row.ID)
	}
	return out, nil
}

// ListAttentionByAgent 给 sidebar 折叠态的 attention bubble 用：返回该 agent 下
// 当前需要用户关注的会话 —— 跑步中、等待用户输入/审批、或出错的。
// 按 last_message_at DESC 排序；limit 由 service 传入（典型 20，防止异常数据撑爆 UI）。
func (r *sessionRepo) ListAttentionByAgent(ctx context.Context, agentID int64, limit int) ([]*chat_entity.Session, error) {
	return r.listAttentionByAgent(ctx, agentID, limit, true)
}

func (r *sessionRepo) ListAttentionByAgentIncludingGroups(ctx context.Context, agentID int64, limit int) ([]*chat_entity.Session, error) {
	return r.listAttentionByAgent(ctx, agentID, limit, false)
}

func (r *sessionRepo) listAttentionByAgent(ctx context.Context, agentID int64, limit int, ordinaryOnly bool) ([]*chat_entity.Session, error) {
	var rows []*chat_entity.Session
	q := db.Ctx(ctx).
		Where("agent_id = ? AND status = ? AND agent_status IN ?",
			agentID, consts.ACTIVE, []string{"running", "waiting", "error"}).
		Scopes(nonSubagentScope)
	err := q.
		Order("last_message_at DESC, id DESC").
		Limit(limit).
		Find(&rows).Error
	applySessionDerivedFields(rows)
	return rows, err
}

// CountByAgents 批量统计每个 agent 的未删除会话数。
// 用于 ListAgents 一次把侧栏「查看全部 N 个会话」需要的总数都查出来，
// 避免每个 agent 单独发一条 COUNT。
func (r *sessionRepo) CountByAgents(ctx context.Context, agentIDs []int64) (map[int64]int64, error) {
	return r.countByAgents(ctx, agentIDs, true)
}

func (r *sessionRepo) CountByAgentsIncludingGroups(ctx context.Context, agentIDs []int64) (map[int64]int64, error) {
	return r.countByAgents(ctx, agentIDs, false)
}

func (r *sessionRepo) countByAgents(ctx context.Context, agentIDs []int64, ordinaryOnly bool) (map[int64]int64, error) {
	out := make(map[int64]int64, len(agentIDs))
	if len(agentIDs) == 0 {
		return out, nil
	}
	rows := []struct {
		AgentID int64 `gorm:"column:agent_id"`
		N       int64 `gorm:"column:n"`
	}{}
	q := db.Ctx(ctx).
		Table("chat_sessions").
		Select("agent_id, COUNT(*) AS n").
		Where("agent_id IN ? AND status = ?", agentIDs, consts.ACTIVE).
		Scopes(nonSubagentScope)
	err := q.
		Group("agent_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.AgentID] = row.N
	}
	return out, nil
}

// CountByAgent 给 popover 拼 hasMore / "已加载 X / Y" 用。
func (r *sessionRepo) CountByAgent(ctx context.Context, agentID int64) (int64, error) {
	return r.countByAgent(ctx, agentID, true)
}

func (r *sessionRepo) CountByAgentIncludingGroups(ctx context.Context, agentID int64) (int64, error) {
	return r.countByAgent(ctx, agentID, false)
}

func (r *sessionRepo) countByAgent(ctx context.Context, agentID int64, ordinaryOnly bool) (int64, error) {
	var n int64
	q := db.Ctx(ctx).
		Model(&chat_entity.Session{}).
		Where("agent_id = ? AND status = ?", agentID, consts.ACTIVE).
		Scopes(nonSubagentScope)
	err := q.Count(&n).Error
	return n, err
}

// CountRunningByAgents 统计每个 agent 处在 "running" 状态的未删除会话数,
// 用于侧栏判断 agent 是否真的正在跑 turn(对应 UI 上的"运行中"呼吸灯)。
// 注意:不要把 consts.ACTIVE(软删除位)误用为"运行中"语义 —— 那会让任何有历史会话的
// agent 一直亮灯。真实"是否在跑"由 chat_sessions.agent_status 表达。
//
// 挂 nonSubagentScope: 子 agent 委派会话从侧栏隐藏、点不进去,让它点亮呼吸灯会留下
// 「亮灯却无会话可看」的死角,故运行中的子 agent 轮不计入呼吸灯。
func (r *sessionRepo) CountRunningByAgents(ctx context.Context, agentIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(agentIDs))
	if len(agentIDs) == 0 {
		return out, nil
	}
	rows := []struct {
		AgentID int64 `gorm:"column:agent_id"`
		N       int   `gorm:"column:n"`
	}{}
	err := db.Ctx(ctx).
		Table("chat_sessions").
		Select("agent_id, COUNT(*) AS n").
		Where("agent_id IN ? AND agent_status = ? AND status = ?", agentIDs, "running", consts.ACTIVE).
		Scopes(nonSubagentScope).
		Group("agent_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.AgentID] = row.N
	}
	return out, nil
}

// ListByProject 返回该项目下的全部未软删除会话，按 last_message_at DESC 排。
// 项目页 ChatProjectList 用它把 sessions 挂在 ProjectCard 下。
// 子 agent 委派会话(purpose=subagent_call)仍被 nonSubagentScope 排除。
func (r *sessionRepo) ListByProject(ctx context.Context, projectID int64) ([]*chat_entity.Session, error) {
	var rows []*chat_entity.Session
	err := db.Ctx(ctx).
		Where("project_id = ? AND status = ?", projectID, consts.ACTIVE).
		Scopes(nonSubagentScope).
		Order("last_message_at DESC, id DESC").
		Find(&rows).Error
	applySessionDerivedFields(rows)
	return rows, err
}

// indexScope 是「会话索引」几条查询共用的 WHERE：未软删 + 排除子 agent 委派会话，
// 再按 projectFilter 收窄。ORDER 与分页由调用方拼，计数不需要它们。
//
// projectFilter 为 nil 表示不限项目（时间轴）；指向 0 即「随手对话」，指向正数即某个
// 项目。用指针而不是 -1 之类的哨兵：0 是一个**有意义的取值**，哨兵会把它吃掉。
func indexScope(projectFilter *int64) func(*gorm.DB) *gorm.DB {
	return func(d *gorm.DB) *gorm.DB {
		if projectFilter != nil {
			d = d.Where("project_id = ? AND status = ?", *projectFilter, consts.ACTIVE)
		} else {
			d = d.Where("status = ?", consts.ACTIVE)
		}
		return d.Scopes(nonSubagentScope)
	}
}

func (r *sessionRepo) listIndexPaged(ctx context.Context, projectFilter *int64, offset, limit int) ([]*chat_entity.Session, error) {
	var rows []*chat_entity.Session
	err := db.Ctx(ctx).
		Scopes(indexScope(projectFilter)).
		Order("last_message_at DESC, id DESC").
		Offset(offset).
		Limit(limit).
		Find(&rows).Error
	applySessionDerivedFields(rows)
	return rows, err
}

func (r *sessionRepo) countIndex(ctx context.Context, projectFilter *int64) (int64, error) {
	var n int64
	err := db.Ctx(ctx).Model(&chat_entity.Session{}).
		Scopes(indexScope(projectFilter)).
		Count(&n).Error
	return n, err
}

// ListRecentPaged 见接口注释：不限 agent、不限项目的最近活动分页。
func (r *sessionRepo) ListRecentPaged(ctx context.Context, offset, limit int) ([]*chat_entity.Session, error) {
	return r.listIndexPaged(ctx, nil, offset, limit)
}

// ListFreePaged 见接口注释：仅 project_id = 0 的会话。
func (r *sessionRepo) ListFreePaged(ctx context.Context, offset, limit int) ([]*chat_entity.Session, error) {
	free := int64(0)
	return r.listIndexPaged(ctx, &free, offset, limit)
}

// ListByProjectPaged 见接口注释：ListByProject 的分页版。
func (r *sessionRepo) ListByProjectPaged(ctx context.Context, projectID int64, offset, limit int) ([]*chat_entity.Session, error) {
	return r.listIndexPaged(ctx, &projectID, offset, limit)
}

func (r *sessionRepo) CountAll(ctx context.Context) (int64, error) {
	return r.countIndex(ctx, nil)
}

func (r *sessionRepo) CountFree(ctx context.Context) (int64, error) {
	free := int64(0)
	return r.countIndex(ctx, &free)
}

func (r *sessionRepo) CountByProject(ctx context.Context, projectID int64) (int64, error) {
	return r.countIndex(ctx, &projectID)
}

// ReassignProject 见接口注释：WHERE 里只有 project_id，没有 status / purpose。
func (r *sessionRepo) ReassignProject(ctx context.Context, fromProjectID, toProjectID int64) error {
	return db.Ctx(ctx).Model(&chat_entity.Session{}).
		Where("project_id = ?", fromProjectID).
		Updates(map[string]any{
			"project_id": toProjectID,
			"updatetime": time.Now().UnixMilli(),
		}).Error
}

// CountActiveByProject 统计项目下 status=ACTIVE 且 agent_status 在指定集合内的会话数。
// project_svc.Delete 用它做守门：还有 running/waiting 会话时拒绝删项目。
// 子 agent 委派会话仍被 nonSubagentScope 排除。
func (r *sessionRepo) CountActiveByProject(ctx context.Context, projectID int64, agentStatuses []string) (int64, error) {
	q := db.Ctx(ctx).
		Model(&chat_entity.Session{}).
		Where("project_id = ? AND status = ?", projectID, consts.ACTIVE)
	if len(agentStatuses) > 0 {
		q = q.Where("agent_status IN ?", agentStatuses)
	}
	q = q.Scopes(nonSubagentScope)
	var n int64
	err := q.Count(&n).Error
	return n, err
}

// CountActive 统计 status=ACTIVE 且 agent_status 在指定集合内的会话总数(跨所有 agent/项目)。
// 退出二次确认用它判断是否还有进行中的会话:agentStatuses 传 {"running","waiting"}。
func (r *sessionRepo) CountActive(ctx context.Context, agentStatuses []string) (int64, error) {
	var n int64
	err := db.Ctx(ctx).
		Model(&chat_entity.Session{}).
		Where("status = ? AND agent_status IN ?", consts.ACTIVE, agentStatuses).
		Scopes(nonSubagentScope).
		Count(&n).Error
	return n, err
}

func (r *sessionRepo) Create(ctx context.Context, s *chat_entity.Session) error {
	now := time.Now().UnixMilli()
	if s.Createtime == 0 {
		s.Createtime = now
	}
	s.Updatetime = now
	err := db.Ctx(ctx).Create(s).Error
	s.ApplyDerivedFields()
	return err
}

func (r *sessionRepo) Update(ctx context.Context, s *chat_entity.Session) error {
	s.Updatetime = time.Now().UnixMilli()
	// Save 是整行回写:调用方通常只改了 title / agent_status / last_message_at 之类
	// 的一两个字段,却会把手上那份实体的**每一列**都写回去。凡是由专用单列更新负责的
	// 列都必须在这里 Omit,否则一份读得早的实体会把它们盖回旧值:
	//   - permission_mode / permission_mode_at_launch —— 运行中切换的模式与 spawn 快照;
	//   - exec_device_id / exec_daemon_fingerprint / exec_agent_backend_id /
	//     event_cursor(R12 / R15b)—— 轮次开始时读出的实体这四列还是零值,轮次中途
	//     UpdateExecDaemon / UpdateEventCursor 才把真值写进去,收尾时的整行回写因此
	//     会把「这条会话跑在哪台 daemon 上、钉在哪一档、消费到哪」一起抹成 0 / '' / 0,
	//     空闲的远端会话从此落在 ListRemoteExecSessions 的取材条件之外,再也进不了
	//     启动补齐;钉住的档也会被抹回未钉住,下一轮又变成重挑(决策36明确禁止)。
	//   - provider_key / model_key —— 会话级 ModelTarget 允许在轮中切换(2026-08-10
	//     决策 8 / 2026-08-11 决策 1),而收尾用的实体是轮次开始时读出的、带着旧 target
	//     的那一份,不 Omit 就会把用户刚切好的 target 冲回去。新建会话的首次写入走
	//     Create,不经这里。
	// 这几列在服务层没有任何「写实体再 Update」的路径,Omit 不会丢掉谁的写入。
	err := db.Ctx(ctx).Omit(
		"permission_mode", "permission_mode_at_launch",
		"exec_device_id", "exec_daemon_fingerprint", "exec_agent_backend_id", "event_cursor",
		"provider_key", "model_key",
	).Save(s).Error
	s.ApplyDerivedFields()
	return err
}

func (r *sessionRepo) UpdatePermissionMode(ctx context.Context, sessionID int64, mode string) error {
	return db.Ctx(ctx).Model(&chat_entity.Session{}).
		Where("id = ? AND status = ?", sessionID, consts.ACTIVE).
		Updates(map[string]any{
			"permission_mode": mode,
			"updatetime":      time.Now().UnixMilli(),
		}).Error
}

func (r *sessionRepo) UpdatePermissionModeAtLaunch(ctx context.Context, sessionID int64, mode string) error {
	return db.Ctx(ctx).Model(&chat_entity.Session{}).
		Where("id = ? AND status = ?", sessionID, consts.ACTIVE).
		Updates(map[string]any{
			"permission_mode_at_launch": mode,
			"updatetime":                time.Now().UnixMilli(),
		}).Error
}

func (r *sessionRepo) UpdateModelTarget(ctx context.Context, sessionID int64, providerKey, modelKey string) error {
	return db.Ctx(ctx).Model(&chat_entity.Session{}).
		Where("id = ? AND status = ?", sessionID, consts.ACTIVE).
		Updates(map[string]any{
			"provider_key": providerKey,
			"model_key":    modelKey,
			"updatetime":   time.Now().UnixMilli(),
		}).Error
}

func (r *sessionRepo) UpdateContextWindow(ctx context.Context, sessionID int64, tokens int) error {
	return db.Ctx(ctx).Model(&chat_entity.Session{}).
		Where("id = ? AND status = ?", sessionID, consts.ACTIVE).
		Updates(map[string]any{
			"context_window": tokens,
			"updatetime":     time.Now().UnixMilli(),
		}).Error
}

func (r *sessionRepo) UpdateExecDaemon(ctx context.Context, sessionID int64, deviceID int64, daemonFingerprint string, agentBackendID int64) error {
	return db.Ctx(ctx).Model(&chat_entity.Session{}).
		Where("id = ? AND status = ?", sessionID, consts.ACTIVE).
		Updates(map[string]any{
			"exec_device_id":          deviceID,
			"exec_daemon_fingerprint": daemonFingerprint,
			// 会话钉住的执行目标档(R15b / 决策36):与设备/实例标识同一条语句一并写,
			// 三列同生共死,不拆成两个写入点。
			"exec_agent_backend_id": agentBackendID,
			// 换了一台 daemon 实例(含改回本机的空标识)就在同一条语句里把游标归零:
			// 老游标指的是老 daemon 通知日志里的位置,留着会被下次 LoadCursor 当成对新
			// daemon 有效。SQL 的 SET 右值一律读改写前的行值,所以这里比的是老标识。
			"event_cursor": gorm.Expr(
				"CASE WHEN exec_daemon_fingerprint = ? THEN event_cursor ELSE 0 END", daemonFingerprint),
			"updatetime": time.Now().UnixMilli(),
		}).Error
}

// catchUpLimit 一次启动补齐最多认领多少条会话。补齐会为**每条**会话装一个轮次消费方、
// 加一份池连接引用、开一条自主轮监视 goroutine,而 releaseCatchUpRefs 只还得掉 daemon
// 说不在跑的那些 —— 剩下的引用要占到进程退出,开销随「历史上远端跑过的会话数」线性
// 增长。那个数如今没有上界(通知日志不再回收、会话也不过期),所以这条上限是取材唯一
// 的界,不能跟着时间窗一起去掉。
//
// 200 条远超「一台 daemon 上还可能有新内容的会话」的实际量级。收尾那一步的 SQL 参数
// 上限与它无关,由 resetIDChunk 自己分片扛住。
const catchUpLimit = 200

func (r *sessionRepo) ListRemoteExecSessions(ctx context.Context) ([]*chat_entity.Session, error) {
	var rows []*chat_entity.Session
	err := db.Ctx(ctx).
		Where("exec_device_id > ? AND exec_daemon_fingerprint <> ? AND status = ?",
			int64(0), "", consts.ACTIVE).
		// 排序决定上限砍掉谁:等判据的排最前(只有 daemon 能给它们判据,漏掉一条就是
		// 界面上一条永远转圈的会话),其余按最近活动。
		Order("CASE WHEN agent_status IN ('running','waiting') THEN 0 ELSE 1 END, updatetime DESC, id DESC").
		Limit(catchUpLimit).
		Find(&rows).Error
	applySessionDerivedFields(rows)
	return rows, err
}

func (r *sessionRepo) UpdateEventCursor(ctx context.Context, sessionID int64, daemonFingerprint string, seq int64) error {
	return db.Ctx(ctx).Model(&chat_entity.Session{}).
		Where("id = ? AND status = ? AND exec_daemon_fingerprint = ?", sessionID, consts.ACTIVE, daemonFingerprint).
		Updates(map[string]any{
			"event_cursor": seq,
			"updatetime":   time.Now().UnixMilli(),
		}).Error
}

func (r *sessionRepo) MarkRead(ctx context.Context, sessionID int64, ts int64) error {
	return db.Ctx(ctx).Model(&chat_entity.Session{}).
		Where("id = ? AND status = ? AND last_read_at < ?", sessionID, consts.ACTIVE, ts).
		Updates(map[string]any{
			"last_read_at": ts,
			"updatetime":   time.Now().UnixMilli(),
		}).Error
}

func (r *sessionRepo) ResetActiveSessions(ctx context.Context) (int64, error) {
	res := db.Ctx(ctx).Model(&chat_entity.Session{}).
		// 后半截把远端跑的会话排除在启动期清理之外,理由见接口注释。判据与
		// ListRemoteExecSessions 的取材条件互补:那边取的行,这边一行不碰。
		Where("agent_status IN ? AND status = ? AND (exec_device_id <= ? OR exec_daemon_fingerprint = ?)",
			[]string{"running", "waiting"}, consts.ACTIVE, int64(0), "").
		Updates(map[string]any{
			"agent_status": "error",
			"updatetime":   time.Now().UnixMilli(),
		})
	return res.RowsAffected, res.Error
}

// resetIDChunk 一条 IN ? 最多塞多少个 id。SQLite 的预编译参数上限最保守是 999
// (SQLITE_MAX_VARIABLE_NUMBER),SET 与其余 WHERE 还要占几个 —— 攒够会话之后整条
// 语句会被直接拒掉,补齐判定的结果一条都落不了库。
const resetIDChunk = 400

func (r *sessionRepo) ResetActiveSessionsByIDs(ctx context.Context, ids []int64) (int64, error) {
	// 空列表发出去是 WHERE id IN ()：不同方言下要么语法错、要么退化成全表更新。
	// 下面的循环对空列表天然一条 SQL 都不发。
	var affected int64
	for start := 0; start < len(ids); start += resetIDChunk {
		res := db.Ctx(ctx).Model(&chat_entity.Session{}).
			Where("agent_status IN ? AND status = ? AND id IN ?",
				[]string{"running", "waiting"}, consts.ACTIVE, ids[start:min(start+resetIDChunk, len(ids))]).
			Updates(map[string]any{
				"agent_status": "error",
				"updatetime":   time.Now().UnixMilli(),
			})
		if res.Error != nil {
			return affected, res.Error
		}
		affected += res.RowsAffected
	}
	return affected, nil
}

func (r *sessionRepo) SoftDelete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Model(&chat_entity.Session{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     consts.DELETE,
			"updatetime": time.Now().UnixMilli(),
		}).Error
}

func applySessionDerivedFields(rows []*chat_entity.Session) {
	for _, row := range rows {
		row.ApplyDerivedFields()
	}
}
