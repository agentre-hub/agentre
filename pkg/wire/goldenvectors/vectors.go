// Package goldenvectors 是 wire 协议**二进制**金向量的主人:一批由 Go 侧真实
// marshaler 序列化出来的帧,连同各自用确定性 JSON 写下的期望字段值。
//
// 它服务的是「同一份 schema 的第二个语言实现解得对不对」这件事。既有的
// frontend/packages/agentre-wire/fixtures/ 那 23 份 JSON 回答不了这个问题:那是
// internal/.../remote/wire 里**手写 JSON 领域结构**的金样本(与 src/codec.gen.ts
// 由同一个 Go 生成器产出),而中继上跑的是 Request{method_id, encoded_payload}
// 的二进制 protobuf,原生端直接消费 protobuf 结构,根本不经过那一层。
//
// 判据**不是字节逐一相等**。protobuf 不保证跨实现的字节级一致(map 序、未知字段
// 的摆放位置都可以不同),把字节相等当判据会得到一条随实现升级而红的测试。判据是:
//
//   - Swift 解 .bin 得到的消息,与它解同名 .json 得到的消息相等,且逐个字段等于
//     期望值(断言写在 Swift 测试源码里,读得出在验什么);
//   - Swift 把解出来的消息再编码回去,Go 解得到与原消息 proto.Equal 的消息。
//
// 向量的新鲜度由 vectors_test.go 的守卫冻结;重新生成:
//
//	WIRE_VECTORS_WRITE=1 go test -C pkg/wire ./goldenvectors/ -run TestWriteVectors
package goldenvectors

