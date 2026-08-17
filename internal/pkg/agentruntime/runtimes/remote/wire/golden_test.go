// golden_test.go 生成 wire 协议的黄金样本(浏览器侧 TS 编解码的对照基准)。
//
// 黄金样本的用途是「与 Go 侧逐字段同构」:浏览器侧的 vitest 读同一批帧,断言 TS
// 编解码解出的结构与这里用真实 Go marshaler 序列化出来的逐字节一致。生成器住在本包
// (wire 类型旁),产物提交在本仓的 frontend/packages/agentre-wire/fixtures/,由零
// 依赖数据包 @agentre-ai/agentre-wire 对外发布 —— 消费方装包取样本,不再跨仓搬运。
//
// 三个测试各司其职:
//
//   - TestGoldenSamples —— 自检:每条样本验证「确定性」(再 marshal 一次逐字节相同,
//     Go 的 map key 排序保证稳定)与「往返不丢字段」(map 形态解析再序列化不丢键)。
//   - TestGoldenFixturesFresh —— 新鲜度守卫,总是运行:把样本生成到临时目录,与包里
//     已提交的副本比文件集 + 逐字节内容。给 wire 结构加字段却忘了重新生成,这里变红。
//   - TestWriteGoldenSamples —— 重新生成这个动作本身,带 WIRE_GOLDEN_WRITE=1 才执行,
//     让 `go test ./...` 不写盘。
//
// 重新生成的命令:
//
//	WIRE_GOLDEN_WRITE=1 go test ./internal/pkg/agentruntime/runtimes/remote/wire/ -run TestWriteGoldenSamples
//
// 命名约定:每条样本的名字就是 fixtures 目录里那个 .json 文件的 basename,
// vitest 直接 import 同名文件。
package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/require"

	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
)

// goldenFixturesRel 黄金样本在本仓里的位置(相对仓库根)——
// @agentre-ai/agentre-wire 这个零依赖数据包的 fixtures 目录。
const goldenFixturesRel = "frontend/packages/agentre-wire/fixtures"

// regenCmd 样本过期时重新生成的确切命令,原样出现在守卫的失败信息里。
const regenCmd = "WIRE_GOLDEN_WRITE=1 go test ./internal/pkg/agentruntime/runtimes/remote/wire/ -run TestWriteGoldenSamples"

