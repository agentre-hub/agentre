package chat_svc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
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

// ListPeerSessions projects every desktop-owned top-level chat session into
// the existing runtime.session.list wire shape. AgentSyncID refers to the
// account Agent record, where the caller resolves the stored name and avatar.
func (s *chatSvc) ListPeerSessions(ctx context.Context, keyword string) (*wire.SessionListResult, error) {
	fingerprint, err := desktopPeerFingerprint()
	if err != nil {
		return nil, err
	}

	agents, err := agent_repo.Agent().List(ctx)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	// 项目的同步标识一次取齐:这份清单在一次列举里不会变,而下面是「每个 Agent ×
	// 它的每条会话」两层循环,逐条回库查一次就是几百次往返。
	projectSyncIDs, err := projectSyncIDByID(ctx)
	if err != nil {
		return nil, err
	}
	result := &wire.SessionListResult{
		Sessions: make([]wire.SessionSummary, 0),
		// 桌面端就是 chat_sessions.provider_key / model_key 的落库方,如实声明。
	}
	for _, agent := range agents {
		if agent == nil {
			return nil, fmt.Errorf("%w: nil Agent", ErrPeerSessionMetadata)
		}
		// 关键词下推到查询而不是取回来再筛:后者省的只是带宽,库还是白读一遍。
		// 命中面比 wire 承诺的底线(标题)宽一格 —— 桌面端手上有 agent 名与项目名,
		// 与它自己侧栏的搜索同一口径;agentred 那一侧只有 title,所以协议只承诺 title。
		agentID := agent.ID
		sessions, err := chat_repo.Session().ListIndexPaged(ctx,
			chat_repo.SessionIndexFilter{AgentID: &agentID, Keyword: keyword}, 0, math.MaxInt)
		if err != nil {
			return nil, operationFailedWithCause(ctx, err)
		}
		for _, session := range sessions {
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
	}
	return result, nil
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

	latestSeq, detach, err := s.attachPeerTranscript(ctx, session.ID, subscriber)
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
	backendType, err := peerSessionBackendType(ctx, session, agent)
	if err != nil {
		return wire.SessionSummary{}, err
	}
	lifecycle, waiting, err := peerSessionLifecycle(session)
	if err != nil {
		return wire.SessionSummary{}, err
	}
	return wire.SessionSummary{
		ConversationID:    peerConversationID(fingerprint, session.ID),
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
		return wire.SessionLifecycleInterrupted, false, nil
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
// 正向是纯函数:本机自己发起的对话按 (本机设备指纹, 本地会话 id) 确定性派生 ——
// 与日后迁移回填对同一批行算出的值逐位相同。
//
// 反向没有反解法(派生是单向哈希),所以靠一张进程级备忘录:正向算过一次就记下,
// 没记过就枚举一遍本机会话把它补齐。**这层枚举是过渡形态**:conversation_id 落库
// 并建唯一索引之后,它由一次索引查询取代。备忘录是纯函数的缓存(键含指纹),条目
// 只增不改,因此并发安全也不需要失效。
var (
	peerConversationIndex  sync.Map // conversation_id(string) → chat_sessions.id(int64)
	peerConversationBySess sync.Map // chat_sessions.id(int64) → conversation_id(string)
)

// peerConversationID 交出这条本机会话的对话身份,并登记双向映射。
func peerConversationID(fingerprint string, sessionID int64) string {
	if sessionID <= 0 {
		return ""
	}
	id := conversationid.Derive(conversationid.Namespace, fingerprint, strconv.FormatInt(sessionID, 10))
	rememberPeerConversation(id, sessionID)
	return id
}

// rememberPeerConversation 记下一条对话与本机会话行的对应关系。
//
// 除了正向派生,它还是 R17 那条路的登记点:浏览器把新对话派到这台桌面端上跑时,
// 号是浏览器铸的 v7 —— 派生不出来,只能记下来。
func rememberPeerConversation(conversationID string, sessionID int64) {
	if conversationID == "" || sessionID <= 0 {
		return
	}
	peerConversationIndex.Store(conversationID, sessionID)
	peerConversationBySess.Store(sessionID, conversationID)
}

// peerConversationIDOf 交出这条本机会话在线上的身份。登记过的直接取(推送热路径
// 上的每一帧都要它);没登记过的现取一次本机指纹派生 —— 那只发生在从没被列举 /
// attach 过的会话上。
func peerConversationIDOf(sessionID int64) string {
	if id, ok := peerConversationBySess.Load(sessionID); ok {
		return id.(string)
	}
	fingerprint, err := desktopPeerFingerprint()
	if err != nil {
		return ""
	}
	return peerConversationID(fingerprint, sessionID)
}

// ResolvePeerConversation 把线上的 conversation_id 翻回本机 chat_sessions.id。
//
// 非法取值(空、旧的裸数字会话号、畸形 uuid)在这里就被挡下并给出明确错误 ——
// 它是 RPC 边界上「这不是一条对话身份」与「这条对话不在本机」的分界。
func ResolvePeerConversation(ctx context.Context, conversationID string) (int64, error) {
	if err := conversationid.Validate(conversationID); err != nil {
		return 0, fmt.Errorf("%w: %s", ErrPeerSessionInvalidID, err)
	}
	if sid, ok := peerConversationIndex.Load(conversationID); ok {
		return sid.(int64), nil
	}
	if err := reindexPeerConversations(ctx); err != nil {
		return 0, err
	}
	if sid, ok := peerConversationIndex.Load(conversationID); ok {
		return sid.(int64), nil
	}
	return 0, ErrPeerSessionNotFound
}

// reindexPeerConversations 枚举本机每条会话并把它们的对话身份铸进备忘录。
//
// 走会话索引这一条查询(与 ListPeerSessions 同一个可见性口径),而不是再开一条只取
// id 的路:这层枚举本来就是过渡形态,conversation_id 落库并建唯一索引之后它整个消失,
// 不值得为它新增一个仓储方法。
func reindexPeerConversations(ctx context.Context) error {
	fingerprint, err := desktopPeerFingerprint()
	if err != nil {
		return err
	}
	rows, err := chat_repo.Session().ListIndexPaged(ctx, chat_repo.SessionIndexFilter{}, 0, math.MaxInt)
	if err != nil {
		return operationFailedWithCause(ctx, err)
	}
	for _, row := range rows {
		if row != nil {
			peerConversationID(fingerprint, row.ID)
		}
	}
	return nil
}

// desktopPeerFingerprint 取本机设备指纹 —— 对话身份派生的第一个输入,也是这台桌面端
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
