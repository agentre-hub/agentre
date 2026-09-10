package handlers_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/handlers/mock_handlers"
	"github.com/agentre-hub/agentre/internal/service/update_svc"
)

// selfUpdateFixture 组装一套默认全绿(0 条活跃轮次、有更新、下载校验都成功)的
// SelfUpdateHandlers,用例按需覆盖其中某一步。
func selfUpdateFixture(t *testing.T) (
	context.Context, *mock_handlers.MockSelfUpdateActiveTurnsPort, *handlers.SelfUpdateDeps, *handlers.SelfUpdateHandlers,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	activeTurns := mock_handlers.NewMockSelfUpdateActiveTurnsPort(ctrl)

	deps := &handlers.SelfUpdateDeps{
		ActiveTurns: activeTurns,
		Resolve: func(context.Context, update_svc.AgentredReleaseOptions) (*update_svc.AgentredRelease, error) {
			return &update_svc.AgentredRelease{
				Channel: update_svc.ChannelStable, CurrentVersion: "0.3.0", LatestVersion: "0.4.0", HasUpdate: true,
			}, nil
		},
		Apply: func(context.Context, *update_svc.AgentredRelease, update_svc.ApplyAgentredUpdateOptions) error {
			return nil
		},
		Restart: func() {},
	}
	return context.Background(), activeTurns, deps, handlers.NewSelfUpdateHandlers(*deps)
}

// TestSelfUpdate_AcceptsAndAppliesTheRelease 覆盖第一个必须成立的场景:全绿路径上
// 一次调用被受理、Apply 真的被调用(升级已经开始),应答带出目标版本。
func TestSelfUpdate_AcceptsAndAppliesTheRelease(t *testing.T) {
	ctx, activeTurns, deps, _ := selfUpdateFixture(t)
	activeTurns.EXPECT().CountRunning(gomock.Any()).Return(int64(0), nil)
	var applied int32
	deps.Apply = func(context.Context, *update_svc.AgentredRelease, update_svc.ApplyAgentredUpdateOptions) error {
		atomic.AddInt32(&applied, 1)
		return nil
	}
	var restarted int32
	deps.Restart = func() { atomic.AddInt32(&restarted, 1) }
	h := handlers.NewSelfUpdateHandlers(*deps)

	result, err := h.Update(ctx, handlers.SelfUpdateParams{Channel: "stable"})
	require.NoError(t, err)
	assert.True(t, result.Accepted)
	assert.Equal(t, handlers.SelfUpdateRejectNone, result.RejectReason)
	assert.Equal(t, "0.4.0", result.TargetVersion)
	assert.Equal(t, int32(1), atomic.LoadInt32(&applied), "受理必须真的开始升级,不能只是回一句 accepted")

	// Restart 是异步触发的(它会让进程退出,不能挡住应答的返回),给它一点时间跑起来。
	require.Eventually(t, func() bool { return atomic.LoadInt32(&restarted) == 1 }, time.Second, time.Millisecond)
}

// TestSelfUpdate_SecondConcurrentCallIsRejectedAsInProgress 覆盖第二个必须成立的
// 场景:同一台机器上,第一次调用还卡在 Apply 里没回来时,第二次调用必须立刻拿到
// IN_PROGRESS,而不是排队等着一起跑(决策:同一台机器同时只允许一次升级在跑)。
func TestSelfUpdate_SecondConcurrentCallIsRejectedAsInProgress(t *testing.T) {
	ctx, activeTurns, deps, _ := selfUpdateFixture(t)
	activeTurns.EXPECT().CountRunning(gomock.Any()).Return(int64(0), nil)
	release := make(chan struct{})
	entered := make(chan struct{})
	deps.Apply = func(context.Context, *update_svc.AgentredRelease, update_svc.ApplyAgentredUpdateOptions) error {
		close(entered)
		<-release
		return nil
	}
	h := handlers.NewSelfUpdateHandlers(*deps)

	var wg sync.WaitGroup
	wg.Add(1)
	var firstResult handlers.SelfUpdateResult
	go func() {
		defer wg.Done()
		res, err := h.Update(ctx, handlers.SelfUpdateParams{})
		require.NoError(t, err)
		firstResult = res
	}()

	<-entered // 第一次调用确认已经进了 Apply,第二次调用此刻必然与它并发。
	second, err := h.Update(ctx, handlers.SelfUpdateParams{})
	require.NoError(t, err)
	assert.False(t, second.Accepted)
	assert.Equal(t, handlers.SelfUpdateRejectInProgress, second.RejectReason)
	assert.NotEmpty(t, second.Message)

	close(release)
	wg.Wait()
	assert.True(t, firstResult.Accepted, "第一次调用不该被第二次调用打断")
}

