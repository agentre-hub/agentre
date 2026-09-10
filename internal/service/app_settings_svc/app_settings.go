package app_settings_svc

import (
	"context"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/httpgateway"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo"
)

// AppSettingsSvc App 全局设置 + 本地 HTTP 代理生命周期。
type AppSettingsSvc interface {
	Get(ctx context.Context, req *GetRequest) (*GetResponse, error)
	Update(ctx context.Context, req *UpdateRequest) (*UpdateResponse, error)
	GetGatewayStatus(ctx context.Context) (*GatewayStatusResponse, error)
	RestartGateway(ctx context.Context) (*RestartGatewayResponse, error)
}

type appSettingsSvc struct {
	now     func() int64
	gateway httpgateway.Lifecycle
}

var defaultSvc AppSettingsSvc = &appSettingsSvc{
	now: func() int64 { return time.Now().UnixMilli() },
}

// AppSettings 取默认服务单例。
func AppSettings() AppSettingsSvc { return defaultSvc }

// RegisterGateway 由 bootstrap 注入 httpgateway 实例。
func RegisterGateway(g httpgateway.Lifecycle) {
	if s, ok := defaultSvc.(*appSettingsSvc); ok {
		s.gateway = g
	}
}

func (s *appSettingsSvc) Get(ctx context.Context, req *GetRequest) (*GetResponse, error) {
	key := strings.TrimSpace(req.Key)
	if key == "" {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	item, err := app_setting_repo.AppSetting().Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, i18n.NewError(ctx, code.AppSettingNotFound)
	}
	return &GetResponse{Key: item.Key, Value: item.Value}, nil
}

func (s *appSettingsSvc) Update(ctx context.Context, req *UpdateRequest) (*UpdateResponse, error) {
	if req == nil || len(req.Entries) == 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}

	// 1. 先全量校验，避免改了一半就报错。
	touchedProxy := false
	for _, e := range req.Entries {
		key := strings.TrimSpace(e.Key)
		val := strings.TrimSpace(e.Value)
		switch key {
		case app_setting_entity.KeyProxyListenHost:
			if err := app_setting_entity.ValidateProxyHost(ctx, val); err != nil {
				return nil, err
			}
			touchedProxy = true
		case app_setting_entity.KeyProxyListenPort:
			if err := app_setting_entity.ValidateProxyPort(ctx, val); err != nil {
				return nil, err
			}
			touchedProxy = true
		case app_setting_entity.KeyNotifyEnabled,
			app_setting_entity.KeyNotifyOnlyWhenUnfocused,
			app_setting_entity.KeyNotifySystem,
			app_setting_entity.KeyNotifyToast:
			if err := app_setting_entity.ValidateBoolSetting(ctx, val); err != nil {
				return nil, err
			}
		case app_setting_entity.KeySkippedUpdateVersion:
			// 版本号是上游 release tag，本端不做格式校验；空串表示撤销跳过。
		case "":
			return nil, i18n.NewError(ctx, code.InvalidParameter)
		default:
			// 其它 key 暂未启用，给出明确错误避免静默写入。
			return nil, i18n.NewError(ctx, code.AppSettingNotFound)
		}
	}

	// 2. 落库。
	now := s.now()
	for _, e := range req.Entries {
		if err := app_setting_repo.AppSetting().Set(ctx, &app_setting_entity.AppSetting{
			Key:        strings.TrimSpace(e.Key),
			Value:      strings.TrimSpace(e.Value),
			Updatetime: now,
		}); err != nil {
			return nil, err
		}
	}

	// 3. 只在 proxy.* 变了的时候 Restart 一次。
	if touchedProxy && s.gateway != nil {
		host, port, err := s.currentProxyAddr(ctx)
		if err != nil {
			return nil, err
		}
		if applier, ok := s.gateway.(interface{ ApplyAddr(string, int) }); ok {
			applier.ApplyAddr(host, port)
		}
		if err := s.gateway.Restart(ctx); err != nil {
			return nil, i18n.NewError(ctx, code.AppGatewayRestartFailed)
		}
	}

	return &UpdateResponse{}, nil
}

func (s *appSettingsSvc) GetGatewayStatus(_ context.Context) (*GatewayStatusResponse, error) {
	if s.gateway == nil {
		return &GatewayStatusResponse{State: "stopped"}, nil
	}
	st := s.gateway.Status()
	return &st, nil
}

func (s *appSettingsSvc) RestartGateway(ctx context.Context) (*RestartGatewayResponse, error) {
	if s.gateway == nil {
		return &RestartGatewayResponse{Status: GatewayStatusResponse{State: "stopped"}}, nil
	}
	host, port, err := s.currentProxyAddr(ctx)
	if err != nil {
		return nil, err
	}
	if applier, ok := s.gateway.(interface{ ApplyAddr(string, int) }); ok {
		applier.ApplyAddr(host, port)
	}
	if err := s.gateway.Restart(ctx); err != nil {
		return nil, i18n.NewError(ctx, code.AppGatewayRestartFailed)
	}
	st := s.gateway.Status()
	return &RestartGatewayResponse{Status: st}, nil
}

// currentProxyAddr 读 host + port 当前值；缺失走默认 127.0.0.1:DefaultProxyListenPort。
func (s *appSettingsSvc) currentProxyAddr(ctx context.Context) (string, int, error) {
	host := app_setting_entity.DefaultProxyListenHost
	port := app_setting_entity.DefaultProxyListenPort
	if got, err := app_setting_repo.AppSetting().Get(ctx, app_setting_entity.KeyProxyListenHost); err != nil {
		return "", 0, err
	} else if got != nil && strings.TrimSpace(got.Value) != "" {
		host = strings.TrimSpace(got.Value)
	}
	if got, err := app_setting_repo.AppSetting().Get(ctx, app_setting_entity.KeyProxyListenPort); err != nil {
		return "", 0, err
	} else if got != nil && strings.TrimSpace(got.Value) != "" {
		port = app_setting_entity.ParseProxyPort(got.Value)
	}
	return host, port, nil
}
