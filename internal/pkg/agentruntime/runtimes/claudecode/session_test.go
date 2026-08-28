package claudecode

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/pkg/claudecode"
)

// TestCCBuildClientOpts_MCPServersInjectTool 锁住 AM2:RunRequest.MCPServers 非空时
// ccBuildClientOpts 把它翻译成 (a) --mcp-config 的 spike 形态 JSON,带 server 的
// name / url / header,(b) --allowedTools 里追加 mcp__<name>__<tool>(如 mcp__org__org_create_department)。
// 通过 Client accessor 断言,不 spawn 真子进程。
func TestCCBuildClientOpts_MCPServersInjectTool(t *testing.T) {
	Convey("Given RunRequest 带一个 http MCP server", t, func() {
		spec := ccLaunchSpec{
			Req: agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
				MCPServers: []agentruntime.MCPServerSpec{{
					Name:    "group",
					URL:     "http://127.0.0.1:9000/mcp/group/",
					Headers: map[string]string{"Authorization": "Bearer tok-123"},
					Tools:   []string{"group_send", "group_invite"},
				}},
			},
		}
		c := claudecode.New(ccBuildClientOpts(spec, "claude")...)

		Convey("Then --mcp-config 是 spike 形态,带 name/url/header", func() {
			cfg := c.McpConfig()
			So(cfg, ShouldEqual,
				`{"mcpServers":{"group":{"type":"http","url":"http://127.0.0.1:9000/mcp/group/","headers":{"Authorization":"Bearer tok-123"}}}}`)
		})

		Convey("Then --allowedTools 含 spec.Tools 声明的每个 tool", func() {
			So(c.AllowedTools(), ShouldContain, "mcp__group__group_send")
			So(c.AllowedTools(), ShouldContain, "mcp__group__group_invite")
		})
	})

	Convey("Given RunRequest 不带 MCPServers(回归)", t, func() {
		spec := ccLaunchSpec{
			Req: agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
			},
		}
		c := claudecode.New(ccBuildClientOpts(spec, "claude")...)

		Convey("Then 不下发 --mcp-config,allowedTools 为空", func() {
			So(c.McpConfig(), ShouldEqual, "")
			So(c.AllowedTools(), ShouldBeEmpty)
		})
	})
}

// TestCCBuildClientOpts_ProviderModelDownToCLI 锁住 Bug 1 修复:
// 绑了 LLM provider 的 claudecode 后端必须把解析出的 ModelID 通过 WithModel
// 透到 *claudecode.Client,后续 OpenSession 装配 argv 才会带 --model。
// 不传时 CLI 用本地默认模型名报 system.init.model,result.Model 会把
// chat_svc 创建消息时写好的占位值覆盖成 CLI 默认,前端展示错。
func TestCCBuildClientOpts_ProviderModelDownToCLI(t *testing.T) {
	Convey("解析出的 ModelID 非空 → Client.Model() = 该 ModelID", t, func() {
		spec := ccLaunchSpec{
			Req: agentruntime.RunRequest{
				Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
				Effective: &agentruntime.EffectiveLLMConfig{ModelID: "glm-5.1"},
			},
		}
		c := claudecode.New(ccBuildClientOpts(spec, "claude")...)
		So(c.Model(), ShouldEqual, "glm-5.1")
	})

	Convey("provider = nil(CLI 登录模式) → Client.Model() 留空", t, func() {
		spec := ccLaunchSpec{
			Req: agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
				// Provider nil:用户没绑 LLM provider,CLI 自身 OAuth 走 anthropic.com。
			},
		}
		c := claudecode.New(ccBuildClientOpts(spec, "claude")...)
		So(c.Model(), ShouldEqual, "")
	})

	Convey("provider 非空但解析出的 ModelID 空(罕见配置) → 不下发 --model,留给 CLI 默认", t, func() {
		spec := ccLaunchSpec{
			Req: agentruntime.RunRequest{
				Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
				Effective: &agentruntime.EffectiveLLMConfig{ModelID: "   "}, // 只有空白
			},
		}
		c := claudecode.New(ccBuildClientOpts(spec, "claude")...)
		// strings.TrimSpace 之后空串不应当下发 WithModel,留给 CLI 走默认。
		So(c.Model(), ShouldEqual, "")
	})
}

