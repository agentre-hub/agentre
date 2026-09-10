package chat_svc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/claudecode"
)

// shouldSignChatGateway 决定本轮要不要给 CLI 子进程签一个 gateway token（spec
// 2026-08-10 决策 6）。Claude Code local 无论是否有 provider 都要签——PostToolUse
// hook 子进程访问 /hook/v1/inbox 靠它，与 LLM 是否走网关无关（网关路由那半独立由
// BuildClaudeCodeEnv 按 effective provider 门控）。Codex local 没有 hook，只有本轮
// 存在 effective provider（prov 非 nil：会话 provider_key 覆盖 agent 绑定后解析出的
// 那家，已过缺失/停用回退）时才该签，否则会把它自身的 CLI 登录态误打到本地网关
// ——门控看 prov 而不是 be.LLMProviderKey，是登录态会话能双向切换供应商的前提。
func shouldSignChatGateway(be *agent_backend_entity.AgentBackend, prov *llm_provider_entity.LLMProvider) bool {
	if be == nil || be.IsBuiltin() {
		return false
	}
	if be.IsClaudeCode() {
		return true
	}
	return prov != nil
}

// gatewayRoutesLLM 报告这个后端的 LLM 流量是否真的经本机网关：claudecode 靠
// ANTHROPIC_BASE_URL、codex 靠 model_provider/base_url，两者都是 spawn 时从网关派生的
// 启动期参数，拿不到网关就会静默退回 CLI 自身登录态。piagent 不在此列 —— 它把
// provider.APIKey 直接注进子进程 env（agentruntime.BuildPiAgentProviderEnv），整个
// piagent runtime 没有任何 GatewayURL/GatewayToken 消费点，网关没在跑照样打得到所选
// 供应商。「有 effective provider 就必须有可用网关」这条门控只对前两者成立。
func gatewayRoutesLLM(be *agent_backend_entity.AgentBackend) bool {
	return be != nil && (be.IsClaudeCode() || be.IsCodex())
}

// remoteProviderKnownMissing returns true only when the watcher cache has a
// recorded provider list for the remote device and that list does not contain
// the backend's provider key. A nil list means "no heartbeat data yet", so the
// runtime path is allowed to try and report the authoritative daemon error.
func remoteProviderKnownMissing(ctx context.Context, be *agent_backend_entity.AgentBackend) bool {
	if !beTargetsRemote(be) || strings.TrimSpace(be.LLMProviderKey) == "" {
		return false
	}
	deviceID, ok := localPairedDeviceID(ctx, be.DeviceFingerprint)
	if !ok {
		return false
	}
	rds := remote_device_svc.Default()
	if rds == nil {
		return false
	}
	providers := rds.ListDeviceProviders(deviceID)
	if providers == nil {
		return false
	}
	for _, p := range providers {
		if p.Key == be.LLMProviderKey {
			return false
		}
	}
	return true
}

func remoteProviderNotConfiguredError(ctx context.Context, providerKey string) error {
	key := strings.TrimSpace(providerKey)
	if key == "" {
		key = "unknown"
	}
	return i18n.NewError(ctx, code.ChatRemoteProviderNotConfigured, key, key)
}

