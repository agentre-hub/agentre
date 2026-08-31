package chat_svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
	chatblocks "github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
)

// ErrPeerExecutionUnavailable is deliberately narrower than a generic remote
// dial failure: the desktop remains present and its persisted transcript can
// still be read, but the agentred pinned to this session cannot execute a new
// turn.
var ErrPeerExecutionUnavailable = errors.New("desktop history remains available, but the session execution target is unavailable")

// ErrPeerAgentNotFound 是 R17 建会话时对端点的 agentSyncId 在本机找不到（同步还没
// 落地 / 该 Agent 已删除）——不静默落到别的 Agent 上，也不建一条幽灵会话。
var ErrPeerAgentNotFound = errors.New("desktop peer agent not found")

// ErrPeerProjectNotFound 是 R17 建会话时对端点的 cwd 在本机找不到对应项目（项目刚被
// 删/改路径，与上报时不一致）——拒绝而不是静默把会话跑进一个错误的目录。
var ErrPeerProjectNotFound = errors.New("desktop peer project not found")

// peerMessageSource is source metadata carried by an account peer. It is
// persisted inside the existing text StoredBlock so it survives transcript
// reload without changing the chat_messages schema.
type peerMessageSource struct {
	Device string
	Name   string
}

// PeerSessionSource is the account-authorized caller identity captured by the
// relay connection. The request cannot nominate a different fingerprint.
type PeerSessionSource struct {
	Device string
	Name   string
}

func (s PeerSessionSource) messageSource() peerMessageSource {
	return peerMessageSource(s)
}

// PeerSessionControlResult is returned by inbound decision handlers. A second
// winner is a normal, typed outcome rather than a transport failure.
type PeerSessionControlResult struct {
	AlreadyHandled bool `json:"alreadyHandled,omitempty"`
}

// PeerSessionRunResult describes a rejected write while keeping the desktop
// transcript readable. The peer adapter serializes it in typed RPC error data.
type PeerSessionRunResult struct {
	Accepted             bool `json:"accepted"`
	HistoryAvailable     bool `json:"historyAvailable"`
	ExecutionUnavailable bool `json:"executionUnavailable"`
}

// RunPeerSession adapts the existing runtime.run wire request into the
// desktop's session-level Send path. Backend, queue, permission, and MCP
// selection remain entirely owned by Send; only the authenticated source is
// added to the persisted user row.
//
// 对端点到的会话在本机不存在时，这是 R17 的「浏览器把新对话派到这台桌面端上」：
// 会话行/标题/转录都在这台机器上新建并跑首轮（见 runFreshPeerSession）。会话存在时
// 原样续轮，行为不变。
func (s *chatSvc) RunPeerSession(ctx context.Context, params wire.RunParams, source PeerSessionSource) (*SendResponse, error) {
	if source.Device == "" {
		return nil, fmt.Errorf("invalid peer session run")
	}
	if err := conversationid.Validate(params.ConversationID); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrPeerSessionInvalidID, err)
	}
	sessionID, err := ResolvePeerConversation(ctx, params.ConversationID)
	if err != nil && !errors.Is(err, ErrPeerSessionNotFound) {
		return nil, err
	}
	// 解析不出来的是对端**新铸**的对话(R17:浏览器把新对话派到这台桌面端上跑)。
	// 新建的会话行与对端铸的号在这里对上,此后这条对话双向都寻址得到。
	if sessionID == 0 {
		return s.runFreshPeerSession(ctx, params, source)
	}
	session, err := chat_repo.Session().Find(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return s.runFreshPeerSession(ctx, params, source)
	}
	_, backend, _, err := s.resolveAgentBackend(ctx, session, session.AgentID, session.ProjectID)
	if err != nil {
		return nil, err
	}
	if beTargetsRemote(backend) {
		if err := s.preflightPeerRemoteExecution(ctx, backend, session.ID); err != nil {
			return nil, err
		}
	}
	return s.send(ctx, &SendRequest{
		SessionID:             session.ID,
		Text:                  params.UserText,
		PermissionMode:        params.PermissionMode,
		EmitTurnStartedBypass: true,
		peerSource:            source.messageSource(),
	}, sendOptions{})
}

