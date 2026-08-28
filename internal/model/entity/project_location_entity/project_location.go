// Package project_location_entity 维护 ProjectLocation 的充血实体。
//
// ProjectLocation 装载一份"远端 agentred 下，某个 project 的绝对路径"。本地 path
// 仍住在 projects.path（避免双源同步）。账号内自然键是 (project, device_fingerprint)
// ——本表不存空 device_fingerprint 的行（决策 26）。
//
// device_id 保留列与取值形态（空串或 paired_agentreds.id 的字符串形式），但语义
// 降级为「由指纹解析出的本地缓存」：本机配对表里查不到该指纹时留空——R2b 下那台
// 机器上的路径记录本机不参与解析、也不出现在路径列表里；配对后自动回填、解除
// 配对后清空，行本身与它的 path 不丢。它不再是身份，也不再受唯一约束管辖。
package project_location_entity

import (
	"context"
	"strings"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// ProjectLocation 远端 agentred 下某个 project 的绝对路径记录。
type ProjectLocation struct {
	ID                int64  `gorm:"column:id;primaryKey;autoIncrement"`
	ProjectID         int64  `gorm:"column:project_id;type:bigint;not null"`
	DeviceID          string `gorm:"column:device_id;type:text;not null;default:''"`
	DeviceFingerprint string `gorm:"column:device_fingerprint;type:text;not null;default:''"`
	Path              string `gorm:"column:path;type:text;not null"`
	Status            int    `gorm:"column:status;type:int;not null;default:1"`
	Createtime        int64  `gorm:"column:createtime;type:bigint;not null;default:0"`
	Updatetime        int64  `gorm:"column:updatetime;type:bigint;not null;default:0"`
	// SyncMeta 账号级同步元数据（R1，366 行；决策 26 的自然键另见 device_fingerprint）。
	syncmeta_entity.SyncMeta `gorm:"embedded"`
}

func (*ProjectLocation) TableName() string { return "project_locations" }

func (p *ProjectLocation) IsActive() bool { return p != nil && p.Status == consts.ACTIVE }

// IsUnresolved 报告这一行的 device_id 缓存是否为空——即本机配对表里当前查不到
// DeviceFingerprint 那一行（R2b：未配对）。取代原先的 IsLocal()：本表从不存放
// 本机自己的路径（那住在 projects.path），device_id 降级为缓存后，"是否为空"
// 问的不再是"是不是本机"，而是"这个指纹本机有没有解析出配对行"。
func (p *ProjectLocation) IsUnresolved() bool { return p != nil && p.DeviceID == "" }

// Check 字段校验。绝对路径与否、指纹是否必填的检查放这里；外键存在性放 svc 层
// （依赖 paired_agentreds repo）。
func (p *ProjectLocation) Check(ctx context.Context) error {
	if p == nil {
		return i18n.NewError(ctx, code.ProjectLocationNotFound)
	}
	if p.ProjectID <= 0 {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	if strings.TrimSpace(p.Path) == "" {
		return i18n.NewError(ctx, code.ProjectLocationInvalidPath)
	}
	if !strings.HasPrefix(p.Path, "/") {
		return i18n.NewError(ctx, code.ProjectLocationInvalidPath)
	}
	if strings.TrimSpace(p.DeviceFingerprint) == "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	return nil
}