// goldenFrame 一条黄金样本:名字 + 用真实 Go marshaler 序列化出来的帧。
// extraKeys 是注入的未知字段键名,自检时断言它们在序列化结果里确实存在。
type goldenFrame struct {
	name      string
	body      any
	extraKeys []string
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// injectUnknown 把既有帧(marshal 成 map)加上未知字段,模拟老版本 agentred /
// 未来扩展在帧里多带了 TS codec 不认识的键 —— 验证「未知字段不丢弃」。
func injectUnknown(t *testing.T, name string, body any, extra map[string]any) goldenFrame {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	for k, v := range extra {
		m[k] = v
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	return goldenFrame{name: name, body: m, extraKeys: keys}
}

// buildGoldenFrames 用真实 Go 结构体 + 真实 Go marshaler 组装全部黄金样本。
// 新增 wire 帧类型时,在这里加一条样本(以及 agentre-server 侧对应的 TS 解码断言)。
func buildGoldenFrames(t *testing.T) []goldenFrame {
	t.Helper()

	const (
		sid       = int64(42)
		agentID   = int64(7)
		title     = "重构登录页"
		agentSync = "01JZ7W2A8KZ4R5T6Y7U8I9O0P1Q"
		provSess  = "sess_abc123"
	)

	// 一条带 R7 + 决策 8 全字段的会话(浏览器看到的「新」会话)。
	newSummary := SessionSummary{
		SessionID:         sid,
		AgentID:           agentID,
		Title:             title,
		AgentSyncID:       agentSync,
		ProviderSessionID: provSess,
		Cwd:               "/home/agent/proj",
		BackendType:       "claudecode",
		LifecycleState:    SessionLifecycleRunning,
		WaitingForInput:   true,
		LatestSeq:         12,
		UpdatedAt:         1754800000000,
	}
	// 一条老会话:R7 未到达,标题 / Agent 标识 / provider_session_id 如实留空
	// (omitempty 直接省略键,不填占位名)。
	legacySummary := SessionSummary{
		SessionID:       8,
		PeerFingerprint: "fp-desktop",
		AgentID:         3,
		Cwd:             "/var/proj",
		BackendType:     "codex",
		LifecycleState:  SessionLifecycleIdle,
		LatestSeq:       5,
	}

	// 实时通知帧(EventFrame)的 event 载荷:真实 agentruntime 事件走它的
	// MarshalJSON(拍平成 {"kind":...}),与 daemon handlers/runtime.go 同一来源。
	textDelta := mustJSON(t, agentruntime.TextDelta{Text: "你好"})

	runAck := RunAck{
		SessionID:            sid,
		ProviderSessionID:    provSess,
		LaunchPermissionMode: "default",
		ProviderFallbackKey:  "key-fallback",
	}
	runResultDone := RunResultDoneFrame{
		SessionID:         sid,
		ProviderSessionID: provSess,
		Usage: &UsageWire{
			PromptTokens:        100,
			CompletionTokens:    50,
			ReasoningTokens:     10,
			CachedTokens:        5,
			CacheCreationTokens: 2,
			TotalTokens:         155,
		},
		UserAnchor:    "anchor-1",
		Model:         "claude-sonnet-4-5",
		ContextWindow: 200000,
		TurnToken:     9,
		Seq:           12,
	}

	return []goldenFrame{
		{
			name: "run-params",
			body: RunParams{
				Backend:   json.RawMessage(`{"backendType":"claudecode"}`),
				AgentID:   agentID,
				SessionID: sid,
				// 别的对端发起的那条会话上开新一轮(R9):origin 原样带回。
				PeerFingerprint:   "fp-desktop",
				Cwd:               "/home/agent/proj",
				Title:             title,
				AgentSyncID:       agentSync,
				SystemPrompt:      "你是 AgentRe 的 Agent。",
				ProviderSessionID: provSess,
				UserText:          "把登录按钮改成蓝色",
				History: []HistoryMessageWire{
					{Role: "user", Blocks: []cagoblocks.StoredBlock{
						{Type: "text", Data: json.RawMessage(`"上一轮的上下文"`)},
					}},
				},
				PermissionMode:    "default",
				CollaborationMode: "manual",
				MCPServers: []agentruntime.MCPServerSpec{
					{
						Name:    "org",
						URL:     "http://127.0.0.1:8899/mcp/org/",
						Headers: map[string]string{"Authorization": "Bearer tok"},
						Tools:   []string{"mcp__org__list"},
					},
				},
				EnabledPlugins:   map[string]bool{"auto-continue": true, "dangerous": false},
				LLMProviderKey:   "11111111-2222-3333-4444-555555555555",
				SourceDevice:     "fp-web-1",
				SourceDeviceName: "Chrome · macOS",
			},
		},
		// 挂账修复(2026-08-11)的 freshSession 场景:regenerate 无锚点 / provider 会话失效
		// 恢复 —— 空 providerSessionId + FreshSession=true,daemon 据此起全新会话而不是拿
		// 落库旧 id 续话。
		{
			name: "run-params-fresh",
			body: RunParams{
				Backend:        json.RawMessage(`{"backendType":"claudecode"}`),
				AgentID:        agentID,
				SessionID:      sid,
				Cwd:            "/home/agent/proj",
				FreshSession:   true,
				PermissionMode: "default",
			},
		},
		{name: "run-ack", body: runAck},
		{name: "session-summary", body: newSummary},
		{name: "session-summary-legacy", body: legacySummary},
		{name: "session-list-result", body: SessionListResult{
			Sessions:                []SessionSummary{newSummary, legacySummary},
			SupportsSessionMetadata: true,
		}},
		// 未升级的 agentred 的应答:它不认识 SupportsSessionMetadata,omitempty 因此
		// 直接省略这个键,客户端解出 false 并据此说明该机器需要升级(兼容性)。
		{name: "session-list-result-legacy", body: SessionListResult{
			Sessions: []SessionSummary{legacySummary},
		}},
		{name: "session-pull-params", body: SessionPullParams{SessionID: sid, Cursor: 0, Limit: DefaultSessionPullLimit}},
		{
			name: "session-pull-result",
			body: SessionPullResult{
				Notifications: []JournaledNotification{
					// 日志行上的 params 不含 seq —— seq 是日志行自己的列,补齐端盖上去。
					{Seq: 11, Method: NotifyEvent, Params: mustJSON(t, EventFrame{SessionID: sid, Event: textDelta})},
					{Seq: 12, Method: NotifyRunResultDone, Params: mustJSON(t, runResultDone)},
				},
				Cursor:    12,
				HasMore:   false,
				OldestSeq: 1,
			},
		},
		{
			name: "journaled-notification",
			body: JournaledNotification{
				Seq:    11,
				Method: NotifyEvent,
				Params: mustJSON(t, EventFrame{SessionID: sid, Event: textDelta}),
			},
		},
		{name: "session-attach-params", body: SessionAttachParams{SessionID: sid}},
		{
			name: "session-attach-result",
			body: SessionAttachResult{
				SessionID:      sid,
				BackendType:    "claudecode",
				LifecycleState: SessionLifecycleRunning,
				LatestSeq:      12,
			},
		},
		{name: "session-pending-waiters-params", body: SessionPendingWaitersParams{SessionID: sid}},
		{
			name: "session-pending-waiters-result",
			body: SessionPendingWaitersResult{
				ToolPermissions: []agentruntime.PendingToolPermission{
					{RequestID: "perm-1", ToolName: "Bash", Input: json.RawMessage(`{"command":"ls -la"}`)},
				},
				AskUserQuestions: []agentruntime.PendingAskUserQuestion{
					{RequestID: "ask-1", Questions: []agentruntime.AskQuestion{
						{ID: "q1", Question: "确认继续执行？", Header: "确认", Options: []agentruntime.AskOption{{Label: "继续", Description: "继续执行"}}},
					}},
				},
			},
		},
		{name: "event-frame", body: EventFrame{SessionID: sid, Event: textDelta, Seq: 11}},
		{name: "run-result-done-frame", body: runResultDone},
		{
			name: "usage-wire",
			body: UsageWire{
				PromptTokens:        100,
				CompletionTokens:    50,
				ReasoningTokens:     10,
				CachedTokens:        5,
				CacheCreationTokens: 2,
				TotalTokens:         155,
			},
		},
		{name: "autonomous-turn-started", body: AutonomousTurnStartedFrame{SessionID: sid, Trigger: "auto", TurnToken: 9, Seq: 13}},
		// 带未知字段的帧:验证 TS 解码不丢弃。
		injectUnknown(t, "run-params-extra", RunParams{
			Backend:          json.RawMessage(`{"backendType":"claudecode"}`),
			AgentID:          agentID,
			SessionID:        sid,
			Title:            title,
			AgentSyncID:      agentSync,
			SourceDevice:     "fp-web-1",
			SourceDeviceName: "Chrome · macOS",
		}, map[string]any{"futureField": map[string]any{"nested": true}, "clientNote": "来自浏览器的自定义字段"}),
		injectUnknown(t, "session-pull-result-extra", SessionPullResult{
			Notifications: []JournaledNotification{
				{Seq: 1, Method: NotifyEvent, Params: mustJSON(t, EventFrame{SessionID: sid, Event: textDelta, Seq: 1})},
			},
			Cursor:    1,
			HasMore:   true,
			OldestSeq: 1,
		}, map[string]any{"serverVersion": "1.2.3"}),
		// JSON-RPC 信封(daemon/rpc.Frame 同 shape;wire 包不反向依赖 daemon,这里用
		// encoding/json 直接组装 —— 帧体仍是上面真实 marshaler 的字节)。
		{
			name: "frame-envelope-request",
			body: map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"method":  MethodSessionPull,
				"params":  mustJSON(t, SessionPullParams{SessionID: sid, Cursor: 0, Limit: DefaultSessionPullLimit}),
			},
		},
		{
			name: "frame-envelope-response",
			body: map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": mustJSON(t, SessionListResult{
					Sessions:                []SessionSummary{newSummary},
					SupportsSessionMetadata: true,
				}),
			},
		},
		{
			name: "frame-envelope-notification",
			body: map[string]any{
				"jsonrpc": "2.0",
				"method":  NotifyEvent,
				"params":  mustJSON(t, EventFrame{SessionID: sid, Event: textDelta, Seq: 11}),
			},
		},
		{
			name: "frame-envelope-error",
			body: map[string]any{
				"jsonrpc": "2.0",
				"id":      2,
				"error":   map[string]any{"code": ErrCodeSessionNotFound, "message": "session not found"},
			},
		},
	}
}