// runFreshPeerSession 在桌面端上为远端对端新建一条会话并跑首轮（R17）。对端只携带
// 账号级 agentSyncId 与该项目在本机的 cwd，本机据此解析本地 agent / project 行，然后
// 走与桌面端自己发消息完全相同的 Send 路径（排队、权限模式、转录落库都发生）——
// 会话行、标题与转录因此都住在这台机器上，返回的也是本机的真实会话 id。
func (s *chatSvc) runFreshPeerSession(ctx context.Context, params wire.RunParams, source PeerSessionSource) (*SendResponse, error) {
	if strings.TrimSpace(params.AgentSyncID) == "" {
		return nil, fmt.Errorf("invalid fresh peer session run: agentSyncId is required")
	}
	agentID, err := syncstate_repo.SyncState().FindLocalID(ctx, syncwire.KindAgent, params.AgentSyncID)
	if err != nil {
		return nil, err
	}
	if agentID <= 0 {
		return nil, fmt.Errorf("%w: agent sync id %q", ErrPeerAgentNotFound, params.AgentSyncID)
	}
	projectID, err := resolvePeerProjectID(ctx, strings.TrimSpace(params.Cwd))
	if err != nil {
		return nil, err
	}
	out, err := s.send(ctx, &SendRequest{
		AgentID:               agentID,
		ProjectID:             projectID,
		Text:                  params.UserText,
		PermissionMode:        params.PermissionMode,
		ProviderKey:           params.LLMProviderKey,
		ModelKey:              params.LLMModelKey,
		EmitTurnStartedBypass: true,
		peerSource:            source.messageSource(),
	}, sendOptions{})
	if err != nil {
		return nil, err
	}
	// 号是对端铸的(v5/v7 都可能),本机派生不出来 —— 只能在建行的这一刻记下对应关系,
	// 此后这条对话的 attach / pull / 控制请求才寻址得到本机这一行。
	if out != nil {
		rememberPeerConversation(params.ConversationID, out.SessionID)
	}
	return out, nil
}

// resolvePeerProjectID 把对端报告的 cwd（该桌面端自己上报过的本机项目路径）翻回本地
// project 行：会话要钉到用户挑的那个项目上，转录与后续轮次才带正确的项目上下文。
// cwd 为空（未挑项目的自由会话）返回 0；路径对不上本机任何已配置项目（项目刚被
// 删/改路径，与上报时不一致）时拒绝——把会话静默跑进一个错误的目录比报错更糟。
func resolvePeerProjectID(ctx context.Context, cwd string) (int64, error) {
	if cwd == "" {
		return 0, nil
	}
	rows, err := project_repo.Project().List(ctx)
	if err != nil {
		return 0, err
	}
	for _, p := range rows {
		if p != nil && p.IsActive() && !p.LocalPathMissing && strings.TrimSpace(p.Path) == cwd {
			return p.ID, nil
		}
	}
	return 0, fmt.Errorf("%w: project cwd %q", ErrPeerProjectNotFound, cwd)
}

func (s *chatSvc) preflightPeerRemoteExecution(ctx context.Context, backend *agent_backend_entity.AgentBackend, sessionID int64) error {
	_, err := s.selectRunner(ctx, backend, sessionID)
	if err == nil {
		if deviceID, ok := localPairedDeviceID(ctx, backend.DeviceFingerprint); ok {
			s.releaseRemoteRuntime(deviceID, sessionID)
		}
		return nil
	}
	var httpErr *httputils.Error
	if errors.As(err, &httpErr) && (httpErr.Code == code.RemoteRunnerDialFailed || httpErr.Code == code.AgentBackendInvalidDevice) {
		return fmt.Errorf("%w: %v", ErrPeerExecutionUnavailable, err)
	}
	return err
}