func TestCCBuildClientOpts_EnvironmentDownToSettings(t *testing.T) {
	Convey("Given the runtime selected a provider, when Claude client options are built, then the routing environment also has settings precedence", t, func() {
		env := map[string]string{
			"ANTHROPIC_BASE_URL":   "http://gateway.test",
			"ANTHROPIC_AUTH_TOKEN": "gateway-test-token",
		}
		spec := ccLaunchSpec{
			Env: env,
			Req: agentruntime.RunRequest{
				Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
				Effective: &agentruntime.EffectiveLLMConfig{ModelID: "glm-test-model"},
			},
		}

		c := claudecode.New(ccBuildClientOpts(spec, "claude")...)

		So(c.SettingsEnv(), ShouldResemble, env)
	})
}

// TestCCBuildClientOpts_BackendDefaultModel 锁住 CLI 登录态(无 provider)下的自定义
// 模型:backend.DefaultModel 在解析出的 ModelID 为空时兜底下发成 --model;ModelID
// 非空时仍优先,DefaultModel 被忽略(绑 provider 行为不变)。
func TestCCBuildClientOpts_BackendDefaultModel(t *testing.T) {
	Convey("provider = nil + backend.DefaultModel 非空 → 下发 DefaultModel", t, func() {
		spec := ccLaunchSpec{
			Req: agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{
					Type:         string(agent_backend_entity.TypeClaudeCode),
					DefaultModel: "claude-fable-5",
				},
			},
		}
		c := claudecode.New(ccBuildClientOpts(spec, "claude")...)
		So(c.Model(), ShouldEqual, "claude-fable-5")
	})

	Convey("解析出的 ModelID 非空 → 优先于 backend.DefaultModel", t, func() {
		spec := ccLaunchSpec{
			Req: agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{
					Type:         string(agent_backend_entity.TypeClaudeCode),
					DefaultModel: "claude-fable-5",
				},
				Effective: &agentruntime.EffectiveLLMConfig{ModelID: "glm-5.1"},
			},
		}
		c := claudecode.New(ccBuildClientOpts(spec, "claude")...)
		So(c.Model(), ShouldEqual, "glm-5.1")
	})

	Convey("DefaultModel 只有空白 → 不下发 --model", t, func() {
		spec := ccLaunchSpec{
			Req: agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{
					Type:         string(agent_backend_entity.TypeClaudeCode),
					DefaultModel: "   ",
				},
			},
		}
		c := claudecode.New(ccBuildClientOpts(spec, "claude")...)
		So(c.Model(), ShouldEqual, "")
	})
}

