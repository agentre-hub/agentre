package codex

import (
	"context"
	"fmt"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

var (
	_ agentruntime.CLIModelSetter      = (*Runtime)(nil)
	_ agentruntime.CLIModelStateReader = (*Runtime)(nil)
)

// codexCLIRecord 是一条会话的 CLI 模型账。
//
// 它挂在 Runtime 而不是 codexActive 上：codexActive 一轮一换，而「回落到 CLI 默认」要
// 跨轮记着本进程到底有没有显式下发过一个固定值（见 cliModelTurnValue）。
type codexCLIRecord struct {
	// sess 是记这份账时的 app-server。换了进程说明池里那条被驱逐重开了，新线程上没有
	// 任何残留的 override，账随之作废。
	sess cxSessionHandle
	// appliedValue 是会话口径的选择值（cli-default 记空串，与 chat_svc 判「挂起」的
	// wanted 同源），不拿解析出的模型冒充它会永远显示挂起。
	appliedValue string
	// lastSentModel 是最后一次显式下发的 turn/start 模型（空 = 本进程不挂着固定值）。
	lastSentModel string
}

// cliModelTurnValue 交回这一轮 turn/start 该携带的模型（空串 = 不下发）。
//
// codex 没有独立的热切换调用：TurnStartParams.model 是「Override the model for this
// turn and subsequent turns」（spec 2026-10-26 决策 6）。所以会话选择在**每一轮**
// turn/start 上重新下发，而不是像 claude 那样空闲时立刻发一帧；启动参数与启动身份
// 都不因它变化（换了选择仍复用同一个 app-server）。
//
//   - 绑了供应商：CLI 段不生效，走解析出的 ModelID，帧与今天逐字相同。
//   - 固定模型：下发它。
//   - 跟随 CLI 默认：下发 CLI 生效配置里的模型——上一个固定值会延续到其后各轮，不下发
//     就等于「回到 CLI 默认」静默失效。
//   - 跟随 Agent 绑定：后端设了默认模型就下发它（帧与今天一致）；没设但这一进程曾经
//     显式下发过模型时同样要复位到 CLI 默认，否则残留的就是那个固定值。从没下发过的
//     会话保持今天的帧（不带 model），不为此白起一个查询进程。
func (r *Runtime) cliModelTurnValue(ctx context.Context, req agentruntime.RunRequest, sess cxSessionHandle) (string, error) {
	if req.Backend == nil {
		return "", nil
	}
	applied, lastSent := r.cliRecordState(req.SessionID, sess)
	switch req.CLIModelMode {
	case "fixed":
		return strings.TrimSpace(req.CLIModel), nil
	case "cli-default":
		return r.resolvedCLIDefault(ctx, req, applied, lastSent)
	}
	if model := codexEffectiveModel(req); model != "" {
		return model, nil
	}
	// 跟随 Agent 绑定且后端没设默认模型：只有这一进程曾经显式下发过模型时才需要复位，
	// 否则保持今天的帧（不带 model）。
	if lastSent == "" {
		return "", nil
	}
	return r.resolvedCLIDefault(ctx, req, applied, lastSent)
}

// resolvedCLIDefault 解析「不下发模型时 CLI 会用哪个」。
//
// 上一轮已经是 CLI 默认时沿用那一轮确认过的解析结果：值本来就是同一份 CLI 配置的答案，
// 而每轮都为一个字符串起一次查询进程代价太大。从别的状态切进来（上一轮固定模型 /
// 供应商）才真去问；再切一次模式仍会重新解析，与（设备, 后端）目录缓存的寿命同档。
func (r *Runtime) resolvedCLIDefault(ctx context.Context, req agentruntime.RunRequest, applied, lastSent string) (string, error) {
	if applied == "" && lastSent != "" {
		return lastSent, nil
	}
	return r.resolveCLIDefault(ctx, req)
}

// resolveCLIDefault 问 CLI 在不下发模型时会用哪个模型。
//
// 必须问按（设备, 后端）绑定的短命查询进程：会话在跑的那台可能正是被切过的那一个，
// 它答出的是这个会话的模型而不是 CLI 默认（spec「模型列表与解析的获取」）。
//
// 解析失败必须让这一轮失败而不是「什么都不下发」：线程上还挂着上一个固定值，不下发
// 就等于静默回落到旧模型继续跑（spec「不得静默回落到旧模型继续跑」）。
func (r *Runtime) resolveCLIDefault(ctx context.Context, req agentruntime.RunRequest) (string, error) {
	catalog, err := r.QueryCLIModels(ctx, agentruntime.CLIModelQueryRequest{Backend: req.Backend})
	if err != nil {
		logger.Ctx(ctx).Warn("codex.Runtime: resolve CLI default model failed",
			zap.Int64("sessionId", req.SessionID),
			zap.Int64("backendId", req.Backend.ID), zap.Error(err))
		return "", err
	}
	return strings.TrimSpace(catalog.EffectiveValue), nil
}

// SetCLIModel 确认本会话还有活着的 CLI 进程；选择本身由 chat_svc 落库并在下一轮
// RunRequest 里带回来，这里没有第二份要写。
//
// 与 claude 不同，codex 没有空闲可用的切换调用，轮中与空闲因此是同一件事——都是
// 「下一轮生效」（决策 6：会话内切换不重启 CLI）。这里刻意不 evict、不重新 spawn：
// 选择不是启动参数，逐出只会白丢一条还能用的线程。进程不在时按 CLIModelSetter 的契约
// 返回 ErrNoActiveTurn，由 chat_svc 只落库、下次以选择启动。
func (r *Runtime) SetCLIModel(_ context.Context, sessionID int64, value string) error {
	if sessionID <= 0 {
		return fmt.Errorf("agentruntime/runtimes/codex: invalid sessionID %d", sessionID)
	}
	// 记下新选择是一次新的尝试：上一轮的应用失败不再代表当前状态。
	r.modelFailures.Delete(sessionID)
	if _, ok := r.pool.Get(sessionKey(sessionID)); !ok {
		return agentruntime.ErrNoActiveTurn
	}
	return nil
}

// CLIModelState 交回这一进程自己上报的模型与应用失败，不用设备目录的默认解析。
func (r *Runtime) CLIModelState(_ context.Context, sessionID int64) agentruntime.CLIModelState {
	// 失败原文按会话留在 Runtime 上（与 claude 同形）：turn/start 被拒之后池里那条会被
	// 摘掉，状态若只挂在进程上，pill 的失效态就永远读不到「为什么」。
	if failure, ok := r.modelFailures.Load(sessionID); ok {
		return agentruntime.CLIModelState{Error: failure.(string)}
	}
	handle, ok := r.pool.Get(sessionKey(sessionID))
	if !ok {
		return agentruntime.CLIModelState{}
	}
	sess := handle.(cxSessionHandle)
	applied, _ := r.cliRecordState(sessionID, sess)
	return agentruntime.CLIModelState{
		Active:       true,
		AppliedValue: applied,
		ModelID:      strings.TrimSpace(sess.Model()),
	}
}

// cliModelAppliedValue 是会话口径的选择值（与 chat_svc 判「挂起」的 wanted 同源）：
// 跟随 CLI 默认记空串，不拿解析出的模型冒充它，否则 pill 会永远显示挂起。
func cliModelAppliedValue(req agentruntime.RunRequest) string {
	switch req.CLIModelMode {
	case "fixed":
		return req.CLIModel
	case "cli-default":
		return ""
	}
	if req.Backend == nil {
		return ""
	}
	return strings.TrimSpace(req.Backend.DefaultModel)
}

// cliRecordState 在锁内交回这条会话的账（记录来自别的进程时按空账处理）。
func (r *Runtime) cliRecordState(sessionID int64, sess cxSessionHandle) (applied, lastSent string) {
	r.cliModelMu.Lock()
	defer r.cliModelMu.Unlock()
	record := r.cliModelRecords[sessionID]
	if record == nil || record.sess != sess {
		return "", ""
	}
	return record.appliedValue, record.lastSentModel
}

// cliRecordFor 取（必要时新建）这条会话的账；池里那条换了进程就作废重记。
func (r *Runtime) cliRecordFor(sessionID int64, sess cxSessionHandle) *codexCLIRecord {
	r.cliModelMu.Lock()
	defer r.cliModelMu.Unlock()
	record := r.cliModelRecords[sessionID]
	if record == nil || record.sess != sess {
		record = &codexCLIRecord{sess: sess}
		r.cliModelRecords[sessionID] = record
	}
	return record
}

// settleCLIModelTurn 在 turn/start 成功之后落定账本：记下会话口径的选择值与这次显式
// 下发的模型，供下一轮判断要不要复位到 CLI 默认。
func (r *Runtime) settleCLIModelTurn(sessionID int64, sess cxSessionHandle, turnModel, appliedValue string) {
	r.modelFailures.Delete(sessionID)
	record := r.cliRecordFor(sessionID, sess)
	r.cliModelMu.Lock()
	defer r.cliModelMu.Unlock()
	record.appliedValue = appliedValue
	record.lastSentModel = turnModel
}

// forgetCLIModel 会话的进程被显式释放时账一起消失，不留下会误导 pill 的旧值。
func (r *Runtime) forgetCLIModel(sessionID int64) {
	r.modelFailures.Delete(sessionID)
	r.cliModelMu.Lock()
	delete(r.cliModelRecords, sessionID)
	r.cliModelMu.Unlock()
}
