package update_svc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
)

func TestShouldCheck(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

	convey.Convey("ShouldCheck 按触发源与上次检查时间判定是否真的发起检查", t, func() {
		convey.Convey("用户主动触发的检查绕过节流", func() {
			// 刚查过 1 分钟,自动触发会被挡掉;用户此刻在等结果,必须放行。
			// 切换更新通道后的重查也走这一条(前端切完通道即调 CheckForUpdate)。
			justChecked := now.Add(-time.Minute)

			assert.True(t, ShouldCheck(TriggerManual, justChecked, now),
				"点「检查更新」必须立刻查")
		})

		convey.Convey("自动触发在节流窗口内不查", func() {
			justChecked := now.Add(-time.Minute)

			for _, trigger := range []CheckTrigger{TriggerStartup, TriggerTick, TriggerFocus} {
				assert.False(t, ShouldCheck(trigger, justChecked, now),
					"距上次检查不足 %s,%s 不应发起请求", AutoCheckInterval, trigger)
			}
		})

		convey.Convey("自动触发超过节流窗口后放行", func() {
			stale := now.Add(-AutoCheckInterval - time.Second)

			for _, trigger := range []CheckTrigger{TriggerStartup, TriggerTick, TriggerFocus} {
				assert.True(t, ShouldCheck(trigger, stale, now), "%s 应发起请求", trigger)
			}
		})

		convey.Convey("恰好等于节流窗口的边界放行", func() {
			// >= 而不是 >:24h ticker 与 24h 节流同频时,差一纳秒就会整轮跳过。
			boundary := now.Add(-AutoCheckInterval)

			assert.True(t, ShouldCheck(TriggerTick, boundary, now))
		})

		convey.Convey("从未检查过时放行", func() {
			// GetLastUpdateCheck 未设置或值损坏时返回 0,换算成零值时间。
			assert.True(t, ShouldCheck(TriggerStartup, time.Unix(0, 0), now))
			assert.True(t, ShouldCheck(TriggerStartup, time.Time{}, now))
		})

		convey.Convey("上次检查时间在未来时放行", func() {
			// 时钟回拨/存储被外部改坏会留下未来时间戳。若按 now.Sub(last) < interval 判定,
			// 它是负数 —— 自动检查会一直被挡到系统时间追上为止。
			future := now.Add(72 * time.Hour)

			assert.True(t, ShouldCheck(TriggerTick, future, now))
		})

		convey.Convey("未知触发源按自动处理,不绕过节流", func() {
			justChecked := now.Add(-time.Minute)

			assert.False(t, ShouldCheck(CheckTrigger("whatever"), justChecked, now))
		})
	})
}

// scheduleFake 记录 RunCheck 对协作方的每一次调用。用手写 fake 而不是 mock_update_svc：
// 后者 import 本包，在包内测试里会构成导入环（package 内已有 fakeService 先例）。
type scheduleFake struct {
	fakeService

	lastCheck    int64
	lastCheckErr error
	channel      string
	mirror       string
	info         *UpdateInfo
	checkErr     error
	setErr       error

	checkedChannel string
	checkedMirror  string
	checkCalls     int
	channelCalls   int
	persisted      []int64
}

func (f *scheduleFake) GetLastUpdateCheck(_ context.Context) (int64, error) {
	return f.lastCheck, f.lastCheckErr
}

func (f *scheduleFake) GetChannel(_ context.Context) (string, error) {
	f.channelCalls++
	return f.channel, nil
}

func (f *scheduleFake) GetMirror(_ context.Context) (string, error) { return f.mirror, nil }

func (f *scheduleFake) CheckForUpdate(channel, mirror string) (*UpdateInfo, error) {
	f.checkCalls++
	f.checkedChannel, f.checkedMirror = channel, mirror
	return f.info, f.checkErr
}

func (f *scheduleFake) SetLastUpdateCheck(_ context.Context, ts int64) error {
	f.persisted = append(f.persisted, ts)
	return f.setErr
}

func setupRunCheck(t *testing.T, at time.Time, f *scheduleFake) {
	t.Helper()

	originalSvc := Update()
	t.Cleanup(func() { RegisterUpdate(originalSvc) })
	RegisterUpdate(f)

	originalNow := timeNow
	t.Cleanup(func() { timeNow = originalNow })
	timeNow = func() time.Time { return at }
}

func TestRunCheck(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

	convey.Convey("RunCheck 判定→检查→落上次检查时间", t, func() {
		ctx := context.Background()

		convey.Convey("被节流挡住时不读通道、不发请求、不写时间戳", func() {
			f := &scheduleFake{lastCheck: now.Add(-time.Minute).Unix()}
			setupRunCheck(t, now, f)

			info, err := RunCheck(ctx, TriggerTick)

			assert.NoError(t, err)
			assert.Nil(t, info, "跳过时返回 nil,调用方据此不广播任何东西")
			assert.Zero(t, f.channelCalls)
			assert.Zero(t, f.checkCalls)
			assert.Empty(t, f.persisted)
		})

		convey.Convey("放行时按当前通道与镜像检查,并写回时间戳", func() {
			want := &UpdateInfo{HasUpdate: true, LatestVersion: "v0.9.2"}
			f := &scheduleFake{
				lastCheck: 0,
				channel:   ChannelBeta,
				mirror:    "https://mirror.example/",
				info:      want,
			}
			setupRunCheck(t, now, f)

			info, err := RunCheck(ctx, TriggerStartup)

			assert.NoError(t, err)
			assert.Equal(t, want, info)
			assert.Equal(t, ChannelBeta, f.checkedChannel)
			assert.Equal(t, "https://mirror.example/", f.checkedMirror)
			assert.Equal(t, []int64{now.Unix()}, f.persisted)
		})

		convey.Convey("用户主动检查绕过节流", func() {
			f := &scheduleFake{
				lastCheck: now.Add(-time.Minute).Unix(),
				channel:   ChannelStable,
				info:      &UpdateInfo{},
			}
			setupRunCheck(t, now, f)

			_, err := RunCheck(ctx, TriggerManual)

			assert.NoError(t, err)
			assert.Equal(t, 1, f.checkCalls)
		})

		convey.Convey("检查失败时把错误交出去,且不推进节流窗口", func() {
			// 失败也写时间戳的话,一次网络抖动会让接下来 24h 都不再尝试。
			boom := errors.New("dial tcp: i/o timeout")
			f := &scheduleFake{channel: ChannelStable, checkErr: boom}
			setupRunCheck(t, now, f)

			info, err := RunCheck(ctx, TriggerTick)

			assert.ErrorIs(t, err, boom)
			assert.Nil(t, info)
			assert.Empty(t, f.persisted)
		})

		convey.Convey("读上次检查时间失败时不发请求", func() {
			f := &scheduleFake{lastCheckErr: errors.New("db closed")}
			setupRunCheck(t, now, f)

			info, err := RunCheck(ctx, TriggerTick)

			assert.Error(t, err)
			assert.Nil(t, info)
			assert.Zero(t, f.checkCalls)
		})

		convey.Convey("时间戳写失败不影响本次结果", func() {
			want := &UpdateInfo{HasUpdate: false}
			f := &scheduleFake{channel: ChannelStable, info: want, setErr: errors.New("db closed")}
			setupRunCheck(t, now, f)

			info, err := RunCheck(ctx, TriggerFocus)

			assert.NoError(t, err)
			assert.Equal(t, want, info)
		})
	})
}