// goldenBytes 用真实 Go marshaler 把一条样本序列化成落盘字节。
// 落盘与比对共用它,守卫比的就是生成器此刻会写出的东西。
func goldenBytes(t *testing.T, gf goldenFrame) []byte {
	t.Helper()
	b, err := json.MarshalIndent(gf.body, "", "  ")
	require.NoError(t, err, "帧 %s 序列化失败", gf.name)
	return append(b, '\n')
}

// repoRoot 从当前工作目录向上找到带 go.mod 的仓库根。
// 样本目录因此由本仓自己定位,不依赖任何兄弟 checkout 存在。
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "从工作目录向上找不到 go.mod")
		dir = parent
	}
}

// writeGoldenSamples 把全部样本写进 dir(落盘与守卫的临时目录共用这一条路径)。
func writeGoldenSamples(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755), "创建黄金样本目录")
	for _, gf := range buildGoldenFrames(t) {
		out := filepath.Join(dir, gf.name+".json")
		require.NoError(t, os.WriteFile(out, goldenBytes(t, gf), 0o644), "写黄金样本 %s", gf.name)
	}
}

// listGoldenNames 列出目录里全部 .json 样本文件名(已排序)。
func listGoldenNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "读黄金样本目录 %s", dir)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// TestGoldenSamples 自检每条黄金样本的确定性 + 往返不丢字段。不写盘。
func TestGoldenSamples(t *testing.T) {
	for _, gf := range buildGoldenFrames(t) {
		t.Run(gf.name, func(t *testing.T) {
			b, err := json.MarshalIndent(gf.body, "", "  ")
			require.NoError(t, err)

			// 确定性:同一帧再 marshal 一次逐字节相同(Go 的 map key 排序保证稳定)。
			b2, err := json.MarshalIndent(gf.body, "", "  ")
			require.NoError(t, err)
			require.Equal(t, string(b), string(b2), "帧 %s 序列化不确定", gf.name)

			// 往返:map 形态解析再序列化再解析,两次解析的结构逐字段相同
			// (不丢字段,含未知字段)。比较解析后的结构而不是字节 —— struct 按声明序
			// 序列化、map 按键排序,逐字节比会误报"字段丢失"。
			var m map[string]any
			require.NoError(t, json.Unmarshal(b, &m), "帧 %s 不是合法 JSON", gf.name)
			b3, err := json.MarshalIndent(m, "", "  ")
			require.NoError(t, err)
			var m3 map[string]any
			require.NoError(t, json.Unmarshal(b3, &m3), "帧 %s 往返后不是合法 JSON", gf.name)
			require.Equal(t, m, m3, "帧 %s 往返后字段丢失", gf.name)

			// 注入的未知字段确实存在。
			for _, k := range gf.extraKeys {
				_, ok := m[k]
				require.True(t, ok, "帧 %s 应含未知字段 %q", gf.name, k)
			}
		})
	}
}