import (
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// Dir 是向量在本包下的目录名。生成物与生成器同住,与 pkg/wire 的其它包一致。
const Dir = "vectors"

// WriteEnv 置 1 时 TestWriteVectors 才写盘,`go test ./...` 因此不写盘。
const WriteEnv = "WIRE_VECTORS_WRITE"

// RegenCmd 向量过期时重新生成的确切命令,原样出现在守卫的失败信息里。
const RegenCmd = "WIRE_VECTORS_WRITE=1 go test -C pkg/wire ./goldenvectors/ -run TestWriteVectors"

// Vector 一条金向量:名字 + 一条真实的 wire 消息。
//
// Name 同时是 vectors/ 下那两个文件的 basename,Swift 测试按同一个名字取用。
type Vector struct {
	Name    string
	Message proto.Message
}

// Vectors 全部金向量。
//
// 覆盖面对着 iOS 真正走的那条通路挑:中继上载的是 Request{method_id,
// encoded_payload},所以会话族(list / counts / attach / pull)、运行族与带 oneof
// 和嵌套消息的那几条都要在;oneof **未设**分支与全默认值消息各留一条 —— 那是跨实现
// 最容易分歧的两处(一个实现把未设 oneof 读成某个分支、或把默认值当成"有值"写上线,
// 都只在对拍时才暴露)。
func Vectors() []Vector {
	const (
		sid      = "00000000-0000-7000-8000-000000000042"
		otherSID = "00000000-0000-7000-8000-000000000008"
		fp       = "fp-desktop"
	)

	usage := &agentrewire.Usage{
		PromptTokens:        100,
		CompletionTokens:    50,
		ReasoningTokens:     10,
		CachedTokens:        5,
		CacheCreationTokens: 2,
		TotalTokens:         155,
	}
	runDone := &agentrewire.RunResultDoneNotification{
		ConversationId:    sid,
		Seq:               12,
		ProviderSessionId: "sess_abc123",
		Usage:             usage,
		UserAnchor:        "anchor-1",
		Model:             "claude-sonnet-4-5",
		ContextWindow:     200000,
		TurnToken:         9,
	}
	// 跑满一轮的会话与还没跑过第一轮的会话各一条:后者除身份外全是默认值,
	// 正是"默认值不该被当成有值"那条的载体。
	richSummary := &agentrewire.SessionSummary{
		ConversationId:    sid,
		PeerFingerprint:   fp,
		AgentId:           7,
		Title:             "重构登录页",
		AgentSyncId:       "01JZ7W2A8KZ4R5T6Y7U8I9O0P1Q",
		ProviderSessionId: "sess_abc123",
		Cwd:               "/home/agent/proj",
		BackendType:       "claudecode",
		LifecycleState:    "running",
		WaitingForInput:   true,
		LatestSeq:         12,
		LastMessageAt:     1754800000000,
		ReasoningEffort:   "high",
	}
	freshSummary := &agentrewire.SessionSummary{
		ConversationId: otherSID,
		AgentId:        3,
		Cwd:            "/var/proj",
		BackendType:    "codex",
		LifecycleState: "idle",
		LatestSeq:      5,
	}

	return []Vector{
		// ── 会话族 ──────────────────────────────────────────────────────────
		{"session-list-request", &agentrewire.SessionListRequest{
			Keyword: "登录", Limit: 50, Cursor: "MTc1NDgwMDAwMDAwMA==",
			ConversationIds: []string{sid, otherSID},
		}},
		{"session-list-response", &agentrewire.SessionListResponse{
			Sessions: []*agentrewire.SessionSummary{richSummary, freshSummary},
			Cursor:   "MTc1NDc5OTk5OTAwMA==", HasMore: true, Total: 137,
		}},
		// 三个计数全 0 是真实答案(这台机器一条会话都没有),不是"没答"。
		{"session-counts-response-zero", &agentrewire.SessionCountsResponse{}},
		{"session-counts-response", &agentrewire.SessionCountsResponse{
			Total: 137, Running: 2, Waiting: 3,
		}},
		{"session-attach-request", &agentrewire.SessionAttachRequest{
			ConversationId: sid, PeerFingerprint: fp,
		}},
		{"session-attach-response", &agentrewire.SessionAttachResponse{
			ConversationId: sid, BackendType: "claudecode",
			LifecycleState: "running", LatestSeq: 12,
		}},
		{"session-pull-request", &agentrewire.SessionPullRequest{
			ConversationId: sid, PeerFingerprint: fp, Cursor: 0, Limit: 200,
		}},
		// 补齐页:载体是 DurableNotification,里面又套一层 RpcNotification 的
		// oneof —— 嵌套 + oneof 同时在一条向量上。
		{"session-pull-response", &agentrewire.SessionPullResponse{
			Notifications: []*agentrewire.DurableNotification{
				{
					Seq: 11,
					Payload: &agentrewire.RpcNotification{
						Payload: &agentrewire.RpcNotification_RuntimeEvent{
							RuntimeEvent: &agentrewire.RuntimeEventNotification{
								ConversationId: sid, Seq: 11,
								Event: &agentrewire.RuntimeEventNotification_TextDelta{
									TextDelta: &agentrewire.TextDelta{Text: "你好"},
								},
							},
						},
					},
					Createtime: 1754800000000,
				},
				{
					Seq: 12,
					Payload: &agentrewire.RpcNotification{
						Payload: &agentrewire.RpcNotification_RunResultDone{RunResultDone: runDone},
					},
					// createtime 为 0 读作"源端没报过",不是 1970 —— 默认值在这里
					// 是一个有意义的答案。
				},
			},
			Cursor: 12, HasMore: false, OldestSeq: 1,
		}},

		// ── 运行族 ──────────────────────────────────────────────────────────
		// map<string,bool> / 嵌套 repeated / bytes 透传都在这一条上。
		{"runtime-run-request", &agentrewire.RuntimeRunRequest{
			Backend: &agentrewire.AgentBackend{
				Id: 7, Type: "claudecode", Name: "Claude",
				LlmProviderKey: "11111111-2222-3333-4444-555555555555",
			},
			AgentId: 7, ConversationId: sid, PeerFingerprint: fp,
			Cwd: "/home/agent/proj", Title: "重构登录页",
			AgentSyncId:  "01JZ7W2A8KZ4R5T6Y7U8I9O0P1Q",
			SystemPrompt: "你是 AgentRe 的 Agent。",
			UserText:     "把登录按钮改成蓝色",
			History: []*agentrewire.HistoryMessage{
				{Role: "user", Blocks: []*agentrewire.StoredBlock{
					{Type: "text", Data: []byte(`"上一轮的上下文"`)},
				}},
			},
			PermissionMode: "default", CollaborationMode: "manual",
			McpServers: []*agentrewire.MCPServer{{
				Name: "org", Url: "http://127.0.0.1:8899/mcp/org/",
				Headers: map[string]string{"Authorization": "Bearer tok"},
				Tools:   []string{"mcp__org__list"},
			}},
			EnabledPlugins:   map[string]bool{"auto-continue": true, "dangerous": false},
			LlmProviderKey:   "11111111-2222-3333-4444-555555555555",
			SourceDevice:     "fp-web-1",
			SourceDeviceName: "Chrome · macOS",
			ReasoningEffort:  "xhigh",
		}},
		// 无锚点恢复:provider_session_id 留空 + fresh_session=true。
		{"runtime-run-request-fresh", &agentrewire.RuntimeRunRequest{
			Backend:        &agentrewire.AgentBackend{Type: "claudecode"},
			AgentId:        7,
			ConversationId: sid,
			Cwd:            "/home/agent/proj",
			FreshSession:   true,
			PermissionMode: "default",
		}},
		{"runtime-run-response", &agentrewire.RuntimeRunResponse{
			ConversationId: sid, ProviderSessionId: "sess_abc123",
			LaunchPermissionMode: "default",
		}},
		{"run-result-done-notification", runDone},

		// ── oneof 与嵌套 ────────────────────────────────────────────────────
		{"runtime-event-text-delta", &agentrewire.RuntimeEventNotification{
			ConversationId: sid, Seq: 11,
			Event: &agentrewire.RuntimeEventNotification_TextDelta{
				TextDelta: &agentrewire.TextDelta{Text: "你好"},
			},
		}},
		// 另一级帧:预览帧不带 seq、带 preview —— 消费方据此判别,不能从 seq 是不是
		// 0 去猜,所以两级各留一条。
		{"runtime-event-preview", &agentrewire.RuntimeEventNotification{
			ConversationId: sid, Preview: true,
			Event: &agentrewire.RuntimeEventNotification_ThinkingDelta{
				ThinkingDelta: &agentrewire.ThinkingDelta{Text: "让我想想"},
			},
		}},
		// oneof **未设**:一条只有 conversation_id 的事件帧。解错这条的实现会把它
		// 读成某个分支(通常是第一个),界面上表现为凭空多出一条空文本增量。
		{"runtime-event-unset-oneof", &agentrewire.RuntimeEventNotification{
			ConversationId: sid, Seq: 7,
		}},
		{"rpc-notification-autonomous-turn-started", &agentrewire.RpcNotification{
			Payload: &agentrewire.RpcNotification_AutonomousTurnStarted{
				AutonomousTurnStarted: &agentrewire.AutonomousTurnStartedNotification{
					ConversationId: sid, Seq: 13, Trigger: "auto", TurnToken: 9,
				},
			},
		}},
		// 中继上真正载的那两个信封:请求与应答都是 method_id + 不透明字节。
		{"rpc-frame-request", &agentrewire.RpcFrame{
			Id: 7,
			Body: &agentrewire.RpcFrame_Request{Request: &agentrewire.Request{
				MethodId:       uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_LIST),
				EncodedPayload: mustMarshal(&agentrewire.SessionListRequest{Keyword: "登录", Limit: 50}),
			}},
		}},
		{"rpc-frame-error", &agentrewire.RpcFrame{
			Id: 8,
			Body: &agentrewire.RpcFrame_Error{Error: &agentrewire.RpcError{
				Code: -32602, Message: "invalid params", Details: []byte{0x00, 0x7f, 0xff},
			}},
		}},
		// 信封的 oneof 未设:一条只带 id 的帧。
		{"rpc-frame-unset-body", &agentrewire.RpcFrame{Id: 9}},
	}
}

// mustMarshal 序列化嵌进 Request.encoded_payload 的内层消息。
// 向量集是静态数据,这里出错等于本包写错了,没有可恢复的路径。
func mustMarshal(m proto.Message) []byte {
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil {
		panic("goldenvectors: " + err.Error())
	}
	return b
}
