// Package app_setting_repo 提供 App 全局 key-value 设置项的持久化访问。
package app_setting_repo

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/agentre-hub/agentre/internal/model/entity/app_setting_entity"
)

//go:generate mockgen -source app_setting.go -destination mock_app_setting_repo/mock_app_setting.go

// AppSettingRepo App 设置项仓储。
type AppSettingRepo interface {
	// Get 按 key 查找；未命中返回 (nil, nil)。
	Get(ctx context.Context, key string) (*app_setting_entity.AppSetting, error)
	// Set Upsert 一行；调用方负责传 Updatetime。
	Set(ctx context.Context, s *app_setting_entity.AppSetting) error
	// List 列出所有设置项；按 key 升序。
	List(ctx context.Context) ([]*app_setting_entity.AppSetting, error)
	// Delete 按 key 删除一行，返回被删掉的行数；未命中返回 0 而不是报错。
	Delete(ctx context.Context, key string) (int64, error)
}

var defaultAppSetting AppSettingRepo

// AppSetting 取默认仓储单例。
func AppSetting() AppSettingRepo { return defaultAppSetting }

// RegisterAppSetting 注入仓储实现，由 bootstrap 调用一次。
func RegisterAppSetting(impl AppSettingRepo) { defaultAppSetting = impl }

type appSettingRepo struct{}

// NewAppSetting 构造默认 GORM 实现。
func NewAppSetting() AppSettingRepo { return &appSettingRepo{} }

func (r *appSettingRepo) Get(ctx context.Context, key string) (*app_setting_entity.AppSetting, error) {
	out := &app_setting_entity.AppSetting{}
	err := db.Ctx(ctx).Where("`key` = ?", key).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *appSettingRepo) Set(ctx context.Context, s *app_setting_entity.AppSetting) error {
	return db.Ctx(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updatetime"}),
	}).Create(s).Error
}

func (r *appSettingRepo) List(ctx context.Context) ([]*app_setting_entity.AppSetting, error) {
	var rows []*app_setting_entity.AppSetting
	if err := db.Ctx(ctx).Order("`key` ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *appSettingRepo) Delete(ctx context.Context, key string) (int64, error) {
	res := db.Ctx(ctx).Where("`key` = ?", key).Delete(&app_setting_entity.AppSetting{})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}