// EnqueuePeerSession uses the existing steer queue; source metadata is carried
// to the normal consumed-steer persistence path rather than creating a second
// queue for remote peers.
func (s *chatSvc) EnqueuePeerSession(ctx context.Context, params wire.SteerParams, source PeerSessionSource) (*EnqueueResponse, error) {
	sessionID, err := ResolvePeerConversation(ctx, params.ConversationID)
	if err != nil {
		return nil, err
	}
	return s.enqueue(ctx, &EnqueueRequest{SessionID: sessionID, Text: params.Text, peerSource: source.messageSource()})
}

// PendingPeerSessionWaiters 是 AnswerPeerToolPermission / AnswerPeerUserQuestion 这两个
// 写侧的读侧（与 agentred 的 SessionCatchupHandlers.PendingWaiters 同一个方法、同一份
// 载荷形状）。浏览器不订阅桌面端的 Wails 事件，它画审批卡 / 提问卡的数据源就是这份
// 快照 —— 桌面端答不了它，托管在这台机器上的会话在浏览器上就永远没有卡可批。
//
// 快照来自 backend runtime 的进程内存而不是数据库：会话行只用来解「这条会话跑在哪个
// backend 上」。答案随后由写侧用同一个会话键投回同一个 runner，读写两侧因此永远对得上。
func (s *chatSvc) PendingPeerSessionWaiters(
	ctx context.Context, params wire.SessionPendingWaitersParams,
) (wire.SessionPendingWaitersResult, error) {
	sessionID, err := ResolvePeerConversation(ctx, params.ConversationID)
	if err != nil {
		return wire.SessionPendingWaitersResult{}, err
	}
	session, err := chat_repo.Session().Find(ctx, sessionID)
	if err != nil {
		return wire.SessionPendingWaitersResult{}, operationFailedWithCause(ctx, err)
	}
	if session == nil {
		return wire.SessionPendingWaitersResult{}, ErrPeerSessionNotFound
	}
	backend, err := s.peerSessionExecBackend(ctx, session)
	if err != nil {
		return wire.SessionPendingWaitersResult{}, err
	}
	// 这一轮跑在另一台机器上:waiter 住在那台 agentred 的进程内存里，取它是一次会失败
	// 的 RPC，因此走一条**带错误返回**的独立路径而不是塞进 WaiterLister 的无错形状
	// （理由见 remote.Runtime.PendingWaiters）。
	if beTargetsRemote(backend) {
		return s.remotePendingSessionWaiters(ctx, backend, session.ID)
	}
	lister := localWaiterLister(backend)
	if lister == nil {
		return wire.SessionPendingWaitersResult{}, nil
	}
	snapshot := lister.PendingWaiters(ctx, session.ID)
	return wire.SessionPendingWaitersResult{
		ToolPermissions:  snapshot.ToolPermissions,
		AskUserQuestions: snapshot.AskUserQuestions,
	}, nil
}

