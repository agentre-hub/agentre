package piagent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

var (
	_ agentruntime.CLIModelSetter      = (*Runtime)(nil)
	_ agentruntime.CLIModelStateReader = (*Runtime)(nil)
)

// cliModelSwitcher 是能在常驻进程上热切换模型的会话（RPC set_model），交回 pi 报的模型 id。
type cliModelSwitcher interface {
	SetModel(ctx context.Context, value string) (string, error)
}

// piCLIRecord 是一条会话的 CLI 模型账，挂在 Runtime 上而不是进程上：进程被清扫后
// 账作废（sess 不再是池里那个），但应用失败的原文要跨清扫留给 pill（见 modelFailures）。
type piCLIRecord struct {
	// sess 是记账时的 RPC 会话；换了进程说明重新 spawn 过，新进程按会话选择 --model 启动。
	sess sessionHandle
	// backend 是最近一轮的后端，回到 CLI 默认时要用它起查询进程解析默认值。
	backend *agent_backend_entity.AgentBackend
	// appliedValue 是会话口径的选择值（cli-default 记空串，与 chat_svc 判「挂起」的
	// wanted 同源），不拿解析出的默认模型冒充它。
	appliedValue string
	// modelID 是 pi 自己报的模型 id（set_model 应答 / 轮末上报），不用目录默认猜。
	modelID string
}