// TestSelfUpdate_RejectsActiveTurnsWithCount 覆盖第三个必须成立的场景:有活跃轮次
// 时拒绝,且拒绝原因带出确切条数,措辞与 update_svc.ActiveTurnsError(CLI 用的同一个
// 错误类型)逐字相同 —— 决策 22 要求界面与命令行对同一件事说同一句话。
func TestSelfUpdate_RejectsActiveTurnsWithCount(t *testing.T) {
	ctx, activeTurns, deps, _ := selfUpdateFixture(t)
	activeTurns.EXPECT().CountRunning(gomock.Any()).Return(int64(3), nil)
	var applied int32
	deps.Apply = func(context.Context, *update_svc.AgentredRelease, update_svc.ApplyAgentredUpdateOptions) error {
		atomic.AddInt32(&applied, 1)
		return nil
	}
	h := handlers.NewSelfUpdateHandlers(*deps)

	result, err := h.Update(ctx, handlers.SelfUpdateParams{Force: false})
	require.NoError(t, err)
	assert.False(t, result.Accepted)
	assert.Equal(t, handlers.SelfUpdateRejectActiveTurns, result.RejectReason)
	assert.EqualValues(t, 3, result.ActiveTurns)
	assert.Equal(t, (&update_svc.ActiveTurnsError{Count: 3}).Error(), result.Message)
	assert.Zero(t, atomic.LoadInt32(&applied), "被活跃轮次拒绝时不该碰下载/替换")
}

// TestSelfUpdate_ForceCrossesTheActiveTurnGate 覆盖第三个场景的另一半:请求里的
// force 位是唯一能越过活跃轮次闸门的东西。
func TestSelfUpdate_ForceCrossesTheActiveTurnGate(t *testing.T) {
	ctx, activeTurns, deps, _ := selfUpdateFixture(t)
	activeTurns.EXPECT().CountRunning(gomock.Any()).Return(int64(3), nil)
	h := handlers.NewSelfUpdateHandlers(*deps)

	result, err := h.Update(ctx, handlers.SelfUpdateParams{Force: true})
	require.NoError(t, err)
	assert.True(t, result.Accepted)
	assert.Equal(t, handlers.SelfUpdateRejectNone, result.RejectReason)
}

// TestSelfUpdate_RejectsWhenAlreadyLatest 覆盖第四个必须成立的场景。
func TestSelfUpdate_RejectsWhenAlreadyLatest(t *testing.T) {
	ctx, activeTurns, deps, _ := selfUpdateFixture(t)
	activeTurns.EXPECT().CountRunning(gomock.Any()).Return(int64(0), nil)
	deps.Resolve = func(context.Context, update_svc.AgentredReleaseOptions) (*update_svc.AgentredRelease, error) {
		return &update_svc.AgentredRelease{Channel: update_svc.ChannelStable, CurrentVersion: "0.4.0", LatestVersion: "0.4.0", HasUpdate: false}, nil
	}
	var applied int32
	deps.Apply = func(context.Context, *update_svc.AgentredRelease, update_svc.ApplyAgentredUpdateOptions) error {
		atomic.AddInt32(&applied, 1)
		return nil
	}
	h := handlers.NewSelfUpdateHandlers(*deps)

	result, err := h.Update(ctx, handlers.SelfUpdateParams{})
	require.NoError(t, err)
	assert.False(t, result.Accepted)
	assert.Equal(t, handlers.SelfUpdateRejectAlreadyLatest, result.RejectReason)
	assert.Equal(t, "agentred is already up to date on the stable channel.", result.Message)
	assert.Zero(t, atomic.LoadInt32(&applied), "已是最新时不该碰下载/替换")
}

