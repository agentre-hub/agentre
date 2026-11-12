package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agrctlinstall"
)

// 规格「Executors / agentred」:agentred 启动时把随包的 agrctl 与 ctlskill 装到该主机用户
// 目录下,与桌面端的安装位置相同(<AppDataDir>/bin/agrctl + ~/.agents/skills/agrctl …)。

func TestInstallCtlTools_GivenBundledAgrctl_ThenBinaryAndSkillLandWhereTheDesktopPutsThem(t *testing.T) {
	home, appData := t.TempDir(), t.TempDir()
	src := filepath.Join(t.TempDir(), "agrctl")
	require.NoError(t, os.WriteFile(src, []byte("#!/bin/sh\n"), 0o755))

	path, err := InstallCtlTools(CtlTools{Home: home, AppDataDir: appData, Source: src, Version: "1.2.3"})
	require.NoError(t, err)

	assert.Equal(t, agrctlinstall.InstalledPath(appData), path)
	got, err := os.ReadFile(path) //nolint:gosec // G304: 测试读自己刚装到临时目录里的文件。
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\n", string(got))

	skill, err := os.ReadFile(filepath.Join(home, ".agents", "skills", "agrctl", "SKILL.md")) //nolint:gosec // G304: 同上,临时 home。
	require.NoError(t, err, "the universal agent skill is laid down for the host user")
	assert.Contains(t, string(skill), path, "the skill points at the installed agrctl")
	_, err = os.Stat(filepath.Join(home, ".claude", "plugins", "marketplaces", "agentre", "agrctl"))
	assert.NoError(t, err, "and the Claude Code plugin form too")
}

func TestInstallCtlTools_GivenNoBundledAgrctl_ThenNothingIsInstalled(t *testing.T) {
	home, appData := t.TempDir(), t.TempDir()

	path, err := InstallCtlTools(CtlTools{Home: home, AppDataDir: appData, Version: "1.2.3"})
	require.NoError(t, err)
	assert.Empty(t, path)
	_, err = os.Stat(filepath.Join(home, ".agents"))
	assert.True(t, os.IsNotExist(err), "a skill pointing at a missing agrctl is not laid down")
}

// 会话里裸跑 agrctl 要靠 PATH：装好后把路径登记给 agentruntime，由它在注入会话凭证时
// 把所在目录排到 PATH 最前。
func TestInstallCtlTools_GivenInstall_ThenAgrctlPathIsRegisteredForSessions(t *testing.T) {
	t.Cleanup(func() { agentruntime.RegisterAgrctlPath("") })
	t.Setenv("PATH", "/usr/bin")
	src := filepath.Join(t.TempDir(), "agrctl")
	require.NoError(t, os.WriteFile(src, []byte("#!/bin/sh\n"), 0o755))
	appData := t.TempDir()

	path, err := InstallCtlTools(CtlTools{Home: t.TempDir(), AppDataDir: appData, Source: src, Version: "1.2.3"})
	require.NoError(t, err)

	env, err := agentruntime.BuildCodexEnv(&agent_backend_entity.AgentBackend{},
		agentruntime.CLIDeps{Ctl: agentruntime.CtlCredentials{Endpoint: "http://x", Token: "t"}})
	require.NoError(t, err)
	assert.Equal(t, filepath.Dir(path)+string(os.PathListSeparator)+"/usr/bin", env["PATH"])
}

func TestInstallCtlTools_GivenNoBundledAgrctl_ThenNothingIsRegistered(t *testing.T) {
	t.Cleanup(func() { agentruntime.RegisterAgrctlPath("") })
	agentruntime.RegisterAgrctlPath("")
	t.Setenv("PATH", "/usr/bin")

	_, err := InstallCtlTools(CtlTools{Home: t.TempDir(), AppDataDir: t.TempDir(), Version: "1.2.3"})
	require.NoError(t, err)

	env, err := agentruntime.BuildCodexEnv(&agent_backend_entity.AgentBackend{},
		agentruntime.CLIDeps{Ctl: agentruntime.CtlCredentials{Endpoint: "http://x", Token: "t"}})
	require.NoError(t, err)
	_, has := env["PATH"]
	assert.False(t, has)
}
