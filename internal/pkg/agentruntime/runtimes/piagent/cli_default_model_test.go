package piagent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// launchModelOf 装配一次会话并取回真正会交给 pi 的 --model 值（空 = 不下发）。
func launchModelOf(t *testing.T, req agentruntime.RunRequest) string {
	t.Helper()
	restore := SetProviderExtensionWriterForTest(func(string) (string, error) {
		return "/ext/agentre-provider-abc.mjs", nil
	})
	defer restore()
	sess, err := sessionFactory(req, map[string]string{}, t.TempDir())
	require.NoError(t, err)
	adapter, ok := sess.(*clientAdapter)
	require.True(t, ok)
	if closer, ok := sess.(interface{ Close(context.Context) error }); ok {
		t.Cleanup(func() { _ = closer.Close(context.Background()) })
	}
	return adapter.client.Model()
}

func piBackend(defaultModel string) *agent_backend_entity.AgentBackend {
	return &agent_backend_entity.AgentBackend{
		Type:         string(agent_backend_entity.TypePiAgent),
		EnvJSON:      "{}",
		DefaultModel: defaultModel,
	}
}

func boundProvider() *agentruntime.EffectiveLLMConfig {
	return &agentruntime.EffectiveLLMConfig{
		ProviderKey:  "provabc",
		ModelID:      "deepseek-v3",
		ProviderType: string(llm_provider_entity.TypeOpenAIChat),
	}
}

func TestPiCLIEffectiveModel(t *testing.T) {
	Convey("Given 走 CLI 登录态(未绑供应商)的 pi 后端", t, func() {
		Convey("When 后端没有默认模型 Then 不下发 --model(与今天逐字相同)", func() {
			So(launchModelOf(t, agentruntime.RunRequest{Backend: piBackend("")}), ShouldBeEmpty)
		})

		Convey("When 后端默认模型是 provider/id Then 以它作 --model 启动", func() {
			So(launchModelOf(t, agentruntime.RunRequest{Backend: piBackend(" zhipu/glm-5.3 ")}), ShouldEqual, "zhipu/glm-5.3")
		})

		Convey("When 会话选择「跟随 CLI 默认」 Then 后端默认模型被忽略", func() {
			So(launchModelOf(t, agentruntime.RunRequest{
				Backend:      piBackend("zhipu/glm-5.3"),
				CLIModelMode: "cli-default",
			}), ShouldBeEmpty)
		})

		Convey("When 会话固定了某个 CLI 模型 Then 用它而不是后端默认", func() {
			So(launchModelOf(t, agentruntime.RunRequest{
				Backend:      piBackend("zhipu/glm-5.3"),
				CLIModelMode: "fixed",
				CLIModel:     "anthropic/claude-sonnet-5-5",
			}), ShouldEqual, "anthropic/claude-sonnet-5-5")
		})
	})

	Convey("Given 绑定了 Agentre 供应商的 pi 后端", t, func() {
		Convey("When 还有一份后端默认模型 Then 该值被忽略,仍下发 provider 模型", func() {
			So(launchModelOf(t, agentruntime.RunRequest{
				Backend:   piBackend("zhipu/glm-5.3"),
				Effective: boundProvider(),
			}), ShouldEqual, "agentre-provabc/deepseek-v3")
		})
	})
}

func TestPiResultModelPlaceholderCLIDefault(t *testing.T) {
	Convey("Given CLI 登录态且后端设了默认模型", t, func() {
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (sessionHandle, error) {
			return &fakeSession{stream: &emptyStream{}, sid: "pi-session"}, nil
		})
		defer restore()

		Convey("When pi 不报模型 Then 占位就是下发并生效的那个模型", func() {
			events, result, err := NewWithPool(nil).Run(context.Background(), agentruntime.RunRequest{
				Backend:   piBackend("zhipu/glm-5.3"),
				SessionID: 1,
				Cwd:       t.TempDir(),
				UserText:  "hello",
			})
			So(err, ShouldBeNil)
			for range events {
			}
			So(result.Model, ShouldEqual, "zhipu/glm-5.3")
		})
	})
}

// TestPiLaunchArgsCLIModel 锁住「真正下发到命令行的字节」：没人指定模型时参数与
// 今天逐字相同，后端默认模型非空时只是追加 --model provider/id。
func TestPiLaunchArgsCLIModel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake Pi is not portable to windows")
	}
	cases := []struct {
		name        string
		defaultMode string
		want        []string
	}{
		{name: "no model selected keeps today's command line", want: []string{"--mode", "rpc"}},
		{name: "backend default model is sent as provider/id", defaultMode: "zhipu/glm-5.3", want: []string{"--mode", "rpc", "--model", "zhipu/glm-5.3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			argsFile := filepath.Join(dir, "args.txt")
			binary := writeFakePiRPC(t, dir)
			sess, err := sessionFactory(agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{
					Type:         string(agent_backend_entity.TypePiAgent),
					CLIPath:      binary,
					DefaultModel: tc.defaultMode,
				},
				SessionID: 78,
			}, map[string]string{
				"AGENTRE_TEST_PI_ARGS":       argsFile,
				"AGENTRE_TEST_PI_SESSION_ID": "pi-native-query",
			}, dir)
			require.NoError(t, err)
			// 开一轮才会起进程（会话是惰性 spawn），关闭则收掉它。
			stream, err := sess.Stream(context.Background(), "hello", "", nil)
			require.NoError(t, err)
			for stream.Next() {
			}
			require.NoError(t, stream.Err())
			require.NoError(t, sess.Close(context.Background()))

			argsRaw, err := os.ReadFile(argsFile) //nolint:gosec // test-owned temp path
			require.NoError(t, err)
			args := strings.Split(strings.TrimSpace(string(argsRaw)), "\n")
			assert.Equal(t, tc.want, args)
		})
	}
}
func TestLaunchIdentityIncludesCLIModel(t *testing.T) {
	Convey("Given 两轮之间只有 CLI 模型选择不同", t, func() {
		backend := piBackend("")
		cwd := t.TempDir()
		base := agentruntime.RunRequest{Backend: backend}
		Convey("Then 启动身份不同(含后端默认模型与会话选择)", func() {
			So(launchIdentity(base, cwd), ShouldNotEqual, launchIdentity(agentruntime.RunRequest{
				Backend: piBackend("zhipu/glm-5.3"),
			}, cwd))
			So(launchIdentity(base, cwd), ShouldNotEqual, launchIdentity(agentruntime.RunRequest{
				Backend: backend, CLIModelMode: "fixed", CLIModel: "zhipu/glm-5.3",
			}, cwd))
		})
	})
}

// 用户选定固定模型后,pi 上报的实际模型与本轮固定值不符要能被 chat_svc 发现,
// 而不是把 provider 前缀归一规则用错。
func TestPiUserModelIDKeepsCLIProviderSlash(t *testing.T) {
	Convey("Given CLI 登录态下 pi 上报 provider/id", t, func() {
		Convey("Then 原样保留(不是机器前缀)", func() {
			So(piUserModelID(agentruntime.RunRequest{Backend: piBackend("")}, "zhipu/glm-5.3"), ShouldEqual, "zhipu/glm-5.3")
		})
	})
}
