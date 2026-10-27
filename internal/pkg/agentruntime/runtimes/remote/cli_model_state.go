package remote

import (
	"context"
	"errors"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

var _ agentruntime.CLIModelStateReader = (*Runtime)(nil)

// CLIModelState 交回本端记下的这条远端会话的 CLI 模型账：开轮时交给执行端的选择、
// 执行端轮末上报的模型、以及最近一次应用失败的原文。与本机 runtime 同一口径，供 pill
// 判「挂起」与「失效」；从没在本连接上跑过的会话不猜，交零值。
func (r *Runtime) CLIModelState(_ context.Context, sessionID int64) agentruntime.CLIModelState {
	r.cliModelMu.Lock()
	defer r.cliModelMu.Unlock()
	return r.cliModels[sessionID]
}

// noteCLIModelRun 在一轮交给执行端之后记账：成功 = 执行端以这份选择开轮；会话选了 CLI
// 模型而开轮失败 = 下一轮应用失败，原文留给 pill 的失效态。绑了供应商的轮次 CLI 段不生效，
// 账一并作废。
func (r *Runtime) noteCLIModelRun(req agentruntime.RunRequest, err error) {
	if req.SessionID <= 0 {
		return
	}
	r.cliModelMu.Lock()
	defer r.cliModelMu.Unlock()
	if req.LLMProviderKey != "" || req.EffectiveProviderKey() != "" {
		delete(r.cliModels, req.SessionID)
		return
	}
	if r.cliModels == nil {
		r.cliModels = map[int64]agentruntime.CLIModelState{}
	}
	if err != nil {
		if req.CLIModelMode != "" {
			r.cliModels[req.SessionID] = agentruntime.CLIModelState{Error: err.Error()}
		}
		return
	}
	prev := r.cliModels[req.SessionID]
	r.cliModels[req.SessionID] = agentruntime.CLIModelState{Active: true, AppliedValue: req.CLIModelChoiceValue(), ModelID: prev.ModelID}
}

// noteCLIModelResult 记下执行端这一轮上报的模型；首个模型报告之前就失败的轮次与本机
// claudecode 同一判据，视为选择没能应用。
func (r *Runtime) noteCLIModelResult(sessionID int64, model string, stopErr error) {
	r.cliModelMu.Lock()
	defer r.cliModelMu.Unlock()
	state, ok := r.cliModels[sessionID]
	if !ok || !state.Active {
		return
	}
	if model = strings.TrimSpace(model); model != "" {
		state.ModelID, state.Error = model, ""
	} else if stopErr != nil && !errors.Is(stopErr, agentruntime.ErrAborted) {
		state.Error = stopErr.Error()
	}
	r.cliModels[sessionID] = state
}

// noteCLIModelSwitch 记下空闲时的即时切换：执行端确认 = 已应用；执行端已没有这条会话的
// 进程 = 账作废（下次以会话选择启动，不该再显示挂起）；其它失败是 CLI 拒绝，账不动。
func (r *Runtime) noteCLIModelSwitch(sessionID int64, value string, err error) {
	r.cliModelMu.Lock()
	defer r.cliModelMu.Unlock()
	switch {
	case err == nil:
		if r.cliModels == nil {
			r.cliModels = map[int64]agentruntime.CLIModelState{}
		}
		state := r.cliModels[sessionID]
		state.Active, state.AppliedValue, state.Error = true, strings.TrimSpace(value), ""
		r.cliModels[sessionID] = state
	case errors.Is(err, agentruntime.ErrNoActiveTurn):
		delete(r.cliModels, sessionID)
	}
}