// TestCCBuildClientOpts_MCPTimeoutEnv 锁住:有 MCPServers 时注入 MCP_TIMEOUT /
// MCP_TOOL_TIMEOUT env(均为 600000 ms);无 MCPServers 时不注入;caller 显式配
// 置同名 key 时不覆盖。
func TestCCBuildClientOpts_MCPTimeoutEnv(t *testing.T) {
	Convey("Given RunRequest 带 MCPServers", t, func() {
		spec := ccLaunchSpec{
			Req: agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
				MCPServers: []agentruntime.MCPServerSpec{{
					Name: "org",
					URL:  "http://127.0.0.1:9001/mcp/org/",
				}},
			},
		}
		c := claudecode.New(ccBuildClientOpts(spec, "claude")...)

		Convey("Then Env 含 MCP_TIMEOUT=600000 与 MCP_TOOL_TIMEOUT=600000", func() {
			env := c.Env()
			So(env["MCP_TIMEOUT"], ShouldEqual, "600000")
			So(env["MCP_TOOL_TIMEOUT"], ShouldEqual, "600000")
		})
	})

	Convey("Given RunRequest 不带 MCPServers", t, func() {
		spec := ccLaunchSpec{
			Req: agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
			},
		}
		c := claudecode.New(ccBuildClientOpts(spec, "claude")...)

		Convey("Then Env 不含 MCP_TIMEOUT / MCP_TOOL_TIMEOUT", func() {
			env := c.Env()
			_, hasMCPTimeout := env["MCP_TIMEOUT"]
			_, hasMCPToolTimeout := env["MCP_TOOL_TIMEOUT"]
			So(hasMCPTimeout, ShouldBeFalse)
			So(hasMCPToolTimeout, ShouldBeFalse)
		})
	})

	Convey("Given RunRequest 带 MCPServers 且 spec.Env 已有同名 key", t, func() {
		spec := ccLaunchSpec{
			Env: map[string]string{
				"MCP_TIMEOUT":      "30000",
				"MCP_TOOL_TIMEOUT": "30000",
			},
			Req: agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
				MCPServers: []agentruntime.MCPServerSpec{{
					Name: "org",
					URL:  "http://127.0.0.1:9001/mcp/org/",
				}},
			},
		}
		c := claudecode.New(ccBuildClientOpts(spec, "claude")...)

		Convey("Then caller 的值优先,不被覆盖", func() {
			env := c.Env()
			So(env["MCP_TIMEOUT"], ShouldEqual, "30000")
			So(env["MCP_TOOL_TIMEOUT"], ShouldEqual, "30000")
		})
	})
}

// TestResolveLaunchMode_BypassDefaultLocksLaunchToBypass 锁住"backend admin 配
// bypass 时 CLI 永远以 bypass 启动"的不变量。stored mode(perTurn)可以是 plan /
// acceptEdits / default 任意值, launch 永远 bypass —— 这是「先 plan 后 bypass」
// 工作流的承重柱:permission_mode_at_launch 必须 == bypass, 才能让 bypass-lockout
// 解锁 + PlanApproveCard 主按钮走 Bypass 分支。
//
// 其它 backend default 走原来的"perTurn → backendDefault → ”"优先级。
func TestResolveLaunchMode_BypassDefaultLocksLaunchToBypass(t *testing.T) {
	Convey("Given backendDefault=bypassPermissions", t, func() {
		Convey("When perTurn=plan, Then launch=bypassPermissions", func() {
			So(resolveLaunchMode("plan", "bypassPermissions"), ShouldEqual, "bypassPermissions")
		})
		Convey("When perTurn=acceptEdits, Then launch=bypassPermissions", func() {
			So(resolveLaunchMode("acceptEdits", "bypassPermissions"), ShouldEqual, "bypassPermissions")
		})
		Convey("When perTurn=default, Then launch=bypassPermissions", func() {
			So(resolveLaunchMode("default", "bypassPermissions"), ShouldEqual, "bypassPermissions")
		})
		Convey("When perTurn 空, Then launch=bypassPermissions", func() {
			So(resolveLaunchMode("", "bypassPermissions"), ShouldEqual, "bypassPermissions")
		})
	})

	Convey("Given backendDefault != bypassPermissions, 原有 perTurn → default 优先级不变", t, func() {
		Convey("backendDefault=acceptEdits + perTurn=plan → plan(perTurn 优先)", func() {
			So(resolveLaunchMode("plan", "acceptEdits"), ShouldEqual, "plan")
		})
		Convey("backendDefault=acceptEdits + perTurn='' → acceptEdits(回落 default)", func() {
			So(resolveLaunchMode("", "acceptEdits"), ShouldEqual, "acceptEdits")
		})
		Convey("backendDefault='' + perTurn='' → ''(走 pkg/claudecode 兜底)", func() {
			So(resolveLaunchMode("", ""), ShouldEqual, "")
		})
	})
}
