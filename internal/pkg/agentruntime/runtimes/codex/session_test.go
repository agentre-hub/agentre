package codex

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// TestGatewayDeps 锁住 codex 网关门控的唯一口径(spec 2026-08-10 决策 6):是否装配
// gateway CLIDeps 看本轮有没有 effective provider(req.EffectiveProviderKey()),不再
// 看 backend 是否绑定(req.Backend.LLMProviderKey)——CLI 登录态后端上会话选了 agentre
// 供应商时(backend 未绑定但 req.Effective 非空)也要装配,否则 shouldSignChatGateway
// 签的 token 永远传不到 BuildCodexConfig。
func TestGatewayDeps(t *testing.T) {
	Convey("Given backend 未绑定 provider(LLMProviderKey==\"\")", t, func() {
		backend := &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex)}

		Convey("When 会话没有 effective provider(req.Effective==nil), Then deps 为空(CLI 登录态不装配)", func() {
			deps := gatewayDeps(agentruntime.RunRequest{
				Backend: backend, GatewayToken: "tok", GatewayURL: "http://127.0.0.1:60080",
			})
			So(deps, ShouldResemble, CLIDeps{})
		})

		Convey("When 会话选了 agentre 供应商(req.Effective 非空), Then deps 装配 token/url(登录态可被接管)", func() {
			deps := gatewayDeps(agentruntime.RunRequest{
				Backend: backend, GatewayToken: "tok", GatewayURL: "http://127.0.0.1:60080",
				Effective: &agentruntime.EffectiveLLMConfig{ProviderKey: "session-picked", ModelID: "gpt-5.5"},
			})
			So(deps.Token, ShouldEqual, "tok")
			So(deps.GatewayURL, ShouldEqual, "http://127.0.0.1:60080")
		})
	})

	Convey("Given backend nil, Then deps 为空", t, func() {
		deps := gatewayDeps(agentruntime.RunRequest{GatewayToken: "tok", GatewayURL: "http://127.0.0.1:60080"})
		So(deps, ShouldResemble, CLIDeps{})
	})
}

func TestBuildLaunchSpec_MCPServers(t *testing.T) {
	Convey("Given RunRequest 带一个 http MCP server", t, func() {
		spec := buildLaunchSpec(agentruntime.RunRequest{
			Backend: &agent_backend_entity.AgentBackend{
				Type:    string(agent_backend_entity.TypeCodex),
				EnvJSON: "{}",
			},
			MCPServers: []agentruntime.MCPServerSpec{{
				Name: "group",
				URL:  "http://127.0.0.1:9000/mcp/group/",
				Headers: map[string]string{
					"Authorization": "Bearer tok-123",
					"X-Group":       "group-1",
				},
				Tools: []string{"group_send", "group_invite"},
			}},
		}, nil, "/tmp/work")

		Convey("Then Codex --config 注入 mcp_servers 配置并自动放行声明的 tool", func() {
			So(spec.config, ShouldContain, `mcp_servers.group.url="http://127.0.0.1:9000/mcp/group/"`)
			So(spec.config, ShouldContain, `mcp_servers.group.http_headers.Authorization="Bearer tok-123"`)
			So(spec.config, ShouldContain, `mcp_servers.group.http_headers.X-Group="group-1"`)
			So(spec.config, ShouldContain, `mcp_servers.group.enabled_tools=["group_send","group_invite"]`)
			So(spec.config, ShouldContain, `mcp_servers.group.default_tools_approval_mode="approve"`)
		})
	})

	Convey("Given RunRequest 不带 MCPServers(回归)", t, func() {
		spec := buildLaunchSpec(agentruntime.RunRequest{
			Backend: &agent_backend_entity.AgentBackend{
				Type:    string(agent_backend_entity.TypeCodex),
				EnvJSON: "{}",
			},
		}, nil, "/tmp/work")

		Convey("Then 不下发任何 mcp_servers 覆盖项", func() {
			for _, cfg := range spec.config {
				So(cfg, ShouldNotStartWith, "mcp_servers.")
			}
		})
	})
}

