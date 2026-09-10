package issue_entity

import (
	"context"
	"crypto/sha256"
	"strings"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/oklog/ulid/v2"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// 色调是设计系统的 8 档**颜色名**，不是用途名（决策 6）：标签一旦可自建，`bug` /
// `feature` 这类语义名就只配得上内置目录，配不上用户自己的标签。取值与前端的色调表
// （共享包的 toneClassNames）同源，颜色本身由设计系统的 token 管理。
const (
	ToneGray     = "gray"      // 中性档：描边而非填充，不依赖任何表面色
	ToneRed      = "red"       //
	ToneRedSolid = "red_solid" // 实心红
	ToneAmber    = "amber"     //
	ToneGreen    = "green"     //
	ToneSteel    = "steel"     // 钢蓝
	ToneBlue     = "blue"      //
	ToneViolet   = "violet"    //
)

// tones 是 8 档色调的取值域**与渲染顺序**（色板从左到右）。
var tones = []string{
	ToneGray, ToneRed, ToneRedSolid, ToneAmber,
	ToneGreen, ToneSteel, ToneBlue, ToneViolet,
}

var allowedTones = func() map[string]struct{} {
	m := make(map[string]struct{}, len(tones))
	for _, t := range tones {
		m[t] = struct{}{}
	}
	return m
}()

// Tones 返回 8 档色调（副本，调用方改不动取值域）。
func Tones() []string { return append([]string(nil), tones...) }

// builtinLabelNames 是内置标签目录：由 202608080010 seed、202608270004 精简到五个。
// 它们的显示名在前端按当前语言翻译（issues.labels.<name>），库里存的始终是这个英文
// slug；色调不再等于名字（见 Tone 常量）。
var builtinLabelNames = []string{"bug", "critical", "docs", "feature", "refactor"}

// BuiltinLabelNames 返回内置标签目录（副本）。
func BuiltinLabelNames() []string { return append([]string(nil), builtinLabelNames...) }

// SeedLabelSyncID 按名字**确定性派生**内置种子标签的同步标识。
//
// 内置的五个标签在每台机器上都存在同一份（都来自同一条 seed 迁移）。补发同步标识时
// 若照常随机取值，同一个「前端」标签首次上行后就会在账号里变成 N 份；按名字派生让两台
// 机器上的同一个种子标签天然收敛成同一个对象。用户自建的标签照常随机取标识。
//
// 取值与 syncmeta_entity.NewSyncID 同形（26 位 ULID 串），只是熵来自名字的哈希而不是
// 随机源——同步层看到的始终是同一种字符串形状。
func SeedLabelSyncID(name string) string {
	sum := sha256.Sum256([]byte("agentre.seed.label:" + name))
	var id ulid.ULID
	copy(id[:], sum[:len(id)])
	return id.String()
}

// Label issue 标签目录项。
type Label struct {
	ID         int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Name       string `gorm:"column:name;type:text;not null"`
	Tone       string `gorm:"column:tone;type:text;not null;default:''"`
	SortOrder  int    `gorm:"column:sort_order;type:int;not null;default:0"`
	Status     int    `gorm:"column:status;type:int;not null;default:1"`
	Createtime int64  `gorm:"column:createtime;type:bigint;not null;default:0"`
	Updatetime int64  `gorm:"column:updatetime;type:bigint;not null;default:0"`
	// SyncMeta 账号级同步元数据（R1）：标签目录跨机共享，内置的五个按名字收敛
	// （见 SeedLabelSyncID）。
	syncmeta_entity.SyncMeta `gorm:"embedded"`
}

func (*Label) TableName() string { return "labels" }

func (l *Label) IsActive() bool { return l != nil && l.Status == consts.ACTIVE }

// Check 校验标签名与色调。
func (l *Label) Check(ctx context.Context) error {
	if l == nil || strings.TrimSpace(l.Name) == "" {
		return i18n.NewError(ctx, code.IssueLabelNameRequired)
	}
	if _, ok := allowedTones[l.Tone]; !ok {
		return i18n.NewError(ctx, code.IssueLabelInvalidTone)
	}
	return nil
}

// IssueLabel issue ↔ label 多对多关联。
type IssueLabel struct {
	IssueID int64 `gorm:"column:issue_id;primaryKey"`
	LabelID int64 `gorm:"column:label_id;primaryKey"`
	// SyncMeta 关联行自己也是一个同步对象：跨机表达的是「哪个任务挂了哪个标签」，
	// 两端的本地自增主键各不相同，只能靠同步标识指认。
	syncmeta_entity.SyncMeta `gorm:"embedded"`
}

func (*IssueLabel) TableName() string { return "issue_labels" }
