package chat_svc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

var (
	// ErrPeerSessionNotFound distinguishes an unknown desktop session from an
	// authorized remote peer that has merely not attached it yet.
	ErrPeerSessionNotFound = errors.New("desktop peer session not found")
	// ErrPeerSessionMetadata means a corrupt local row cannot safely be exposed
	// as a round-A-style unnamed fallback.
	ErrPeerSessionMetadata = errors.New("desktop peer session is missing required metadata")
	// ErrPeerSessionInvalidID 线上给来的不是一条合法的 conversation_id(空、旧的裸
	// 数字会话号、畸形 uuid)。与"这条对话不在本机"分开:前者是调用方参数错了,
	// 在 RPC 边界上要给出一个能分辨的错误码。
	ErrPeerSessionInvalidID = errors.New("invalid conversation id")
)

// PeerSessionSubscriber is the remote-notification sink registered by an
// attached account peer. Task 3 only owns its lifecycle; task 4 publishes the
// canonical session events to these subscribers.
type PeerSessionSubscriber interface {
	Notify(method string, params any) error
	Done() <-chan struct{}
}

// PeerSessionSubscriberKeyer supplies a stable connection identity across the
// attach and pull RPC calls. Subscribers without it fall back to pointer
// identity for unit-test and in-process callers.
type PeerSessionSubscriberKeyer interface {
	PeerSessionSubscriberKey() string
}