// remotePendingSessionWaiters 只读地问「那台 agentred 上这条会话此刻卡在哪些决策上」。
//
// 只看本机**已经在跑的**那条连接（cachedRemoteRuntime），不为这次查询借新的：没有在跑
// 的连接就意味着本机没有在那台设备上开着的轮次，那边也没有本机要照看的待决策；而借一条
// 会顺带拨号、占住池引用并落一次库（recordExecDaemon）——「浏览器查一眼待决策」不该改
// 会话的执行归属。
//
// 查询失败如实上报：降级成空快照等于告诉浏览器「没有待决策」，而远端 agent 正阻塞着等
// 答复 —— 一条「看起来空闲、实际卡在审批」的会话比一次可见的加载失败糟得多。
func (s *chatSvc) remotePendingSessionWaiters(
	ctx context.Context, backend *agent_backend_entity.AgentBackend, sessionID int64,
) (wire.SessionPendingWaitersResult, error) {
	deviceID, ok := localPairedDeviceID(ctx, backend.DeviceFingerprint)
	if !ok {
		logger.Ctx(ctx).Warn("chat_svc.remotePendingSessionWaiters: exec device is not paired here",
			zap.Int64("sessionId", sessionID), zap.String("deviceId", backend.DeviceFingerprint))
		return wire.SessionPendingWaitersResult{}, nil
	}
	rt := s.cachedRemoteRuntime(deviceID)
	if rt == nil {
		logger.Ctx(ctx).Debug("chat_svc.remotePendingSessionWaiters: no live connection to that device",
			zap.Int64("sessionId", sessionID), zap.Int64("deviceId", deviceID))
		return wire.SessionPendingWaitersResult{}, nil
	}
	result, err := rt.PendingWaiters(ctx, sessionID)
	if err != nil {
		return wire.SessionPendingWaitersResult{}, operationFailedWithCause(ctx, err,
			zap.Int64("sessionId", sessionID), zap.Int64("deviceId", deviceID))
	}
	return result, nil
}

// localWaiterLister 解出本机 runtime 注册表里那一档的 waiter 读侧；没有读侧时回 nil。
//
// 没有审批协议的 backend 不实现 WaiterLister：R7 明写这一支回空列表而不是报错。
func localWaiterLister(backend *agent_backend_entity.AgentBackend) agentruntime.WaiterLister {
	runner := agentruntime.RuntimeFor(agent_backend_entity.BackendType(backend.Type))
	if runner == nil {
		return nil
	}
	lister, _ := runner.(agentruntime.WaiterLister)
	return lister
}

