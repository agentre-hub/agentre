// Package sessionengine 回答「一条已有会话用哪份引擎配置、在哪台设备上」的只读那一半
// （spec 2026-11-25 决策 10：会话第一轮钉住设备与引擎配置，此后不再改变）。
//
// 单开一个叶子包而不是留在 chat_svc：chat_svc 的轮次入口、它的只读读者（工具审批、提问
// 应答、ctl 详情、会话头）与 chat_svc/peerstream 交给账号对端的会话摘要要同一份口径，
// 而 peerstream 是 chat_svc 的叶子，反向 import 不成立（与 projectsync 同一理由）。
//
// 写的那一半（第一轮按派发结果钉住）在 chat_svc：只有起一轮的入口才派发、才写库。
package sessionengine

import (
	"context"

	"github.com/cago-frame/cago/pkg/i18n"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/svcerr"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

var sessionEngineErrors = svcerr.Reporter{LogMessage: "chat_svc.sessionengine: operation failed", CallerSkip: 1}

func operationFailedWithCause(ctx context.Context, cause error, fields ...zap.Field) error {
	return sessionEngineErrors.OperationFailedWithCause(ctx, cause, fields...)
}

// EffectiveFunc 成员生效的引擎配置（成员自己引用的 › 部门链 › 账号默认，agent_svc 的那条
// 继承链）；无处可用时为 nil。
type EffectiveFunc func(ctx context.Context, a *agent_entity.Agent) (*agent_backend_entity.EngineProfile, error)

var effective EffectiveFunc

// RegisterEffective 由 bootstrap 接上 agent_svc 的生效解析。未注册时一律视为「没有生效的
// 引擎配置」。
func RegisterEffective(fn EffectiveFunc) { effective = fn }

// Effective 成员此刻生效的引擎配置；没有（或未接线）时为 nil。
func Effective(ctx context.Context, a *agent_entity.Agent) (*agent_backend_entity.EngineProfile, error) {
	if effective == nil || a == nil {
		return nil, nil
	}
	return effective(ctx, a)
}

// View 把一份引擎配置摊成这一轮执行用的后端视图：设置全取自配置，设备是会话落在的那台
// （空 = 本机）。视图不是 agent_backends 的一行（ID 为零），CLI 路径属于设备设置，由执行
// 入口按（运行时, 设备）另行补上。
func View(p *agent_backend_entity.EngineProfile, device devicefp.Carrier) *agent_backend_entity.AgentBackend {
	be := p.AsBackend()
	if be == nil {
		return nil
	}
	be.DeviceFingerprint = device
	return be
}

// Pinned 已钉住的会话此刻用的后端视图；ok=false 表示还没钉（第一轮由派发决定）。
//
//   - 钉了引擎配置（engine_profile_id，含迁移时从旧执行档换过来的老会话）：那份配置按它的
//     当前设置（被删的按最后的设置）摊成的视图，设备是会话钉住的那台（见 Device）。
//   - 只钉了旧的执行目标档（exec_agent_backend_id，旧派发路径与 agrctl --backend 写下的）
//     且那一行还在：原样用那一行，随后端表在协议切换时一并移除。那一行已不在时视为没钉。
func Pinned(ctx context.Context, sess *chat_entity.Session) (*agent_backend_entity.AgentBackend, bool, error) {
	if sess == nil {
		return nil, false, nil
	}
	if sess.EngineProfileID > 0 {
		profile, err := agent_backend_repo.EngineProfile().FindAnyStatus(ctx, sess.EngineProfileID)
		if err != nil {
			return nil, false, operationFailedWithCause(ctx, err,
				zap.Int64("sessionId", sess.ID), zap.Int64("engineProfileId", sess.EngineProfileID))
		}
		if profile == nil {
			return nil, false, i18n.NewError(ctx, code.EngineProfileNotFound)
		}
		device, err := Device(ctx, sess)
		if err != nil {
			return nil, false, err
		}
		return View(profile, device), true, nil
	}
	if sess.ExecAgentBackendID > 0 {
		be, err := agent_backend_repo.AgentBackend().Find(ctx, sess.ExecAgentBackendID)
		if err != nil {
			return nil, false, operationFailedWithCause(ctx, err,
				zap.Int64("sessionId", sess.ID), zap.Int64("agentBackendId", sess.ExecAgentBackendID))
		}
		if be != nil {
			return be, true, nil
		}
	}
	return nil, false, nil
}

// Device 已钉会话落在的设备，与 dispatch_svc 的会话作用域同一口径：
// exec_device_fingerprint › 旧执行目标档那一行所在的设备 › 本机（空串）。
func Device(ctx context.Context, sess *chat_entity.Session) (devicefp.Carrier, error) {
	if sess == nil {
		return "", nil
	}
	if sess.ExecDeviceFingerprint != "" {
		return sess.ExecDeviceFingerprint, nil
	}
	if sess.ExecAgentBackendID <= 0 {
		return "", nil
	}
	be, err := agent_backend_repo.AgentBackend().Find(ctx, sess.ExecAgentBackendID)
	if err != nil {
		return "", operationFailedWithCause(ctx, err,
			zap.Int64("sessionId", sess.ID), zap.Int64("agentBackendId", sess.ExecAgentBackendID))
	}
	if be == nil {
		return "", nil
	}
	return be.DeviceFingerprint, nil
}

// ForSession 会话的后端视图，给只读的读者（会话头、工具审批 / 提问应答投递、ctl 详情、
// 账号对端的会话摘要）：已钉住就是钉住的那份；还没钉（建好还没跑过第一轮的会话）时是成员
// 此刻生效的引擎配置在本机的视图 —— 只读口不派发、不写库。都没有时为 nil。
func ForSession(ctx context.Context, sess *chat_entity.Session, a *agent_entity.Agent) (*agent_backend_entity.AgentBackend, error) {
	be, ok, err := Pinned(ctx, sess)
	if err != nil || ok {
		return be, err
	}
	profile, err := Effective(ctx, a)
	if err != nil || profile == nil {
		return nil, err
	}
	// 没钉住 = 既没有引擎配置、也没有还在的旧执行档：设备只可能来自设备列本身。
	device := devicefp.Carrier("")
	if sess != nil {
		device = sess.ExecDeviceFingerprint
	}
	return View(profile, device), nil
}