// ListPeerSessions projects one page of desktop-owned top-level chat sessions
// into the existing runtime.session.list wire shape. AgentSyncID refers to the
// account Agent record, where the caller resolves the stored name and avatar.
//
// 一条查询、按最近活动倒序 —— 而不是「每个 Agent 各查一遍再拼起来」。分页只在一条
// 全局有序的清单上说得通:按 Agent 分段拼出来的顺序里,「第 21 条」根本不是一个位置。
func (s *chatSvc) ListPeerSessions(ctx context.Context, params wire.SessionListParams) (*wire.SessionListResult, error) {
	fingerprint, err := desktopPeerFingerprint()
	if err != nil {
		return nil, err
	}
	offset, err := wire.DecodeSessionListCursor(params.Cursor)
	if err != nil {
		// 坏游标是调用方参数错了。默默从头开始会让翻到一半的调用方重新收到前几条,
		// 在它那边表现为「会话被复制了」。
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	limit := wire.ClampSessionListLimit(params.Limit)
	if len(params.ConversationIDs) > wire.SessionListMaxIDs {
		// 截断会让少给的那条在调用方那里读起来是「这条对话不在这台机器上了」。
		// 分批是调用方的事,它手上才有那份名单。
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}

	agents, err := agent_repo.Agent().List(ctx)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	agentByID := make(map[int64]*agent_entity.Agent, len(agents))
	for _, agent := range agents {
		if agent == nil {
			return nil, fmt.Errorf("%w: nil Agent", ErrPeerSessionMetadata)
		}
		agentByID[agent.ID] = agent
	}
	// 项目的同步标识一次取齐:这份清单在一次列举里不会变,而下面要逐条会话问它,
	// 逐条回库查一次就是几百次往返。
	projectSyncIDs, err := projectSyncIDByID(ctx)
	if err != nil {
		return nil, err
	}

	// 关键词下推到查询而不是取回来再筛:后者省的只是带宽,库还是白读一遍。
	// 命中面比 wire 承诺的底线(标题)宽一格 —— 桌面端手上有 agent 名与项目名,
	// 与它自己侧栏的搜索同一口径;agentred 那一侧只有 title,所以协议只承诺 title。
	filter := chat_repo.SessionIndexFilter{
		Keyword:         params.Keyword,
		ConversationIDs: params.ConversationIDs,
	}
	// limit<=0 是协议里「不分页」那一档(老客户端不带 limit):照旧整份交出去。
	pageSize := limit
	if pageSize <= 0 {
		pageSize = math.MaxInt
	}
	sessions, err := chat_repo.Session().ListIndexPaged(ctx, filter, offset, pageSize)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	total := int64(offset + len(sessions))
	if limit > 0 {
		// 分页之后清单只剩一页,而调用方要写「查看全部 N」—— N 只有 COUNT 数得出。
		// 不分页时整份就在手里,再数一遍库是白读。
		total, err = chat_repo.Session().CountIndex(ctx, filter)
		if err != nil {
			return nil, operationFailedWithCause(ctx, err)
		}
	}

	result := &wire.SessionListResult{
		Sessions: make([]wire.SessionSummary, 0, len(sessions)),
		Total:    total,
		// 桌面端就是 chat_sessions.provider_key / model_key 的落库方,如实声明。
	}
	for _, session := range sessions {
		agent := agentByID[session.AgentID]
		if agent == nil {
			// Agent 档已经不在了。跳过而不是报错 —— 与下面那一行缺元数据同一条纪律。
			logger.Ctx(ctx).Warn("chat_svc.ListPeerSessions: skipping session of an unknown agent",
				zap.Int64("sessionId", sessionID(session)), zap.Int64("agentId", session.AgentID))
			continue
		}
		summary, err := peerSessionSummary(ctx, session, agent, fingerprint, projectSyncIDs)
		if err != nil {
			// 一行缺元数据只影响这一行（R5 仍然成立：跳过它，绝不补一个编出来的
			// 摘要）。整份清单不能跟着完蛋——它是 web 控制台进入这台机器的唯一入口，
			// 这里报错，浏览器就只剩一个不会结束的「加载中」。
			if errors.Is(err, ErrPeerSessionMetadata) {
				logger.Ctx(ctx).Warn("chat_svc.ListPeerSessions: skipping unusable session row",
					zap.Int64("sessionId", sessionID(session)),
					zap.Int64("agentId", agent.ID), zap.Error(err))
				continue
			}
			return nil, err
		}
		result.Sessions = append(result.Sessions, summary)
	}
	// 游标按**读到的行数**推进而不是按交出去的条数:上面跳过的行仍然占着库里的一个
	// 位置,按条数推进会让下一页把它后面那条重新读一遍。
	if limit > 0 && int64(offset+len(sessions)) < total {
		result.HasMore = true
		result.Cursor = wire.EncodeSessionListCursor(offset + len(sessions))
	}
	return result, nil
}

// CountPeerSessions 交出这台桌面端此刻的三个数:一共多少条、在跑几条、几条在等用户。
//
// 三条 COUNT,一条摘要都不投影 —— 调用方(web 控制台的设备卡片)要的从来不是清单,
// 而拿清单去数这三个数,就得先把整台机器的摘要搬过线。
//
// 「在等你」在桌面端是 agent_status 的一个取值(waiting),不像 daemon 那边要现问
// backend:这一端的等待态本来就落在会话行上。
func (s *chatSvc) CountPeerSessions(ctx context.Context) (*wire.SessionCountsResult, error) {
	total, err := chat_repo.Session().CountIndex(ctx, chat_repo.SessionIndexFilter{})
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	// 状态词表见 chat_entity.allowedAgentStatuses;running / waiting 两档在会话行上。
	running, err := chat_repo.Session().CountActive(ctx, []string{"running"})
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	waiting, err := chat_repo.Session().CountActive(ctx, []string{"waiting"})
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	return &wire.SessionCountsResult{Total: total, Running: running, Waiting: waiting}, nil
}

// AttachPeerSession registers one remote consumer without changing the Wails
// emitter used by the local desktop UI. The registration follows the RPC
// connection's Done signal, so a disconnected peer can never remain present.
func (s *chatSvc) AttachPeerSession(ctx context.Context, params wire.SessionAttachParams, subscriber PeerSessionSubscriber) (wire.SessionAttachResult, error) {
	if subscriber == nil {
		return wire.SessionAttachResult{}, fmt.Errorf("%w: attach parameters", ErrPeerSessionNotFound)
	}
	sessionID, err := ResolvePeerConversation(ctx, params.ConversationID)
	if err != nil {
		return wire.SessionAttachResult{}, err
	}
	session, err := chat_repo.Session().Find(ctx, sessionID)
	if err != nil {
		return wire.SessionAttachResult{}, operationFailedWithCause(ctx, err)
	}
	if session == nil {
		return wire.SessionAttachResult{}, ErrPeerSessionNotFound
	}
	agent, err := agent_repo.Agent().Find(ctx, session.AgentID)
	if err != nil {
		return wire.SessionAttachResult{}, operationFailedWithCause(ctx, err)
	}
	if agent == nil {
		return wire.SessionAttachResult{}, fmt.Errorf("%w: agent %d", ErrPeerSessionNotFound, session.AgentID)
	}
	backendType, err := peerSessionBackendType(ctx, session, agent)
	if err != nil {
		return wire.SessionAttachResult{}, err
	}
	lifecycle, _, err := peerSessionLifecycle(session)
	if err != nil {
		return wire.SessionAttachResult{}, err
	}

	latestSeq, detach, err := s.attachPeerTranscript(ctx, session.ID, session.ConversationID, subscriber)
	if err != nil {
		return wire.SessionAttachResult{}, err
	}
	go func() {
		<-subscriber.Done()
		detach()
	}()
	return wire.SessionAttachResult{
		ConversationID: params.ConversationID,
		BackendType:    backendType,
		LifecycleState: lifecycle,
		LatestSeq:      latestSeq,
	}, nil
}

// projectSyncIDByID 是「本地项目主键 → 账号级同步标识」的查询表。
//
// 还没认领同步标识的项目(未登录期间建的行,R12a 之前)不进表:交出去的必须是账号
// 认得的那个名字,拿本地主键凑一个只会在账号那边建出一个配不上真项目的组。
func projectSyncIDByID(ctx context.Context) (map[int64]string, error) {
	projects, err := project_repo.Project().List(ctx)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	out := make(map[int64]string, len(projects))
	for _, p := range projects {
		if p != nil && p.SyncID != "" {
			out[p.ID] = p.SyncID
		}
	}
	return out, nil
}

func peerSessionSummary(
	ctx context.Context, session *chat_entity.Session, agent *agent_entity.Agent,
	fingerprint string, projectSyncIDs map[int64]string,
) (wire.SessionSummary, error) {
	if session == nil || agent == nil || strings.TrimSpace(session.Title) == "" || strings.TrimSpace(agent.Name) == "" || agent.SyncID == "" {
		return wire.SessionSummary{}, fmt.Errorf("%w: session %d", ErrPeerSessionMetadata, sessionID(session))
	}
	// 没有对话身份的行寻址不到:对端拿它去 attach / pull 只会得到「这条对话不在
	// 本机」。列一条点不开的会话比不列它更糟,按缺元数据跳过(与 R5 同一条口径)。
	// 迁移之后这不该发生 —— 每一行建档时就有身份。
	if session.ConversationID == "" {
		return wire.SessionSummary{}, fmt.Errorf("%w: session %d has no conversation id", ErrPeerSessionMetadata, session.ID)
	}
	backendType, err := peerSessionBackendType(ctx, session, agent)
	if err != nil {
		return wire.SessionSummary{}, err
	}
	lifecycle, waiting, err := peerSessionLifecycle(session)
	if err != nil {
		return wire.SessionSummary{}, err
	}
	return wire.SessionSummary{
		ConversationID:    session.ConversationID,
		PeerFingerprint:   fingerprint,
		AgentID:           agent.ID,
		Title:             session.Title,
		AgentSyncID:       agent.SyncID,
		ProviderSessionID: session.ProviderSessionID,
		// 自由会话(ProjectID = 0)与还没认领同步标识的项目都留空,不猜。
		ProjectSyncID:   projectSyncIDs[session.ProjectID],
		BackendType:     backendType,
		LifecycleState:  lifecycle,
		WaitingForInput: waiting,
		LatestSeq:       0,
		LastMessageAt:   session.LastMessageAt,
		// 会话级 ModelTarget 原样交出:这两列本来就是桌面端在写的(决策 2/3 与
		// SetChatSessionModelTarget),浏览器此前只是读不到。空是有含义的值
		// (跟随 Agent 绑定),不补默认、不猜。
		ProviderKey: session.ProviderKey,
		ModelKey:    session.ModelKey,
		// 会话级思考力度同一形态的显示镜像（spec 2026-09-01 决策 1）：空表示跟随该
		// 会话那一档 backend 的配置，同样不补默认、不猜。
		ReasoningEffort: session.ReasoningEffort,
	}, nil
}

func peerSessionBackendType(ctx context.Context, session *chat_entity.Session, agent *agent_entity.Agent) (string, error) {
	backendID := agent.AgentBackendID
	if session.ExecAgentBackendID != 0 {
		backendID = session.ExecAgentBackendID
	}
	if backendID == 0 {
		return "", nil
	}
	backend, err := agent_backend_repo.AgentBackend().Find(ctx, backendID)
	if err != nil {
		return "", operationFailedWithCause(ctx, err)
	}
	if backend == nil {
		return "", nil
	}
	return backend.Type, nil
}

func peerSessionLifecycle(session *chat_entity.Session) (lifecycle string, waiting bool, err error) {
	switch session.AgentStatus {
	case "running":
		return wire.SessionLifecycleRunning, false, nil
	case "waiting":
		return wire.SessionLifecycleRunning, true, nil
	case "idle":
		return wire.SessionLifecycleIdle, false, nil
	case "error":
		// failed 而不是 interrupted:interrupted 是自锁终态(消费方对它一律不
		// attach),拿它表达「上一轮跑挂了」会让一条报错收场的对话再也接不上实时流。
		// 见 wire 那一族常量上的说明。
		return wire.SessionLifecycleFailed, false, nil
	default:
		return "", false, fmt.Errorf("%w: session %d status %q", ErrPeerSessionMetadata, sessionID(session), session.AgentStatus)
	}
}

func sessionID(session *chat_entity.Session) int64 {
	if session == nil {
		return 0
	}
	return session.ID
}

func (s *chatSvc) peerSubscriberCount(sessionID int64) int {
	value, ok := s.peerPublications.Load(sessionID)
	if !ok {
		return 0
	}
	publication := value.(*peerSessionPublication)
	publication.mu.Lock()
	defer publication.mu.Unlock()
	return len(publication.subscribers)
}

// ── 对话身份 ↔ 本地会话主键 ─────────────────────────────────────────────────
//
// 线上寻址的是 conversation_id;本机的主键仍是 chat_sessions.id(决策 12:两件事,
// 不合并)。桌面端因此永久存在一层翻译,这里是它的**唯一**一处。
//
// 反向(conversation_id → 本地主键)是 chat_sessions.conversation_id 唯一索引上的
// 一次查询,只在 RPC 边界上做一次;正向不再需要现场翻译 —— 对端通知宇宙
// (peerSessionPublication)建立时就把这条对话的身份钉在自己身上,每一帧直接盖它。
//
// 这一层从前是「按 (本机指纹, 本地会话 id) 现场派生 + 枚举本机会话补齐备忘录」,
// 那是 conversation_id 落库之前的过渡形态。新对话的号是**铸**出来的(UUIDv7),
// 派生算出的是另一个值 —— 落列之后必须一律以库里那一列为准,不能再算。

// ResolvePeerConversation 把线上的 conversation_id 翻回本机 chat_sessions.id。
//
// 非法取值(空、旧的裸数字会话号、畸形 uuid)在碰库之前就被挡下并给出明确错误 ——
// 它是 RPC 边界上「这不是一条对话身份」与「这条对话不在本机」的分界。
func ResolvePeerConversation(ctx context.Context, conversationID string) (int64, error) {
	if err := conversationid.Validate(conversationID); err != nil {
		return 0, fmt.Errorf("%w: %s", ErrPeerSessionInvalidID, err)
	}
	session, err := chat_repo.Session().FindByConversationID(ctx, conversationID)
	if err != nil {
		return 0, operationFailedWithCause(ctx, err)
	}
	if session == nil {
		return 0, ErrPeerSessionNotFound
	}
	return session.ID, nil
}

// desktopPeerFingerprint 取本机设备指纹 —— 会话摘要上盖的来源标注,也是这台桌面端
// 向对端出示的那个值(R5 决策 8:账号侧不得另生成指纹)。
func desktopPeerFingerprint() (string, error) {
	device := remote_device_svc.Default()
	if device == nil {
		return "", fmt.Errorf("desktop peer fingerprint: remote device service unavailable")
	}
	fingerprint, err := device.DeviceFingerprint()
	if err != nil {
		return "", fmt.Errorf("desktop peer fingerprint: %w", err)
	}
	if fingerprint == "" {
		return "", fmt.Errorf("%w: desktop fingerprint", ErrPeerSessionMetadata)
	}
	return fingerprint, nil
}