func TestBuildLaunchSpec_ProviderModel(t *testing.T) {
	Convey("Given RunRequest 绑 provider", t, func() {
		Convey("Then spec.model = 解析出的 ModelID(#26 override 已移除)", func() {
			spec := buildLaunchSpec(agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{
					Type:    string(agent_backend_entity.TypeCodex),
					EnvJSON: "{}",
				},
				Effective: &agentruntime.EffectiveLLMConfig{ModelID: "gpt-5.4"},
			}, nil, "/tmp/work")
			So(spec.model, ShouldEqual, "gpt-5.4")
		})

		Convey("Then effective = nil 时 spec.model 为空(CLI 登录态,runtime 兜底 defaultModelID)", func() {
			spec := buildLaunchSpec(agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{
					Type:    string(agent_backend_entity.TypeCodex),
					EnvJSON: "{}",
				},
			}, nil, "/tmp/work")
			So(spec.model, ShouldEqual, "")
		})
	})
}

func TestBuildLaunchSpec_EnabledPlugins(t *testing.T) {
	Convey("Given RunRequest 带 Codex plugin 显式覆盖", t, func() {
		spec := buildLaunchSpec(agentruntime.RunRequest{
			Backend: &agent_backend_entity.AgentBackend{
				Type:    string(agent_backend_entity.TypeCodex),
				EnvJSON: "{}",
			},
			EnabledPlugins: map[string]bool{
				"browser@openai-bundled":     true,
				"superpowers@openai-curated": false,
			},
		}, nil, "/tmp/work")

		Convey("Then Codex --config 注入 plugins.<id>.enabled 覆盖", func() {
			So(spec.config, ShouldContain, `plugins."browser@openai-bundled".enabled=true`)
			So(spec.config, ShouldContain, `plugins."superpowers@openai-curated".enabled=false`)
		})
	})

	Convey("Given RunRequest 不带 EnabledPlugins(回归)", t, func() {
		spec := buildLaunchSpec(agentruntime.RunRequest{
			Backend: &agent_backend_entity.AgentBackend{
				Type:    string(agent_backend_entity.TypeCodex),
				EnvJSON: "{}",
			},
		}, nil, "/tmp/work")

		Convey("Then 不下发任何 plugins 覆盖项", func() {
			for _, cfg := range spec.config {
				So(cfg, ShouldNotStartWith, "plugins.")
			}
		})
	})
}

// TestBuildLaunchSpec_ReasoningEffort 锁住 spec 2026-09-01「三后端下发档位的收敛」:
// max 原样下发,不再被本地兼容折叠成 high;非法值仍不下发。
func TestBuildLaunchSpec_ReasoningEffort(t *testing.T) {
	Convey("Given 会话有效力度为 max", t, func() {
		spec := buildLaunchSpec(agentruntime.RunRequest{
			Backend: &agent_backend_entity.AgentBackend{
				Type: string(agent_backend_entity.TypeCodex), EnvJSON: "{}", ReasoningEffort: "max",
			},
		}, nil, "/tmp/work")

		Convey("Then Codex --config 下发 model_reasoning_effort=\"max\"", func() {
			So(spec.config, ShouldContain, `model_reasoning_effort="max"`)
		})
	})

	Convey("Given 会话有效力度是非法值", t, func() {
		spec := buildLaunchSpec(agentruntime.RunRequest{
			Backend: &agent_backend_entity.AgentBackend{
				Type: string(agent_backend_entity.TypeCodex), EnvJSON: "{}", ReasoningEffort: "bogus",
			},
		}, nil, "/tmp/work")

		Convey("Then 不下发 model_reasoning_effort,走 CLI 自身默认", func() {
			for _, cfg := range spec.config {
				So(cfg, ShouldNotStartWith, "model_reasoning_effort")
			}
		})
	})
}