// TestSelfUpdate_RejectsWhenTargetIsNotWritable 覆盖第五个必须成立的场景:目标路径
// 不可写时拒绝,措辞逐字复用 update_svc.TargetNotWritableError(CLI 走的是同一个
// ApplyAgentredUpdate,炸出的是同一个错误类型)。
func TestSelfUpdate_RejectsWhenTargetIsNotWritable(t *testing.T) {
	ctx, activeTurns, deps, _ := selfUpdateFixture(t)
	activeTurns.EXPECT().CountRunning(gomock.Any()).Return(int64(0), nil)
	writeErr := &update_svc.TargetNotWritableError{Path: "/opt/agentred/agentred", Err: errors.New("permission denied")}
	deps.Apply = func(context.Context, *update_svc.AgentredRelease, update_svc.ApplyAgentredUpdateOptions) error {
		return writeErr
	}
	h := handlers.NewSelfUpdateHandlers(*deps)

	result, err := h.Update(ctx, handlers.SelfUpdateParams{})
	require.NoError(t, err)
	assert.False(t, result.Accepted)
	assert.Equal(t, handlers.SelfUpdateRejectNotWritable, result.RejectReason)
	assert.Equal(t, writeErr.Error(), result.Message)
	assert.Equal(t, "0.4.0", result.TargetVersion, "已经解出的发布版本要带出去,即使换不动")
}

// TestSelfUpdate_RejectsWhenDownloadOrVerificationFails 覆盖第六个必须成立的场景:
// 下载或校验失败(这里用 update_svc.ChecksumMismatchError 代表 Apply 内部的校验
// 失败,与解析发布失败共享同一个拒绝原因)。
func TestSelfUpdate_RejectsWhenDownloadOrVerificationFails(t *testing.T) {
	ctx, activeTurns, deps, _ := selfUpdateFixture(t)
	activeTurns.EXPECT().CountRunning(gomock.Any()).Return(int64(0), nil)
	mismatchErr := &update_svc.ChecksumMismatchError{AssetName: "agentred-0.4.0-linux-amd64.tar.gz", Expected: "aaa", Actual: "bbb"}
	deps.Apply = func(context.Context, *update_svc.AgentredRelease, update_svc.ApplyAgentredUpdateOptions) error {
		return mismatchErr
	}
	h := handlers.NewSelfUpdateHandlers(*deps)

	result, err := h.Update(ctx, handlers.SelfUpdateParams{})
	require.NoError(t, err)
	assert.False(t, result.Accepted)
	assert.Equal(t, handlers.SelfUpdateRejectDownloadFailed, result.RejectReason)
	assert.Equal(t, mismatchErr.Error(), result.Message)
}

