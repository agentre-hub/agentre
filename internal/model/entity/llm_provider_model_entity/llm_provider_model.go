// Package llm_provider_model_entity 维护 LLM 供应商下稳定模型（1 → N）的充血实体。
//
// 一条 LLMProviderModel = 一个属于某 Provider 的稳定模型：
//   - ModelKey 永久稳定、不可修改，承担 Backend / Session / Route 的跨实体引用；
//   - ModelID  是执行时发送给上游的字符串，可编辑（被引用时由 service 先做影响确认）；
//   - Enabled  独立于 Status（软删除），表示该模型当前是否可被选择 / 执行。
package llm_provider_model_entity

import (
	"context"
	"strings"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/code"
)

const (
	// EnabledOff / EnabledOn 是 Model 独立的可运行状态（与 status 软删除解耦）。
	// enabled=0 的模型仍保留原 target（fixed-model 显示失效），可编辑、可重新启用。
	EnabledOff = 0
	EnabledOn  = 1
)

// LLMProviderModel 一条 Provider 下的稳定模型记录。
type LLMProviderModel struct {
	ID            int64  `gorm:"column:id;primaryKey;autoIncrement"`
	ProviderID    int64  `gorm:"column:provider_id;type:int;not null"`
	ModelKey      string `gorm:"column:model_key;type:text;not null;default:'';uniqueIndex:uniq_llm_provider_models_model_key"`
	ModelID       string `gorm:"column:model_id;type:text;not null;default:''"`
	Name          string `gorm:"column:name;type:text;not null;default:''"`
	ContextWindow int    `gorm:"column:context_window;type:int;not null;default:0"`
	MaxOutput     int    `gorm:"column:max_output;type:int;not null;default:0"`
	Enabled       int    `gorm:"column:enabled;type:int;not null;default:1"`
	Status        int    `gorm:"column:status;type:int;not null;default:1"`
	Createtime    int64  `gorm:"column:createtime;type:bigint;not null;default:0"`
	Updatetime    int64  `gorm:"column:updatetime;type:bigint;not null;default:0"`
}

// TableName 绑定表名。
func (*LLMProviderModel) TableName() string { return "llm_provider_models" }

// IsActive 是否处于启用态（未被软删除）。
func (m *LLMProviderModel) IsActive() bool { return m != nil && m.Status == consts.ACTIVE }

// IsEnabled 是否可被新选择 / 用于执行（独立于软删除状态）。
func (m *LLMProviderModel) IsEnabled() bool { return m != nil && m.Enabled == EnabledOn }

// Check 校验关键字段：nil / 空 model_key / 空 model_id 直接返回业务错误。
// model_key 承担稳定引用，model_id 是执行时发给上游的真实模型，两者都不可为空。
func (m *LLMProviderModel) Check(ctx context.Context) error {
	if m == nil {
		return i18n.NewError(ctx, code.LLMProviderNotFound)
	}
	if strings.TrimSpace(m.ModelKey) == "" || strings.TrimSpace(m.ModelID) == "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	return nil
}
