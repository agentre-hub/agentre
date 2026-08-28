// Package project_entity 维护 Project 的充血实体。
//
// Project 承担「工作上下文」语义：名字 + 本地路径 + 成员 Agent。
package project_entity

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// Project 一条项目记录。
type Project struct {
	ID          int64  `gorm:"column:id;primaryKey;autoIncrement"`
	ParentID    int64  `gorm:"column:parent_id;type:bigint;not null;default:0"`
	Name        string `gorm:"column:name;type:text;not null"`
	Icon        string `gorm:"column:icon;type:text;not null;default:''"`
	Color       string `gorm:"column:color;type:text;not null;default:''"`
	Description string `gorm:"column:description;type:text;not null;default:''"`
	Path        string `gorm:"column:path;type:text;not null"`
	// LocalPathMissing 为 true 表示"本机未配置路径"(R10、决策 21)：项目行照常
	// 落地(名称/图标/颜色/层级/排序都在)，只是本机这一档目前没有可用的 cwd。
	// 判据是这个显式状态位，不是 Path 是否为空 —— Path 只应在这个位为 true
	// 时才是空串，绝不存在"Path 为空却被当成已配置项目"的情形。
	LocalPathMissing bool  `gorm:"column:local_path_missing;type:boolean;not null;default:0"`
	SortOrder        int   `gorm:"column:sort_order;type:int;not null;default:0"`
	Status           int   `gorm:"column:status;type:int;not null;default:1"`
	Createtime       int64 `gorm:"column:createtime;type:bigint;not null;default:0"`
	Updatetime       int64 `gorm:"column:updatetime;type:bigint;not null;default:0"`
	// SyncMeta 账号级同步元数据（R1，366 行）。注意 Path 本身不加同步元数据、不
	// 参与同步载荷（决策 6）——这一份是整行（名称/图标/颜色/层级/排序等）的。
	syncmeta_entity.SyncMeta `gorm:"embedded"`
}

func (*Project) TableName() string { return "projects" }

func (p *Project) IsActive() bool   { return p != nil && p.Status == consts.ACTIVE }
func (p *Project) IsTopLevel() bool { return p != nil && p.ParentID == 0 }

// IsGitRepo 返回 path 下是否存在 .git（文件 / 目录皆可，覆盖 worktree checkout 的情况）。
// 本机未配置路径时(LocalPathMissing，R10)直接判 false —— 判据是这个状态位，
// 不是 Path 是否为空(R11)；Path 的空值检查作为数据不一致时的防御性兜底保留。
// 注意：os.Stat 会触发文件系统访问，service 不要在热路径反复调用 —— 缓存到 detect API。
func (p *Project) IsGitRepo() bool {
	if p == nil || p.LocalPathMissing || strings.TrimSpace(p.Path) == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(p.Path, ".git"))
	return err == nil
}

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

// Check 字段校验 —— 不校验 parent 存在 / 循环 / 路径可访问，那些是 service 职责。
//
// 校验项：
//  1. 名字非空
//  2. path 非空（绝对/相对均可由 service 二次校验）—— 但 LocalPathMissing 为
//     true 时(本机未配置路径，R10、决策 21)放行：这种项目行本就没有本机路径，
//     空 path 是它的合法表达，不是校验失败
//  3. parent_id 非负
//  4. color 在允许集
func (p *Project) Check(ctx context.Context) error {
	if p == nil {
		return i18n.NewError(ctx, code.ProjectNotFound)
	}
	if strings.TrimSpace(p.Name) == "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	if !p.LocalPathMissing && strings.TrimSpace(p.Path) == "" {
		return i18n.NewError(ctx, code.ProjectInvalidPath)
	}
	if p.ParentID < 0 {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	if _, ok := allowedColors[p.Color]; !ok {
		return i18n.NewError(ctx, code.ProjectInvalidColor)
	}
	return nil
}
