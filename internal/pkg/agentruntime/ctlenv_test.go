package agentruntime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
)

// 会话里的固定行让 agent 直接跑裸 `agrctl …`：凭证注入时，已装 agrctl 所在目录要排到
// 子进程 PATH 最前，否则 exit 127。

func TestApplyCtlEnv_GivenRegisteredAgrctl_ThenItsDirComesFirstInPath(t *testing.T) {
	t.Cleanup(func() { RegisterAgrctlPath("") })
	dir := filepath.Join(t.TempDir(), "bin")
	RegisterAgrctlPath(filepath.Join(dir, "agrctl"))
	ctl := CtlCredentials{Endpoint: "http://127.0.0.1:1", Token: "t"}

	t.Run("无 env_json PATH：接在进程 PATH 前", func(t *testing.T) {
		t.Setenv("PATH", "/usr/bin"+string(os.PathListSeparator)+"/bin")
		env := map[string]string{}
		applyCtlEnv(env, ctl)
		assert.Equal(t, dir+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin", env["PATH"])
	})
	t.Run("env_json 的 PATH 保留在后面", func(t *testing.T) {
		t.Setenv("PATH", "/usr/bin")
		env := map[string]string{"PATH": "/custom/bin"}
		applyCtlEnv(env, ctl)
		assert.Equal(t, dir+string(os.PathListSeparator)+"/custom/bin", env["PATH"])
	})
	t.Run("已在最前不重复", func(t *testing.T) {
		env := map[string]string{"PATH": dir + string(os.PathListSeparator) + "/usr/bin"}
		applyCtlEnv(env, ctl)
		assert.Equal(t, dir+string(os.PathListSeparator)+"/usr/bin", env["PATH"])
	})
	t.Run("进程 PATH 为空：只有 agrctl 目录", func(t *testing.T) {
		t.Setenv("PATH", "")
		env := map[string]string{}
		applyCtlEnv(env, ctl)
		assert.Equal(t, dir, env["PATH"])
	})
}

func TestApplyCtlEnv_GivenNoCredentialsOrNoRegisteredPath_ThenPathUntouched(t *testing.T) {
	t.Cleanup(func() { RegisterAgrctlPath("") })
	t.Setenv("PATH", "/usr/bin")

	RegisterAgrctlPath("/opt/agentre/bin/agrctl")
	env := map[string]string{}
	applyCtlEnv(env, CtlCredentials{Token: "t"})
	_, has := env["PATH"]
	assert.False(t, has, "凭证不完整 → 不动 PATH")

	RegisterAgrctlPath("")
	env = map[string]string{"PATH": "/custom"}
	applyCtlEnv(env, CtlCredentials{Endpoint: "http://x", Token: "t"})
	assert.Equal(t, "/custom", env["PATH"], "没注册 agrctl → 不动 PATH")
	env = map[string]string{}
	applyCtlEnv(env, CtlCredentials{Endpoint: "http://x", Token: "t"})
	_, has = env["PATH"]
	assert.False(t, has)
}

func TestCLIEnv_PutsAgrctlDirOnPathForSessions(t *testing.T) {
	t.Cleanup(func() { RegisterAgrctlPath("") })
	dir := filepath.Join(t.TempDir(), "bin")
	RegisterAgrctlPath(filepath.Join(dir, "agrctl"))
	t.Setenv("PATH", "/usr/bin")
	ctl := CtlCredentials{Endpoint: "http://127.0.0.1:1", Token: "t"}
	builders := map[string]func(*agent_backend_entity.AgentBackend, CLIDeps) (map[string]string, error){
		"claudecode": BuildClaudeCodeEnv,
		"codex":      BuildCodexEnv,
		"piagent":    BuildPiAgentEnv,
		"acp":        BuildACPEnv,
	}
	for name, build := range builders {
		t.Run(name, func(t *testing.T) {
			env, err := build(&agent_backend_entity.AgentBackend{}, CLIDeps{Ctl: ctl})
			require.NoError(t, err)
			assert.Equal(t, dir+string(os.PathListSeparator)+"/usr/bin", env["PATH"])

			env, err = build(&agent_backend_entity.AgentBackend{}, CLIDeps{})
			require.NoError(t, err)
			_, has := env["PATH"]
			assert.False(t, has, "探测类调用点不动 PATH")
		})
	}
}

// dev 覆盖 AGENTRE_AGRCTL_PATH 照注释写成 build/bin/agrctl（或裸 agrctl）时是相对路径：原样排进
// PATH 会按会话子进程的 cwd（项目工作区）解析，工作区里的 build/bin/git、./ls 就能顶替系统命令。
// 登记时按登记进程自己的 cwd 定成绝对路径。
func TestRegisterAgrctlPath_GivenRelativePath_ThenPathEntryIsAbsolute(t *testing.T) {
	t.Cleanup(func() { RegisterAgrctlPath("") })
	t.Setenv("PATH", "/usr/bin")
	cwd, err := os.Getwd()
	require.NoError(t, err)
	ctl := CtlCredentials{Endpoint: "http://127.0.0.1:1", Token: "t"}

	for _, rel := range []string{filepath.Join("build", "bin", "agrctl"), "agrctl"} {
		RegisterAgrctlPath(rel)
		env := map[string]string{}
		applyCtlEnv(env, ctl)
		first := filepath.SplitList(env["PATH"])[0]
		assert.Equal(t, filepath.Join(cwd, filepath.Dir(rel)), first, rel)
	}
}