// signChatTokenFor 为需要 gateway 的 CLI 后端签一个 **会话级常驻** token。
// 返回 (gatewayURL, token)，任意一者为空时调用方按"不签"处理（CLI 走自身 login）。
//
// Claude Code local 会使用 token 访问 /hook/v1/inbox；绑定了 LLM provider 的
// Claude Code / Codex 会用它走 LLM 转发。Codex local 不应调用这里。
//
// 关键不变量:同一 session 跨轮返回 **同一个永久 token**。该 token 在首轮 spawn 时
// 烤进 claude 子进程 env(AGENTRE_GATEWAY_TOKEN),后续轮复用子进程时 env 不重建 ——
// 旧实现每轮重签 15min TTL 的新 token、却只有首轮那个被烤进去,导致长会话(>15min)
// 子进程手里的 token 过期、PostToolUse hook 撞 401、SteerInbox 整轮 drain 不到、
// steer 被压到轮末 DrainPending。改成 ttl=0 永久 + 跨轮复用,寿命跟随子进程,
// session 删除时由 Delete→revokeChatToken 撤销。
//
// providerKey 是**本轮真正要跑的那家供应商**(turn 入口按会话 provider_key 覆盖解析后的
// prov;回退过的话就是回退目标),token 按它路由。会话中途换了 target 时,下一轮走
// SetTokenTarget 改既有 token 的路由目标而**不重签** —— 见上面那条不变量,重签等于
// 让在跑的子进程手里那个立刻失效。空串 = CLI 自身登录态(token 只用于 hook inbox)。
func (s *chatSvc) signChatTokenFor(
	ctx context.Context, be *agent_backend_entity.AgentBackend, sessionID int64, providerKey, modelKey string,
) (string, string) {
	if be == nil || s.gateway == nil {
		return "", ""
	}
	// 网关可用性与 URL 是宿主侧的事,缓存不管这两件(见 agentruntime.SessionTokenCache)。
	if s.gateway.Status().State != "running" {
		return "", ""
	}
	tok, err := s.tokenCache().EnsureToken(ctx, sessionID, be, providerKey, modelKey)
	if err != nil {
		return "", ""
	}
	return s.gateway.URL(), tok
}

// tokenCache 惰性构造会话 token 缓存。router 取值函数每次读 s.gateway —— 网关是
// bootstrap 在 chatSvc 之后注入的(RegisterGateway),构造期取不到。
func (s *chatSvc) tokenCache() *agentruntime.SessionTokenCache {
	s.tokenCacheOnce.Do(func() {
		s.chatTokens = agentruntime.NewSessionTokenCache("chat_svc.signChatTokenFor",
			func() agentruntime.SessionTokenRouter {
				if s.gateway == nil {
					return nil
				}
				return s.gateway
			})
	})
	return s.chatTokens
}

// revokeChatToken 撤销并清掉某 session 的常驻 token。Delete 关闭常驻子进程后调用,
// 让 token 寿命跟随子进程 —— 之后该 id 若复活会重签一个新的。
func (s *chatSvc) revokeChatToken(sessionID int64) {
	s.tokenCache().Revoke(sessionID)
}

// mapProviderSessionError 命中 Claude Code 或通用 runtime 的 SessionNotFound
// sentinel 时做两件事：
//  1. 清空 sess.ProviderSessionID 并立即持久化（context.WithoutCancel 防 abort
//     路径下 turnCtx 已 cancel 导致静默失败）—— 下一轮 Send 才能 spawn 全新
//     CLI 会话，而不是一直拿 --resume 撞同一个失效 id。
//  2. 把 err 替换成 ChatProviderSessionGone 的 i18n 错误，前端拿到的就是
//     "CLI 会话已过期 …" 中文人话，不是英文 stderr。
//
// 非 ErrSessionNotFound 原样返回，让上层走 default 失败路径。
func (s *chatSvc) mapProviderSessionError(ctx context.Context, sess *chat_entity.Session, src error) error {
	if !providerSessionNotFound(src) {
		return src
	}
	if sess != nil && sess.HasProviderSession() {
		sess.SetProviderSession("")
		_ = chat_repo.Session().Update(context.WithoutCancel(ctx), sess)
	}
	return i18n.NewError(ctx, code.ChatProviderSessionGone)
}

func providerSessionNotFound(err error) bool {
	return errors.Is(err, claudecode.ErrSessionNotFound) || errors.Is(err, agentruntime.ErrSessionNotFound)
}