// peerSessionExecBackend 解出这条会话此刻实际执行所在的那一档 backend。
//
// 解析与写侧 AnswerToolPermission 逐字一致（会话钉住哪一档就用哪一档）：读出来的
// waiter 与提交答案的目标必须是同一个 runner，否则浏览器会照着一份别处的 requestID
// 去答一个这里没有的问题。
func (s *chatSvc) peerSessionExecBackend(
	ctx context.Context, session *chat_entity.Session,
) (*agent_backend_entity.AgentBackend, error) {
	agent, err := agent_repo.Agent().Find(ctx, session.AgentID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if agent == nil {
		return nil, fmt.Errorf("%w: agent %d", ErrPeerSessionNotFound, session.AgentID)
	}
	backendID := agent.AgentBackendID
	if session.ExecAgentBackendID > 0 {
		backendID = session.ExecAgentBackendID
	}
	if backendID <= 0 {
		return nil, fmt.Errorf("%w: session %d has no agent backend", ErrPeerSessionMetadata, session.ID)
	}
	backend, err := agent_backend_repo.AgentBackend().Find(ctx, backendID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if backend == nil {
		return nil, fmt.Errorf("%w: agent backend %d", ErrPeerSessionMetadata, backendID)
	}
	return backend, nil
}

func (s *chatSvc) AnswerPeerUserQuestion(ctx context.Context, params wire.SubmitAnswerParams) (PeerSessionControlResult, error) {
	sessionID, err := ResolvePeerConversation(ctx, params.ConversationID)
	if err != nil {
		return PeerSessionControlResult{}, err
	}
	_, err = s.AnswerUserQuestion(ctx, &AnswerUserQuestionRequest{
		SessionID: sessionID, RequestID: params.RequestID,
		Answers: chatblocks.AnswersFromRuntime(params.Answers), Skipped: params.Skipped,
	})
	return peerSessionControlResult(err)
}

func (s *chatSvc) AnswerPeerToolPermission(ctx context.Context, params wire.SubmitToolPermissionParams) (PeerSessionControlResult, error) {
	sessionID, err := ResolvePeerConversation(ctx, params.ConversationID)
	if err != nil {
		return PeerSessionControlResult{}, err
	}
	_, err = s.AnswerToolPermission(ctx, &AnswerToolPermissionRequest{
		SessionID: sessionID, RequestID: params.RequestID, Allow: params.Allow,
		AlwaysAllowSession: params.AlwaysAllowSession, DenyReason: params.DenyReason,
	})
	return peerSessionControlResult(err)
}

func peerSessionControlResult(err error) (PeerSessionControlResult, error) {
	if errors.Is(err, agentruntime.ErrWaiterNotFound) || errors.Is(err, agentruntime.ErrNoActiveTurn) {
		return PeerSessionControlResult{AlreadyHandled: true}, nil
	}
	return PeerSessionControlResult{}, err
}

// PeerSessionExecutionResult maps the one write-only availability failure to
// the typed RPC payload consumed by the inbound adapter.
func PeerSessionExecutionResult(err error) (PeerSessionRunResult, error) {
	if errors.Is(err, ErrPeerExecutionUnavailable) {
		return PeerSessionRunResult{
			HistoryAvailable: true, ExecutionUnavailable: true,
		}, nil
	}
	return PeerSessionRunResult{}, err
}

func persistPeerMessageSource(message *chat_entity.Message, source peerMessageSource) error {
	if message == nil || source.Device == "" {
		return nil
	}
	var stored []cagoblocks.StoredBlock
	if err := json.Unmarshal([]byte(message.BlocksJSON), &stored); err != nil {
		return fmt.Errorf("decode user message source: %w", err)
	}
	for index := range stored {
		if stored[index].Type != "text" && stored[index].Type != "display_text" {
			continue
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(stored[index].Data, &data); err != nil {
			return fmt.Errorf("decode user text source: %w", err)
		}
		device, _ := json.Marshal(source.Device)
		data["sourceDevice"] = device
		if source.Name != "" {
			name, _ := json.Marshal(source.Name)
			data["sourceDeviceName"] = name
		}
		encoded, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("encode user text source: %w", err)
		}
		stored[index].Data = encoded
		all, err := json.Marshal(stored)
		if err != nil {
			return fmt.Errorf("encode user message source: %w", err)
		}
		message.BlocksJSON = string(all)
		return nil
	}
	return nil
}

func (s *chatSvc) withPeerSteerSources(steers []agentruntime.ConsumedSteer) []agentruntime.ConsumedSteer {
	for index := range steers {
		if steers[index].SourcePeer != "" || steers[index].QueuedID == "" {
			continue
		}
		value, ok := s.peerSteerSources.LoadAndDelete(steers[index].QueuedID)
		if !ok {
			continue
		}
		source, ok := value.(peerMessageSource)
		if !ok {
			continue
		}
		steers[index].SourcePeer = source.Device
		steers[index].SourceName = source.Name
	}
	return steers
}

func firstTextBlock(blocks []cagoblocks.ContentBlock) string {
	for _, block := range blocks {
		switch text := block.(type) {
		case cagoblocks.TextBlock:
			return text.Text
		case *cagoblocks.TextBlock:
			if text != nil {
				return text.Text
			}
		}
	}
	return ""
}

func peerMessageSourceOf(message *chat_entity.Message) peerMessageSource {
	if message == nil || message.Role != "user" {
		return peerMessageSource{}
	}
	var stored []cagoblocks.StoredBlock
	if json.Unmarshal([]byte(message.BlocksJSON), &stored) != nil {
		return peerMessageSource{}
	}
	for _, block := range stored {
		if block.Type != "text" && block.Type != "display_text" {
			continue
		}
		var data struct {
			Device string `json:"sourceDevice"`
			Name   string `json:"sourceDeviceName"`
		}
		if json.Unmarshal(block.Data, &data) == nil && data.Device != "" {
			return peerMessageSource{Device: data.Device, Name: data.Name}
		}
	}
	return peerMessageSource{}
}
