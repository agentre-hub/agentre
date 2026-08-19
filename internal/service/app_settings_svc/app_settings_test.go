package app_settings_svc

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/agentre-ai/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-ai/agentre/internal/pkg/httpgateway"
	"github.com/agentre-ai/agentre/internal/repository/app_setting_repo"
	"github.com/agentre-ai/agentre/internal/repository/app_setting_repo/mock_app_setting_repo"
)

// fakeGateway 模拟 httpgateway.Lifecycle，记录 Restart / ApplyAddr 调用次数与最终参数。
type fakeGateway struct {
	status     httpgateway.GatewayStatus
	restartErr error

	restartCalls atomic.Int32
	applyHost    string
	applyPort    int
	applyCalls   atomic.Int32
}

func (f *fakeGateway) Status() httpgateway.GatewayStatus { return f.status }

func (f *fakeGateway) Restart(_ context.Context) error {
	f.restartCalls.Add(1)
	if f.restartErr != nil {
		return f.restartErr
	}
	f.status = httpgateway.GatewayStatus{State: "running", URL: "http://127.0.0.1:60080", Routes: httpgateway.DefaultRoutes()}
	return nil
}

func (f *fakeGateway) RegisterMCP(_ string, _ http.Handler) {}

func (f *fakeGateway) ApplyAddr(host string, port int) {
	f.applyHost = host
	f.applyPort = port
	f.applyCalls.Add(1)
}

func setupSvcTest(t *testing.T) (context.Context, *mock_app_setting_repo.MockAppSettingRepo, *fakeGateway, *appSettingsSvc) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	repo := mock_app_setting_repo.NewMockAppSettingRepo(ctrl)
	app_setting_repo.RegisterAppSetting(repo)

	gw := &fakeGateway{status: httpgateway.GatewayStatus{State: "running", URL: "http://127.0.0.1:60080"}}
	svc := &appSettingsSvc{
		now:     func() int64 { return 1700000000 },
		gateway: gw,
	}
	return context.Background(), repo, gw, svc
}

func TestGet(t *testing.T) {
	convey.Convey("Get setting", t, func() {
		ctx, repo, _, svc := setupSvcTest(t)

		convey.Convey("命中", func() {
			repo.EXPECT().Get(gomock.Any(), "proxy.listen_port").
				Return(&app_setting_entity.AppSetting{Key: "proxy.listen_port", Value: "60080"}, nil)
			resp, err := svc.Get(ctx, &GetRequest{Key: "proxy.listen_port"})
			assert.NoError(t, err)
			assert.Equal(t, "60080", resp.Value)
		})

		convey.Convey("未命中", func() {
			repo.EXPECT().Get(gomock.Any(), "missing").Return(nil, nil)
			_, err := svc.Get(ctx, &GetRequest{Key: "missing"})
			assert.Error(t, err)
		})

		convey.Convey("空 key 直接拒", func() {
			_, err := svc.Get(ctx, &GetRequest{Key: "  "})
			assert.Error(t, err)
		})
	})
}

func TestUpdate_BatchProxyTriggersOneRestart(t *testing.T) {
	convey.Convey("Update host+port 只触发一次 Restart", t, func() {
		ctx, repo, gw, svc := setupSvcTest(t)
		repo.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil).Times(2)
		repo.EXPECT().Get(gomock.Any(), app_setting_entity.KeyProxyListenHost).
			Return(&app_setting_entity.AppSetting{Key: "proxy.listen_host", Value: "127.0.0.1"}, nil)
		repo.EXPECT().Get(gomock.Any(), app_setting_entity.KeyProxyListenPort).
			Return(&app_setting_entity.AppSetting{Key: "proxy.listen_port", Value: "60080"}, nil)

		_, err := svc.Update(ctx, &UpdateRequest{Entries: []SettingEntry{
			{Key: "proxy.listen_host", Value: "127.0.0.1"},
			{Key: "proxy.listen_port", Value: "60080"},
		}})
		assert.NoError(t, err)
		assert.Equal(t, int32(1), gw.restartCalls.Load(), "Restart 必须只调一次")
		assert.Equal(t, "127.0.0.1", gw.applyHost)
		assert.Equal(t, 60080, gw.applyPort)
	})
}

func TestUpdate_RejectsInvalidPort(t *testing.T) {
	convey.Convey("端口非法直接拒，不入库", t, func() {
		ctx, _, gw, svc := setupSvcTest(t)
		_, err := svc.Update(ctx, &UpdateRequest{Entries: []SettingEntry{
			{Key: "proxy.listen_port", Value: "70000"},
		}})
		assert.Error(t, err)
		assert.Equal(t, int32(0), gw.restartCalls.Load())
	})
}

func TestUpdate_RejectsInvalidHost(t *testing.T) {
	convey.Convey("host 非 IP 拒", t, func() {
		ctx, _, _, svc := setupSvcTest(t)
		_, err := svc.Update(ctx, &UpdateRequest{Entries: []SettingEntry{
			{Key: "proxy.listen_host", Value: "localhost"},
		}})
		assert.Error(t, err)
	})
}

func TestUpdate_RejectsUnknownKey(t *testing.T) {
	convey.Convey("未启用的 key 直接拒", t, func() {
		ctx, _, _, svc := setupSvcTest(t)
		_, err := svc.Update(ctx, &UpdateRequest{Entries: []SettingEntry{
			{Key: "theme", Value: "dark"},
		}})
		assert.Error(t, err)
	})
}