// mapTurnError 把一轮的终止原因翻成**交到用户面前的那句话**。
//
// 远端的两种非失败终止在这里分道:R15 规定它们都沿用既有的 error 态、不新增第五个
// AgentStatus 取值,「由消息文案区分其与真实错误」—— 而消息文案就是这个返回值(经
// assistantMsg.ErrorText 持久化)。三句话必须互不相同:被打断(daemon 重启 / 会话在
// 那台机器上已中断)、连不上了(重连彻底失败)、真的跑失败了(原样透出后端错误)。
func (s *chatSvc) mapTurnError(ctx context.Context, sess *chat_entity.Session, be *agent_backend_entity.AgentBackend, src error) error {
	if src == nil {
		return nil
	}
	if errors.Is(src, remote.ErrRunInterrupted) {
		return i18n.NewError(ctx, code.ChatRemoteRunInterrupted)
	}
	if errors.Is(src, remote.ErrDaemonDisconnected) {
		return i18n.NewError(ctx, code.ChatRemoteDaemonUnreachable)
	}
	if providerSessionNotFound(src) {
		return s.mapProviderSessionError(ctx, sess, src)
	}
	var rpcErr *rpcerror.Error
	if errors.As(src, &rpcErr) && rpcErr.Code == rpcerror.ErrProviderMissing.Code {
		key := ""
		if be != nil {
			key = be.LLMProviderKey
		}
		return remoteProviderNotConfiguredError(ctx, key)
	}
	return src
}

func chatRuntimeErrorLogFields(err error) []zap.Field {
	if err == nil {
		return nil
	}
	fields := []zap.Field{
		zap.String("errorClass", fmt.Sprintf("%T", err)),
		zap.Int("errorBytes", len(err.Error())),
	}
	var appErr *httputils.Error
	if errors.As(err, &appErr) {
		return append(fields, zap.Int("errorCode", appErr.Code))
	}
	var rpcErr *rpcerror.Error
	if errors.As(err, &rpcErr) {
		return append(fields, zap.Int32("errorCode", rpcErr.Code))
	}
	return fields
}

func (s *chatSvc) failTurn(ctx context.Context, sess *chat_entity.Session, msg *chat_entity.Message, stream string, err error) {
	// 一次落地点收所有 turn 级别错误(selectRunner / resolveSessionCwd / runner.Run /
	// stream loop streamStopErr 等),给运维保留安全分类与定位 ID；完整错误仅继续走
	// 既有前端 StreamError 与持久化 ErrorText 边界。
	fields := make([]zap.Field, 0, 7)
	fields = append(fields,
		zap.Int64("sessionId", sess.ID),
		zap.Int64("messageId", msg.ID),
		zap.String("stream", stream),
		zap.String("agentStatus", sess.AgentStatus),
	)
	fields = append(fields, chatRuntimeErrorLogFields(err)...)
	logger.Ctx(ctx).Warn("chat_svc.failTurn: turn failed", fields...)
	// 终态一律用 WithoutCancel 落库:失败路径最常见的触发方式就是用户点「停止」把
	// turnCtx cancel 掉,若沿用同一个 ctx,这两条 Update 会被 DB 层直接拒掉,结果
	// agent_status 永远停在 running、error_text 也写不进去(前端既不报错也停不掉)。
	finalCtx := context.WithoutCancel(ctx)
	msg.ErrorText = err.Error()
	if uerr := chat_repo.Message().Update(finalCtx, msg); uerr != nil {
		logger.Ctx(finalCtx).Error("chat_svc.failTurn: persist error text failed",
			zap.Int64("messageId", msg.ID), zap.Error(uerr))
	}
	sess.AgentStatus = "error"
	sess.NeedsAttention = false
	if uerr := chat_repo.Session().Update(finalCtx, sess); uerr != nil {
		logger.Ctx(finalCtx).Error("chat_svc.failTurn: persist session status failed",
			zap.Int64("sessionId", sess.ID), zap.Error(uerr))
	}
	// session_status 必须先于 StreamError emit:前端 chat-streams-host 收到 error
	// 立刻 finishStream 删 LiveStream entry → StreamSubscriber 紧接着 unmount,后到
	// 的 session_status 永远收不到。后台 session 出错时只靠 bumpDone 不会翻 tab 红点。
	logger.Ctx(finalCtx).Info("chat_svc: session_status emit",
		zap.Int64("sessionId", sess.ID),
		zap.Int64("assistantMsgId", msg.ID),
		zap.String("stream", stream),
		zap.String("agentStatus", sess.AgentStatus),
		zap.Bool("needsAttention", sess.NeedsAttention),
		zap.String("source", "failTurn"))
	s.emitter.Emit(finalCtx, stream, ChatStreamEvent{
		Kind: StreamSessionStatus,
		SessionStatus: &ChatSessionStatusPatch{
			AgentStatus:    sess.AgentStatus,
			NeedsAttention: sess.NeedsAttention,
			BgRunning:      s.bgRunningActive(sess.ID),
		},
	})
	s.emitter.Emit(finalCtx, stream, ChatStreamEvent{
		Kind:    StreamError,
		Error:   err.Error(),
		Message: chatMessageForEvent(sess, msg),
	})
	// 错误路径的唯一终态回灌点。failTurn 直线到此(无内部 early return),尾端单点
	// publish 即覆盖全部退出路径;与 finalize 互斥(调用方 failTurn 后立即 return)。
	s.publishTurnResult(sess.ID, TurnResult{
		SessionID:          sess.ID,
		AssistantMessageID: msg.ID,
		Err:                err,
	})
}

