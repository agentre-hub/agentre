package claudecode

import (
	"context"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

var _ agentruntime.CLIModelSetter = (*Runtime)(nil)

// SetCLIModel 与启动入口共用会话锁，避免切换与下一轮提交交错。
func (r *Runtime) SetCLIModel(ctx context.Context, sessionID int64, value string) error {
	lk := r.spawnLockFor(sessionKey(sessionID))
	lk.Lock()
	defer lk.Unlock()
	v, ok := r.cache.Get(sessionKey(sessionID))
	if !ok {
		r.modelFailures.Delete(sessionID)
		return agentruntime.ErrNoActiveTurn
	}
	a := v.(*claudeActive)
	// 轮中不写 CLI；落库的最终选择会随下一轮 RunRequest 到达。
	if a.mainThreadBusy() {
		return nil
	}
	err := setActiveCLIModel(ctx, a, value)
	if err == nil {
		r.modelFailures.Delete(sessionID)
	}
	return err
}

func setActiveCLIModel(ctx context.Context, a *claudeActive, value string) error {
	// Wails 调用可能没有 deadline，控制应答必须有界等待。
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	setter, ok := a.handle.(interface {
		SetModel(context.Context, string) error
	})
	if !ok {
		return agentruntime.ErrUnsupported
	}
	if err := setter.SetModel(ctx, value); err != nil {
		return err
	}
	a.cliModel = value
	state := agentruntime.CLIModelState{Active: true, AppliedValue: value}
	if reporter, ok := a.handle.(interface {
		CurrentModel(context.Context) (string, error)
	}); ok {
		// 查询失败不把成功的切换报成失败，也不以目录默认冒充会话实际模型。
		state.ModelID, _ = reporter.CurrentModel(ctx)
	}
	a.modelMu.Lock()
	a.modelState = state
	a.modelMu.Unlock()
	return nil
}

// CLIModelState 返回活着的这一进程的快照，未启动或已清扫时不猜测。
func (r *Runtime) CLIModelState(_ context.Context, sessionID int64) agentruntime.CLIModelState {
	if failure, ok := r.modelFailures.Load(sessionID); ok {
		return agentruntime.CLIModelState{Error: failure.(string)}
	}
	v, ok := r.cache.Get(sessionKey(sessionID))
	if !ok {
		return agentruntime.CLIModelState{}
	}
	a := v.(*claudeActive)
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	return a.modelState
}

func (a *ccClientAdapter) CurrentModel(ctx context.Context) (string, error) {
	return a.sess.CurrentModel(ctx)
}

// SetModel 把模型控制帧交给这一会话自己的常驻 CLI。
func (a *ccClientAdapter) SetModel(ctx context.Context, value string) error {
	return a.sess.SetModel(ctx, value)
}
