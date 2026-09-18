package agentruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
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
		// 词表放宽到容纳系统 Agent 的冒号之后,冒号仍然不许把别的东西夹带进来。
		":", ":a", "C:/Windows", `C:\Windows`, "a:../..", "../agent:system:default-ceo",
		"agent:system:default-ceo/../..", "agent:system:default ceo",
	} {
		got, err := ResolveAgentCwd(0, bad)
		assert.Error(t, err, "agentSyncID %q 必须被拒", bad)
		assert.Empty(t, got, "agentSyncID %q 被拒时不许回半个路径", bad)
	}

	entries, err := os.ReadDir(dataDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "被拒的标识不许在数据目录里建出任何东西")
}

// 系统 Agent(默认 CEO 助手)的同步标识是全仓唯一一个非随机的:
// agent_entity.DefaultAgentSyncID = "agent:system:default-ceo",里面带冒号。控制台
// 对它发起「不指定项目」的自由对话时 AgentID=0、Cwd 空,兜底解析恒定报
// "needs agentID > 0 or a syntactically valid agentSyncID",界面上是「Agent 启动失败」。
// 这一类标识必须解得出目录,且目录名里不许留下冒号——Windows 上冒号是盘符/备用数据流
// 分隔符,带冒号的目录在 NTFS 上根本建不出来。
func TestResolveAgentCwd_AcceptsSystemAgentSyncID(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)

	got, err := ResolveAgentCwd(0, agent_entity.DefaultAgentSyncID)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(dataDir, "agents", "sync-agent~3Asystem~3Adefault-ceo"), got)
	info, err := os.Stat(got)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	seg := filepath.Base(got)
	assert.NotContains(t, seg, ":", "目录名里不许留冒号——Windows 上建不出来")
	assert.Equal(t, filepath.Join(dataDir, "agents"), filepath.Dir(got),
		"目录必须正好落在 agents/ 下一层,不许被标识里的字符带偏")

	// 同一 Agent 的多条自由会话复用同一个目录(与 ULID 那一类同口径)。
	again, err := ResolveAgentCwd(0, agent_entity.DefaultAgentSyncID)
	require.NoError(t, err)
	assert.Equal(t, got, again)
}

// 老标识的目录不许搬家:词表里能直接当目录名用的那一部分逐字保留,转义对它们是恒等。
// ULID 是 [0-9A-Z],落在这一档里。
func TestResolveAgentCwd_PathSafeSyncIDsKeepTheirDirectory(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)

	for _, id := range []string{
		"01KZNE7YKJQ6A79YVDCMW1A63R",
		"01KZQPHK6Q55AKRHWFX5EM0YWH",
		"a",
		"A1_b-c",
		strings.Repeat("z", 64),
	} {
		got, err := ResolveAgentCwd(0, id)
		require.NoError(t, err, "agentSyncID %q", id)
		assert.Equal(t, filepath.Join(dataDir, "agents", "sync-"+id), got,
			"agentSyncID %q 的目录必须与改动前逐字相同", id)
	}
}