// TestGoldenFixturesFresh 新鲜度守卫:已提交的样本必须就是生成器此刻会写出的东西。
//
// 只自检「当下构造出来的帧」是不够的 —— 给 wire 结构加个字段,自检照样绿,而包里
// 已提交的样本还是旧形状,浏览器侧对着旧样本也照样绿,新字段就在 TS 侧静默消失了。
// 这里把样本生成到临时目录,与包里的副本比文件集 + 逐字节内容,把那条缝焊死。
//
// 守卫单仓自足:样本目录由 repoRoot 定位,不读取也不探测任何兄弟仓库。
func TestGoldenFixturesFresh(t *testing.T) {
	committed := filepath.Join(repoRoot(t), goldenFixturesRel)
	fresh := t.TempDir()
	writeGoldenSamples(t, fresh)

	// 文件集一致:删了生成器里的样本却留着 .json(或反过来)同样是漂移。
	require.Equal(t, listGoldenNames(t, fresh), listGoldenNames(t, committed),
		"%s 的样本文件集与生成器不一致,重新生成:\n\t%s", goldenFixturesRel, regenCmd)

	for _, gf := range buildGoldenFrames(t) {
		t.Run(gf.name, func(t *testing.T) {
			// G304:路径由 repoRoot + 本文件里的常量目录 + 本文件里写死的样本名拼出,
			// 没有外部输入参与,且只在测试里读本仓已提交的样本。
			got, err := os.ReadFile(filepath.Join(committed, gf.name+".json")) //nolint:gosec // 见上
			require.NoError(t, err, "读已提交的样本 %s", gf.name)
			require.Equal(t, string(goldenBytes(t, gf)), string(got),
				"黄金样本 %s 已过期(wire 结构改了但样本没重新生成),重新生成:\n\t%s", gf.name, regenCmd)
		})
	}
}

// TestWriteGoldenSamples 重新生成黄金样本,把产物写进本仓的 agentre-wire 包。
// 写盘是显式动作:不带 WIRE_GOLDEN_WRITE=1 直接跳过,`go test ./...` 因此不写盘。
func TestWriteGoldenSamples(t *testing.T) {
	if os.Getenv("WIRE_GOLDEN_WRITE") != "1" {
		t.Skip("设 WIRE_GOLDEN_WRITE=1 重新生成黄金样本")
	}
	dir := filepath.Join(repoRoot(t), goldenFixturesRel)
	writeGoldenSamples(t, dir)
	t.Logf("黄金样本已写入 %s", dir)
}
