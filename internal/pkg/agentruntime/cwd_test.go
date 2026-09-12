package agentruntime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentCwd_UsesAgentDirectory(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)

	got, err := AgentCwd(42)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(dataDir, "agents", "42"), got)
	info, err := os.Stat(got)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestAgentCwd_RejectsMissingAgentID(t *testing.T) {
	_, err := AgentCwd(0)
	assert.Error(t, err)
}

// ── ResolveAgentCwd:没有本地 agentID 时的兜底 ────────────────────────────────
//
// web 发起的对话在 daemon 上落到 AgentID=0:浏览器手里没有桌面端本地自增主键,也不该
// 编一个(见 RunRequest.AgentSyncID),身份只由账号级 ULID 表达。

func TestResolveAgentCwd_PrefersLocalAgentID(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)

	got, err := ResolveAgentCwd(42, "01KZNE7YKJQ6A79YVDCMW1A63R")
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(dataDir, "agents", "42"), got,
		"本地 agentID 可用时目录不变——老会话的累积文件不许因为这次改动搬家")
}

func TestResolveAgentCwd_FallsBackToAgentSyncID(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)

	got, err := ResolveAgentCwd(0, "01KZNE7YKJQ6A79YVDCMW1A63R")
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(dataDir, "agents", "sync-01KZNE7YKJQ6A79YVDCMW1A63R"), got)
	info, err := os.Stat(got)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

// 同一 Agent 的多条自由会话复用同一目录(与 AgentCwd 的 Agent 级语义一致)。
func TestResolveAgentCwd_SyncIDIsStableAcrossCalls(t *testing.T) {
	t.Setenv("AGENTRE_DATA_DIR", t.TempDir())

	first, err := ResolveAgentCwd(0, "01KZNE7YKJQ6A79YVDCMW1A63R")
	require.NoError(t, err)
	second, err := ResolveAgentCwd(0, "01KZNE7YKJQ6A79YVDCMW1A63R")
	require.NoError(t, err)

	assert.Equal(t, first, second)
}

// 同步标识是从对端(浏览器 / 别的设备)原样收来的字符串,直接当路径段会把 AppDataDir
// 之外的目录拖进来。只认「一段安全的标识」,其余一律拒,且不许在数据目录留下痕迹。
func TestResolveAgentCwd_RejectsUnsafeAgentSyncID(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)

	for _, bad := range []string{
		"", "   ", ".", "..", "../../etc", "a/b", `a\b`, "a\x00b", "a b", "sync id",
	} {
		got, err := ResolveAgentCwd(0, bad)
		assert.Error(t, err, "agentSyncID %q 必须被拒", bad)
		assert.Empty(t, got, "agentSyncID %q 被拒时不许回半个路径", bad)
	}

	entries, err := os.ReadDir(dataDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "被拒的标识不许在数据目录里建出任何东西")
}