// turnAbortedByUser 判定「runner.Run 返回的这个错误其实是用户点了停止」。
// 只认两种信号:runtime 显式回 ErrAborted,或本会话已被 Stop 标记且错误确实是
// ctx 取消。普通故障(拨号失败等)即使碰巧带着 abort 标记也仍按错误处理,免得把
// 真故障伪装成"用户停的"。
func (s *chatSvc) turnAbortedByUser(sessionID int64, err error) bool {
	if errors.Is(err, agentruntime.ErrAborted) {
		s.aborted.LoadAndDelete(sessionID)
		return true
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if _, ok := s.aborted.Load(sessionID); !ok {
		return false
	}
	s.aborted.LoadAndDelete(sessionID)
	return true
}

// abortTurnBeforeStream 收敛「Run 还没返回就被 Stop」的那一轮。此时 runtime 侧
// 还没注册 activeTurn(OpenClaw 要先跟网关握手),既没有流也没有产出,但会话已经是
// running —— 必须在这里落回 idle,否则侧栏一直转圈、且只有重启 app 才洗得掉。
// 与流式中途 abort 对齐:发 StreamAborted 而不是 StreamError,不写 ErrorText。
func (s *chatSvc) abortTurnBeforeStream(ctx context.Context, sess *chat_entity.Session, msg *chat_entity.Message, stream string) {
	finalCtx := context.WithoutCancel(ctx)
	logger.Ctx(finalCtx).Info("chat_svc: turn aborted before stream started",
		zap.Int64("sessionId", sess.ID),
		zap.Int64("assistantMsgId", msg.ID),
		zap.String("stream", stream))
	if uerr := chat_repo.Message().Update(finalCtx, msg); uerr != nil {
		logger.Ctx(finalCtx).Error("chat_svc.abortTurnBeforeStream: persist message failed",
			zap.Int64("messageId", msg.ID), zap.Error(uerr))
	}
	sess.AgentStatus = "idle"
	sess.NeedsAttention = false
	if uerr := chat_repo.Session().Update(finalCtx, sess); uerr != nil {
		logger.Ctx(finalCtx).Error("chat_svc.abortTurnBeforeStream: persist session status failed",
			zap.Int64("sessionId", sess.ID), zap.Error(uerr))
	}
	s.emitter.Emit(finalCtx, stream, ChatStreamEvent{
		Kind: StreamSessionStatus,
		SessionStatus: &ChatSessionStatusPatch{
			AgentStatus:    sess.AgentStatus,
			NeedsAttention: sess.NeedsAttention,
			BgRunning:      s.bgRunningActive(sess.ID),
		},
	})
	s.emitter.Emit(finalCtx, stream, ChatStreamEvent{
		Kind:    StreamAborted,
		Message: chatMessageForEvent(sess, msg),
	})
	s.publishTurnResult(sess.ID, TurnResult{
		SessionID:          sess.ID,
		AssistantMessageID: msg.ID,
	})
}

func (s *chatSvc) lockFor(sessionID int64) *trylockMutex {
	v, _ := s.locks.LoadOrStore(sessionID, &trylockMutex{})
	return v.(*trylockMutex)
}

func (t *trylockMutex) TryLock() bool { return t.mu.TryLock() }
func (t *trylockMutex) Unlock()       { t.mu.Unlock() }