// TestSelfUpdate_RejectsWhenReleaseResolutionFails 覆盖下载校验失败的另一条入口:
// 连发布都解析不出来(网络不通 / 找不到当前平台的资产),同样落到 DOWNLOAD_FAILED。
func TestSelfUpdate_RejectsWhenReleaseResolutionFails(t *testing.T) {
	ctx, activeTurns, deps, _ := selfUpdateFixture(t)
	activeTurns.EXPECT().CountRunning(gomock.Any()).Return(int64(0), nil)
	resolveErr := errors.New("resolve agentred release failed: dial tcp: no route to host")
	deps.Resolve = func(context.Context, update_svc.AgentredReleaseOptions) (*update_svc.AgentredRelease, error) {
		return nil, resolveErr
	}
	h := handlers.NewSelfUpdateHandlers(*deps)

	result, err := h.Update(ctx, handlers.SelfUpdateParams{})
	require.NoError(t, err)
	assert.False(t, result.Accepted)
	assert.Equal(t, handlers.SelfUpdateRejectDownloadFailed, result.RejectReason)
	assert.Equal(t, resolveErr.Error(), result.Message)
	assert.Empty(t, result.TargetVersion, "连发布都没解出来,不该报出目标版本")
}

// TestSelfUpdate_StaysInProgressUntilTheRestartTakesEffect 覆盖并发闸门的另一半:
// 受理**之后**这台机器仍然处在「一次升级正在进行中」——二进制已经换掉,进程还在等着
// 退出重启(Restart 是异步的,它给应答留出被写回连接的时间)。这段时间里放第二次调用
// 进来,它会重新解析发布(此刻还在跑的进程报的仍是旧版本,于是 HasUpdate 仍为真)、
// 重新下载并再替换一遍;而第一次调度的那次退出会在中途把进程杀掉,连
// ApplyAgentredUpdate 用来清理下载与解压目录的 defer 都跑不到,安装目录里因此留下
// 半个下载。
//
// 判据只有一条:一旦受理并安排了重启,后续调用一律 IN_PROGRESS。没有安排重启
// (Restart 为 nil,未接线的装配)时不能永久上锁——那会让这台机器再也升不了级。
func TestSelfUpdate_StaysInProgressUntilTheRestartTakesEffect(t *testing.T) {
	ctx, activeTurns, deps, _ := selfUpdateFixture(t)
	activeTurns.EXPECT().CountRunning(gomock.Any()).Return(int64(0), nil).AnyTimes()
	var applied int32
	deps.Apply = func(context.Context, *update_svc.AgentredRelease, update_svc.ApplyAgentredUpdateOptions) error {
		atomic.AddInt32(&applied, 1)
		return nil
	}
	// 生产实现会让进程退出;替身只记一次调用,于是那段「已经换完、还没退出」的窗口
	// 在测试里一直开着,正好是要断言的那一段。
	deps.Restart = func() {}
	h := handlers.NewSelfUpdateHandlers(*deps)

	first, err := h.Update(ctx, handlers.SelfUpdateParams{})
	require.NoError(t, err)
	require.True(t, first.Accepted)

	second, err := h.Update(ctx, handlers.SelfUpdateParams{})
	require.NoError(t, err)
	assert.False(t, second.Accepted)
	assert.Equal(t, handlers.SelfUpdateRejectInProgress, second.RejectReason)
	assert.Equal(t, int32(1), atomic.LoadInt32(&applied),
		"重启还没生效之前不该再替换一次目标文件")
}

// TestSelfUpdate_WithoutARestartTheGateReopens 是上一条的反面:没有 Restart 就没有
// 「正在等着重启」这件事,闸门必须照常打开——否则一个没接线重启的装配会在第一次
// 升级之后永久拒绝所有后续调用。
func TestSelfUpdate_WithoutARestartTheGateReopens(t *testing.T) {
	ctx, activeTurns, deps, _ := selfUpdateFixture(t)
	activeTurns.EXPECT().CountRunning(gomock.Any()).Return(int64(0), nil).Times(2)
	deps.Restart = nil
	h := handlers.NewSelfUpdateHandlers(*deps)

	first, err := h.Update(ctx, handlers.SelfUpdateParams{})
	require.NoError(t, err)
	require.True(t, first.Accepted)

	second, err := h.Update(ctx, handlers.SelfUpdateParams{})
	require.NoError(t, err)
	assert.True(t, second.Accepted, "没有重启在等,闸门不该关着不放人")
}