// modelLock 串行化同一会话的「切换」与「下一轮开轮」：切换判空闲与开轮登记必须互斥，
// 否则 set_model 的应答会和刚开的那一轮抢同一个 stdout。
func (r *Runtime) modelLock(sessionID int64) *sync.Mutex {
	v, _ := r.modelLocks.LoadOrStore(sessionID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// SetCLIModel 空闲时立刻在常驻进程上 set_model（不重启）；一轮进行中只返回 nil，
// 落库的最终选择随下一轮 RunRequest 到达，由 applyPendingCLIModel 在开轮前应用。
// 进程不在时按契约返回 ErrNoActiveTurn，下次以会话选择 --model 启动。
func (r *Runtime) SetCLIModel(ctx context.Context, sessionID int64, value string) error {
	lk := r.modelLock(sessionID)
	lk.Lock()
	defer lk.Unlock()
	v, ok := r.pool.Get(sessionKey(sessionID))
	if !ok {
		r.modelFailures.Delete(sessionID)
		return agentruntime.ErrNoActiveTurn
	}
	if r.turnInFlight(sessionID) {
		return nil
	}
	sess := v.(sessionHandle)
	record := r.cliRecord(sessionID, sess)
	if record == nil {
		return agentruntime.ErrNoActiveTurn
	}
	if err := r.switchCLIModel(ctx, sessionID, sess, record.backend, value); err != nil {
		return err
	}
	r.modelFailures.Delete(sessionID)
	return nil
}

// CLIModelState 交回这一进程自己的模型账；失败原文按会话留在 Runtime 上，进程被清扫后
// pill 的失效态仍读得到「为什么」。
func (r *Runtime) CLIModelState(_ context.Context, sessionID int64) agentruntime.CLIModelState {
	if failure, ok := r.modelFailures.Load(sessionID); ok {
		return agentruntime.CLIModelState{Error: failure.(string)}
	}
	v, ok := r.pool.Get(sessionKey(sessionID))
	if !ok {
		return agentruntime.CLIModelState{}
	}
	state := agentruntime.CLIModelState{Active: true}
	if record := r.cliRecord(sessionID, v.(sessionHandle)); record != nil {
		state.AppliedValue, state.ModelID = record.appliedValue, record.modelID
	}
	return state
}

// applyPendingCLIModel 在开轮前把会话选择落到进程上：新 spawn 的进程已按选择 --model
// 启动，只记账；复用的进程选择变了就 set_model。绑了供应商时 CLI 段不生效，模型走启动
// 身份。失败让这一轮不开始，不静默回落到旧模型继续跑。
func (r *Runtime) applyPendingCLIModel(ctx context.Context, req agentruntime.RunRequest, sess sessionHandle) error {
	if req.SessionID <= 0 || req.EffectiveProviderKey() != "" {
		return nil
	}
	wanted := req.CLIModelChoiceValue()
	record := r.cliRecord(req.SessionID, sess)
	if record == nil {
		r.putCLIRecord(req.SessionID, &piCLIRecord{sess: sess, backend: req.Backend, appliedValue: wanted})
		return nil
	}
	r.cliModelMu.Lock()
	if cur := r.cliRecords[req.SessionID]; cur != nil && cur.sess == sess {
		cur.backend = req.Backend
	}
	r.cliModelMu.Unlock()
	if record.appliedValue == wanted {
		return nil
	}
	return r.switchCLIModel(ctx, req.SessionID, sess, req.Backend, wanted)
}

// switchCLIModel 把 value 发给进程并记账；空串 = 回到 CLI 默认：pi 没有「取消固定」，
// 只能先用短命查询进程解析出默认模型再 set_model 到它（绝不问会话在跑的进程）。
// 失败时账不动。
func (r *Runtime) switchCLIModel(ctx context.Context, sessionID int64, sess sessionHandle, backend *agent_backend_entity.AgentBackend, value string) error {
	switcher, ok := sess.(cliModelSwitcher)
	if !ok {
		return agentruntime.ErrUnsupported
	}
	target := strings.TrimSpace(value)
	if target == "" {
		catalog, err := r.QueryCLIModels(ctx, agentruntime.CLIModelQueryRequest{Backend: backend})
		if err != nil {
			logger.Ctx(ctx).Warn("piagent.switchCLIModel: resolve CLI default model failed",
				zap.Int64("sessionId", sessionID), zap.Error(err))
			return err
		}
		if target = strings.TrimSpace(catalog.EffectiveValue); target == "" {
			return errors.New("piagent: pi did not report a default model")
		}
	}
	// Wails 调用可能没有 deadline，set_model 应答必须有界等待。
	setCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	modelID, err := switcher.SetModel(setCtx, target)
	if err != nil {
		logger.Ctx(ctx).Warn("piagent.switchCLIModel: pi rejected model",
			zap.Int64("sessionId", sessionID), zap.String("model", target), zap.Error(err))
		return err
	}
	r.cliModelMu.Lock()
	defer r.cliModelMu.Unlock()
	if record := r.cliRecords[sessionID]; record != nil && record.sess == sess {
		record.appliedValue, record.modelID = strings.TrimSpace(value), strings.TrimSpace(modelID)
	}
	return nil
}

// noteTurnModel 轮末记下 pi 这一轮报的模型。
func (r *Runtime) noteTurnModel(sessionID int64, sess sessionHandle, model string) {
	if model = strings.TrimSpace(model); model == "" {
		return
	}
	r.cliModelMu.Lock()
	defer r.cliModelMu.Unlock()
	if record := r.cliRecords[sessionID]; record != nil && record.sess == sess {
		record.modelID = model
	}
}

// turnInFlight 判断这条会话此刻是否有已登记的一轮（PrepareRun 登记到轮末注销）。
func (r *Runtime) turnInFlight(sessionID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.prepared[sessionID] != nil
}

// cliRecord 交回这条会话、属于 sess 这个进程的账；记录来自别的进程时视为无账。
func (r *Runtime) cliRecord(sessionID int64, sess sessionHandle) *piCLIRecord {
	r.cliModelMu.Lock()
	defer r.cliModelMu.Unlock()
	record := r.cliRecords[sessionID]
	if record == nil || record.sess != sess {
		return nil
	}
	cp := *record
	return &cp
}

func (r *Runtime) putCLIRecord(sessionID int64, record *piCLIRecord) {
	r.cliModelMu.Lock()
	r.cliRecords[sessionID] = record
	r.cliModelMu.Unlock()
}

// forgetCLIModel 会话的进程被显式释放时账与失败一起消失。
func (r *Runtime) forgetCLIModel(sessionID int64) {
	r.modelFailures.Delete(sessionID)
	r.cliModelMu.Lock()
	delete(r.cliRecords, sessionID)
	r.cliModelMu.Unlock()
}
