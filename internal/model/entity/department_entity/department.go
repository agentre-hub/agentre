// Package department_entity 维护部门的充血实体。
package department_entity

import (
	"context"
	"strings"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// Department 一条部门记录。
type Department struct {
	ID          int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Name        string `gorm:"column:name;type:text;not null"`
	Description string `gorm:"column:description;type:text;not null;default:''"`
	Icon        string `gorm:"column:icon;type:text;not null;default:''"`
	AccentColor string `gorm:"column:accent_color;type:text;not null;default:''"`
	ParentID    int64  `gorm:"column:parent_id;type:bigint;not null;default:0"`
	LeadAgentID int64  `gorm:"column:lead_agent_id;type:bigint;not null;default:0"`
	SortOrder   int    `gorm:"column:sort_order;type:int;not null;default:0"`
	Status      int    `gorm:"column:status;type:int;not null;default:1"`
	Createtime  int64  `gorm:"column:createtime;type:bigint;not null;default:0"`
	Updatetime  int64  `gorm:"column:updatetime;type:bigint;not null;default:0"`
	// SyncMeta 账号级同步元数据（R1，366 行）。
	syncmeta_entity.SyncMeta `gorm:"embedded"`
}

// TableName 绑定表名。
func (*Department) TableName() string { return "departments" }

// IsActive 是否处于启用态。
func (d *Department) IsActive() bool { return d != nil && d.Status == consts.ACTIVE }

// IsRoot 顶级部门 = parent_id == 0。
func (d *Department) IsRoot() bool { return d != nil && d.ParentID == 0 }

var allowedColors = map[string]struct{}{
	"":         {},
	"agent-1":  {},
	"agent-2":  {},
	"agent-3":  {},
	"agent-4":  {},
	"agent-5":  {},
	"agent-6":  {},
	"agent-7":  {},
	"agent-8":  {},
	"agent-9":  {},
	"agent-10": {},
	"agent-11": {},
	"agent-12": {},
	"agent-13": {},
	"agent-14": {},
	"agent-15": {},
	"agent-16": {},
	"neutral":  {},
}

// Check 关键字段校验。不校验 parent 是否存在 / 是否成环 / lead 归属 — 那是 service 职责。
func (d *Department) Check(ctx context.Context) error {
	if d == nil {
		return i18n.NewError(ctx, code.DepartmentNotFound)
	}
	if strings.TrimSpace(d.Name) == "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	if d.ParentID < 0 {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	if _, ok := allowedColors[d.AccentColor]; !ok {
		return i18n.NewError(ctx, code.DepartmentInvalidColor)
	}
	return nil
}
