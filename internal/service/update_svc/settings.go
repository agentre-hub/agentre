package update_svc

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/agentre-hub/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo"
)

// GetChannel 读取持久化的更新通道；未设置时返回 DefaultUpdateChannel。
func GetChannel(ctx context.Context) (string, error) {
	item, err := app_setting_repo.AppSetting().Get(ctx, app_setting_entity.KeyUpdateChannel)
	if err != nil {
		return "", err
	}
	if item == nil || strings.TrimSpace(item.Value) == "" {
		return app_setting_entity.DefaultUpdateChannel, nil
	}
	return strings.TrimSpace(item.Value), nil
}

// SetChannel 持久化更新通道；非法值返回 InvalidParameter 错误。
func SetChannel(ctx context.Context, channel string) error {
	channel = strings.TrimSpace(channel)
	if err := app_setting_entity.ValidateUpdateChannel(ctx, channel); err != nil {
		return err
	}
	return app_setting_repo.AppSetting().Set(ctx, &app_setting_entity.AppSetting{
		Key:        app_setting_entity.KeyUpdateChannel,
		Value:      channel,
		Updatetime: time.Now().UnixMilli(),
	})
}

// GetMirror 读取持久化的下载镜像前缀；未设置时返回空串（直连 GitHub）。
func GetMirror(ctx context.Context) (string, error) {
	item, err := app_setting_repo.AppSetting().Get(ctx, app_setting_entity.KeyDownloadMirror)
	if err != nil {
		return "", err
	}
	if item == nil {
		return "", nil
	}
	return strings.TrimSpace(item.Value), nil
}

// SetMirror 持久化下载镜像前缀；空串表示恢复直连。
// 不做 URL 合法性校验：用户可能配置自建反代，前端做基础格式提示即可。
func SetMirror(ctx context.Context, mirror string) error {
	return app_setting_repo.AppSetting().Set(ctx, &app_setting_entity.AppSetting{
		Key:        app_setting_entity.KeyDownloadMirror,
		Value:      strings.TrimSpace(mirror),
		Updatetime: time.Now().UnixMilli(),
	})
}

// GetLastUpdateCheck 读取上次"检查更新"的毫秒 epoch 时间戳；未设置或非法返回 0。
func GetLastUpdateCheck(ctx context.Context) (int64, error) {
	item, err := app_setting_repo.AppSetting().Get(ctx, app_setting_entity.KeyLastUpdateCheck)
	if err != nil {
		return 0, err
	}
	if item == nil {
		return 0, nil
	}
	ts, parseErr := strconv.ParseInt(strings.TrimSpace(item.Value), 10, 64)
	if parseErr != nil {
		// 容错：存储被外部工具改坏的非法值不应让自动检查链路崩溃，回落到 0 视作"从未检查过"。
		return 0, nil //nolint:nilerr
	}
	return ts, nil
}

// SetLastUpdateCheck 写入上次"检查更新"的毫秒 epoch 时间戳。
func SetLastUpdateCheck(ctx context.Context, ts int64) error {
	return app_setting_repo.AppSetting().Set(ctx, &app_setting_entity.AppSetting{
		Key:        app_setting_entity.KeyLastUpdateCheck,
		Value:      strconv.FormatInt(ts, 10),
		Updatetime: time.Now().UnixMilli(),
	})
}
