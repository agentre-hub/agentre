package issue_entity

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLabelCheck(t *testing.T) {
	ctx := context.Background()
	for _, tone := range Tones() {
		require.NoError(t, (&Label{Name: "x", Tone: tone}).Check(ctx), tone)
	}
	assert.Error(t, (&Label{Name: "", Tone: ToneRed}).Check(ctx))
	assert.Error(t, (&Label{Name: "x", Tone: "rainbow"}).Check(ctx))
	assert.Error(t, (&Label{Name: "x", Tone: ""}).Check(ctx))
	// 标签一旦可自建,色调名就不能再等于用途:五档语义名经迁移 1:1 改写成颜色名后
	// 不再是合法取值,否则目录与前端色调表会各留一套悄悄漂移。
	for _, tone := range []string{"bug", "critical", "docs", "feature", "refactor"} {
		assert.Error(t, (&Label{Name: "x", Tone: tone}).Check(ctx), tone)
	}
	// 精简时被删掉的那五档同样不在取值域里。
	for _, tone := range []string{"auth", "hook", "ops", "perf", "ui"} {
		assert.Error(t, (&Label{Name: "x", Tone: tone}).Check(ctx), tone)
	}
}

// TestTonesIsTheEightColourPalette 钉住 8 档色调取值域与顺序:它同时是前端色板的
// 渲染顺序,少一档意味着色板上少一个可选色而不会有任何用例变红。
func TestTonesIsTheEightColourPalette(t *testing.T) {
	assert.Equal(t, []string{
		ToneGray, ToneRed, ToneRedSolid, ToneAmber,
		ToneGreen, ToneSteel, ToneBlue, ToneViolet,
	}, Tones())
	assert.Equal(t, []string{
		"gray", "red", "red_solid", "amber", "green", "steel", "blue", "violet",
	}, Tones())
}

// TestSeedLabelSyncID_DerivesTheSameIDOnEveryMachine 是「内置种子标签在每台机器上
// 都存在同一份」的落点:补发同步标识时它们必须按名字确定性派生,否则同一个「前端」
// 标签首次上行后会在账号里变成 N 份。
func TestSeedLabelSyncID_DerivesTheSameIDOnEveryMachine(t *testing.T) {
	for _, name := range BuiltinLabelNames() {
		got := SeedLabelSyncID(name)
		assert.Equal(t, got, SeedLabelSyncID(name), name)
		assert.Len(t, got, 26, name) // 与 syncmeta_entity.NewSyncID 同形(ULID)
	}
	assert.NotEqual(t, SeedLabelSyncID("bug"), SeedLabelSyncID("docs"))
}

// TestBuiltinLabelNames 钉住内置目录:迁移按这份名单派生种子标识,名单漂了就会有
// 一部分种子标签拿到随机标识,跨机再也收敛不了。
func TestBuiltinLabelNames(t *testing.T) {
	assert.Equal(t, []string{"bug", "critical", "docs", "feature", "refactor"}, BuiltinLabelNames())
}

// TestLabelCarriesSyncMeta 标签并入账号级同步组:整行带同步元数据。
func TestLabelCarriesSyncMeta(t *testing.T) {
	l := &Label{Name: "x", Tone: ToneRed}
	l.EnsureSyncID()
	assert.NotEmpty(t, l.SyncID)
	assert.False(t, l.IsClaimed())
}

// TestIssueLabelCarriesSyncMeta 关联行本身也是一个同步对象(建/删关联要能跨机)。
func TestIssueLabelCarriesSyncMeta(t *testing.T) {
	il := &IssueLabel{IssueID: 1, LabelID: 2}
	il.EnsureSyncID()
	assert.NotEmpty(t, il.SyncID)
}
