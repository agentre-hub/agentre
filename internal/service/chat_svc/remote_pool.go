package chat_svc

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/remotepool"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

// remote_pool.go 是 chat_svc 到 remotepool 的接线:租约缓存本身住在那个兄弟包里,
// 这里只留三样宿主侧的东西 —— backend → 设备解析的入口、i18n 错误码翻译,以及
// *remote.Runtime 的装配(五类通知 observer 与重连端口只有 chat_svc 知道)。

// remotePool 惰性构造设备租约池。惰性是必需的:不少单测直接字面量构造 chatSvc,
// 拿不到 NewChat 的构造时机(与 pool() / tokenCache() 同一理由)。
func (s *chatSvc) remotePool() *remotepool.Pool {
	s.remotePoolOnce.Do(func() {
		s.remotePoolImpl = remotepool.New(remotePoolHost{s: s})
	})
	return s.remotePoolImpl
}

// remotePoolHost 把 chatSvc 适配成 remotepool.Host。用独立适配器而不是给 chatSvc
// 加一组导出方法:池要的四件事是它对宿主的窄端口,不是 chat_svc 的对外面貌。
type remotePoolHost struct{ s *chatSvc }

func (h remotePoolHost) ConnPool() remote_device_svc.ConnPool { return h.s.pool() }

func (h remotePoolHost) PairedDeviceID(ctx context.Context, deviceRef string) (int64, bool) {
	return localPairedDeviceID(ctx, deviceRef)
}

func (h remotePoolHost) DaemonFingerprint(ctx context.Context, deviceID int64) string {
	return h.s.daemonFingerprint(ctx, deviceID)
}

func (h remotePoolHost) RecordExecDaemon(
	ctx context.Context, sessionID, deviceID int64, fingerprint string, agentBackendID int64,
) {
	h.s.recordExecDaemon(ctx, sessionID, deviceID, fingerprint, agentBackendID)
}

func (h remotePoolHost) NewRuntime(
	deviceID int64, entry *remotepool.Entry, conn client.ProtobufConnection, fingerprint string,
) *remote.Runtime {
	return remote.New(conn,
		remote.WithDaemonFingerprint(fingerprint),
		remote.WithConnStateObserver(remote.ConnStateFunc(h.s.onRemoteConnState)),
		remote.WithReconnect(remote.ReconnectFunc(func(rctx context.Context) (client.ProtobufConnection, string, error) {
			return h.s.reconnectRemote(rctx, deviceID, entry)
		})),
	)
}

// pool 返回当前生效的 ConnPool。测试通过 setConnPoolForTest 注入 mock。
func (s *chatSvc) pool() remote_device_svc.ConnPool {
	if s.testHookPool != nil {
		return s.testHookPool
	}
	return remote_device_svc.Default().Pool()
}

// borrowRemoteRuntime 借该 backend 所指设备上共享的 *remote.Runtime,并同步预取一次
// 能力矩阵。预取失败不阻断借用 —— rt.Capabilities() 回退到占位值,UI gating 不挂死。
func (s *chatSvc) borrowRemoteRuntime(
	ctx context.Context, be *agent_backend_entity.AgentBackend, sessionID int64,
) (*remote.Runtime, error) {
	rt, err := s.remotePool().Borrow(ctx, be.DeviceFingerprint, sessionID, be.ID)
	if err != nil {
		return nil, s.mapBorrowError(ctx, err)
	}
	s.prefetchRemoteCapabilities(ctx, rt, be)
	return rt, nil
}

// borrowRemoteRuntimeForTurn 与 borrowRemoteRuntime 相同,但带上本轮自己的
// generation token,并交回该轮的释放函数(迟到的旧 release 顶不掉新一轮的引用)。
//
// 错误契约(调用方按它写,别按实现猜):err != nil 时一件资源都没占住,交回的
// release 是可安全调用的 no-op —— 不为 nil,也不必被调用。上游 selectTurnRunner
// 在错误路径上直接 return、从不调 release,正是靠这条契约才不泄漏租约。
// 由 TestBorrowRemoteRuntimeForTurn_DialFailure_HoldsNothingAndReleaseIsANoop 守住。
func (s *chatSvc) borrowRemoteRuntimeForTurn(
	ctx context.Context, be *agent_backend_entity.AgentBackend, sessionID int64,
) (*remote.Runtime, func(), error) {
	rt, release, err := s.remotePool().BorrowForTurn(ctx, be.DeviceFingerprint, sessionID, be.ID)
	if err != nil {
		return nil, func() {}, s.mapBorrowError(ctx, err)
	}
	s.prefetchRemoteCapabilities(ctx, rt, be)
	return rt, release, nil
}

// prefetchRemoteCapabilities 同步拉一次远端 backend 的 capability 矩阵缓存到本地,
// 之后 rt.Capabilities() 直接返实际能力。已缓存过的 backendType 直接 noop
// (cache 命中不会再发 RPC)。
func (s *chatSvc) prefetchRemoteCapabilities(
	ctx context.Context, rt *remote.Runtime, be *agent_backend_entity.AgentBackend,
) {
	if err := rt.Prefetch(ctx, agent_backend_entity.BackendType(be.Type)); err != nil {
		deviceID, _ := localPairedDeviceID(ctx, be.DeviceFingerprint)
		logger.Ctx(ctx).Warn("borrowRemoteRuntime: capability prefetch failed",
			zap.Int64("deviceID", deviceID),
			zap.String("backendType", be.Type),
			zap.Error(err))
	}
}

// mapBorrowError 把池的哨兵错误翻成 chat_svc 的 i18n 错误码。
func (s *chatSvc) mapBorrowError(ctx context.Context, err error) error {
	if errors.Is(err, remotepool.ErrInvalidDevice) {
		return i18n.NewError(ctx, code.AgentBackendInvalidDevice)
	}
	var dial *remotepool.DialError
	if errors.As(err, &dial) {
		return i18n.NewError(ctx, code.RemoteRunnerDialFailed)
	}
	return err
}

// cachedRemoteRuntime 见 remotepool.Pool.Cached:只读控制路径专用,一件副作用都不做。
func (s *chatSvc) cachedRemoteRuntime(deviceID int64) *remote.Runtime {
	return s.remotePool().Cached(deviceID)
}

// releaseRemoteRuntime 见 remotepool.Pool.Release。
func (s *chatSvc) releaseRemoteRuntime(deviceID, sessionID int64) {
	s.remotePool().Release(deviceID, sessionID)
}

// remoteRuntimeCount 是测试用的引用计数探针。
func (s *chatSvc) remoteRuntimeCount(deviceID int64) int {
	return s.remotePool().Count(deviceID)
}

// 编译期确认适配器满足端口。
var _ remotepool.Host = remotePoolHost{}