func TestUpdate_RestartFailureMapsToCode(t *testing.T) {
	convey.Convey("Restart 失败 → AppGatewayRestartFailed", t, func() {
		ctx, repo, gw, svc := setupSvcTest(t)
		gw.restartErr = errors.New("bind: address in use")
		repo.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil)
		repo.EXPECT().Get(gomock.Any(), app_setting_entity.KeyProxyListenHost).Return(nil, nil)
		repo.EXPECT().Get(gomock.Any(), app_setting_entity.KeyProxyListenPort).Return(nil, nil)

		_, err := svc.Update(ctx, &UpdateRequest{Entries: []SettingEntry{
			{Key: "proxy.listen_port", Value: "60080"},
		}})
		assert.Error(t, err)
		assert.Equal(t, int32(1), gw.restartCalls.Load())
	})
}

func TestGetGatewayStatus(t *testing.T) {
	convey.Convey("透传 gateway.Status", t, func() {
		ctx, _, _, svc := setupSvcTest(t)
		st, err := svc.GetGatewayStatus(ctx)
		assert.NoError(t, err)
		assert.Equal(t, "running", st.State)
		assert.Equal(t, "http://127.0.0.1:60080", st.URL)
	})
}

func TestGetGatewayStatus_GatewayNil(t *testing.T) {
	convey.Convey("gateway 未注入仍返回 stopped 而不是 panic", t, func() {
		svc := &appSettingsSvc{now: func() int64 { return 0 }}
		st, err := svc.GetGatewayStatus(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, "stopped", st.State)
	})
}

func TestRestartGateway(t *testing.T) {
	convey.Convey("RestartGateway 重启并返新 status", t, func() {
		ctx, repo, gw, svc := setupSvcTest(t)
		repo.EXPECT().Get(gomock.Any(), app_setting_entity.KeyProxyListenHost).
			Return(&app_setting_entity.AppSetting{Key: "proxy.listen_host", Value: "127.0.0.1"}, nil)
		repo.EXPECT().Get(gomock.Any(), app_setting_entity.KeyProxyListenPort).
			Return(&app_setting_entity.AppSetting{Key: "proxy.listen_port", Value: "0"}, nil)

		resp, err := svc.RestartGateway(ctx)
		assert.NoError(t, err)
		assert.Equal(t, int32(1), gw.restartCalls.Load())
		assert.Equal(t, "running", resp.Status.State)
	})
}

func TestRestartGateway_DefaultsToFixedPort(t *testing.T) {
	convey.Convey("host/port 都缺失时回落到 127.0.0.1:DefaultProxyListenPort", t, func() {
		ctx, repo, gw, svc := setupSvcTest(t)
		repo.EXPECT().Get(gomock.Any(), app_setting_entity.KeyProxyListenHost).Return(nil, nil)
		repo.EXPECT().Get(gomock.Any(), app_setting_entity.KeyProxyListenPort).Return(nil, nil)

		_, err := svc.RestartGateway(ctx)
		assert.NoError(t, err)
		assert.Equal(t, "127.0.0.1", gw.applyHost)
		assert.Equal(t, app_setting_entity.DefaultProxyListenPort, gw.applyPort)
	})
}

func TestUpdate_NotifyKeys(t *testing.T) {
	convey.Convey("Update 通知设置 key", t, func() {
		ctx, repo, gw, svc := setupSvcTest(t)

		convey.Convey("合法 bool 写入,不触发 gateway", func() {
			repo.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil).Times(1)
			_, err := svc.Update(ctx, &UpdateRequest{Entries: []SettingEntry{
				{Key: app_setting_entity.KeyNotifySystem, Value: "true"},
			}})
			assert.NoError(t, err)
			assert.Equal(t, int32(0), gw.restartCalls.Load(), "通知 key 不应触发 Restart")
		})

		convey.Convey("非法 bool 直接拒,不落库", func() {
			_, err := svc.Update(ctx, &UpdateRequest{Entries: []SettingEntry{
				{Key: app_setting_entity.KeyNotifyEnabled, Value: "maybe"},
			}})
			assert.Error(t, err)
		})

		convey.Convey("only_when_unfocused 也是合法 bool", func() {
			repo.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil).Times(1)
			_, err := svc.Update(ctx, &UpdateRequest{Entries: []SettingEntry{
				{Key: app_setting_entity.KeyNotifyOnlyWhenUnfocused, Value: "false"},
			}})
			assert.NoError(t, err)
		})
	})
}

func TestUpdate_SkippedUpdateVersion(t *testing.T) {
	convey.Convey("Update 跳过版本 key", t, func() {
		ctx, repo, gw, svc := setupSvcTest(t)

		convey.Convey("版本号写入,不触发 gateway", func() {
			repo.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil).Times(1)
			_, err := svc.Update(ctx, &UpdateRequest{Entries: []SettingEntry{
				{Key: app_setting_entity.KeySkippedUpdateVersion, Value: "v0.9.2"},
			}})
			assert.NoError(t, err)
			assert.Equal(t, int32(0), gw.restartCalls.Load(), "跳过版本不应触发 Restart")
		})

		convey.Convey("空串合法:表示撤销跳过", func() {
			repo.EXPECT().Set(gomock.Any(), gomock.Any()).Return(nil).Times(1)
			_, err := svc.Update(ctx, &UpdateRequest{Entries: []SettingEntry{
				{Key: app_setting_entity.KeySkippedUpdateVersion, Value: ""},
			}})
			assert.NoError(t, err)
		})
	})
}
