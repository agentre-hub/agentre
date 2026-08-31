package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/protobufadapter"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	daemonimport "github.com/agentre-hub/agentre/internal/daemon/transcriptimport"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	remotewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	pkgimport "github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// TestProtobufTranscriptImportScanCarriesPerBackendState 经真实 protorpc 往返:
// 已配对 daemon 按后端分别答 ok / unavailable。「问出来就是没有」与「这台机器答不出」
// 必须在字节流里分得开 —— 否则少装一个 CLI 与目录读不动在 UI 上是同一句话。
func TestProtobufTranscriptImportScanCarriesPerBackendState(t *testing.T) {
	client, ctx := transcriptImportPeers(t, daemonimport.NewHandlers(daemonimport.Options{
		Sources: func() []pkgimport.Source {
			return []pkgimport.Source{
				&transcriptFakeSource{backend: agent_backend_entity.TypeClaudeCode, candidates: []pkgimport.Candidate{{
					Backend: agent_backend_entity.TypeClaudeCode, ProviderSessionID: "s-1", Title: "远端那条",
					Cwd: "/srv/work", EndedAt: time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC), Turns: 6,
					Origin: pkgimport.OriginTerminal, Locator: "loc-1",
				}}},
				&transcriptFakeSource{backend: agent_backend_entity.TypeCodex},
				&transcriptFakeSource{backend: agent_backend_entity.TypePiAgent, scanErr: errors.New("permission denied")},
			}
		},
	}))

	response, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_SCAN),
		protowire.TranscriptScanParamsToProto(wire.ScanParams{Filter: pkgimport.Filter{CwdPrefix: "/srv"}}),
		func() *agentrewire.TranscriptImportScanResponse { return &agentrewire.TranscriptImportScanResponse{} })
	require.NoError(t, err)

	got := protowire.TranscriptScanResultFromProto(response)
	require.Len(t, got.Backends, 3)
	assert.Equal(t, wire.StatusOK, got.Backends[0].Status)
	require.Len(t, got.Backends[0].Candidates, 1)
	assert.Equal(t, "s-1", got.Backends[0].Candidates[0].ProviderSessionID)
	assert.Equal(t, pkgimport.Locator("loc-1"), got.Backends[0].Candidates[0].Locator)
	assert.Equal(t, wire.StatusOK, got.Backends[1].Status, "问出来就是没有,仍然是 ok")
	assert.Empty(t, got.Backends[1].Candidates)
	assert.Equal(t, wire.StatusUnavailable, got.Backends[2].Status)
	assert.Contains(t, got.Backends[2].Reason, "permission denied")
}

// TestProtobufTranscriptImportStreamsTurnsPageByPage 经真实 protorpc 往返分页取回
// 轮次:每页只带这一页,事件序列与磁盘时间原样过 wire。
func TestProtobufTranscriptImportStreamsTurnsPageByPage(t *testing.T) {
	started := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	turns := make([]pkgimport.Turn, 0, 3)
	for i := range 3 {
		turns = append(turns, pkgimport.Turn{
			Index: i, UserText: "问题", Model: "claude-opus-5", StartedAt: started,
			Events: []agentruntime.Event{agentruntime.ToolCall{ID: "t", Name: "Read", Input: []byte(`{}`)}},
		})
	}
	client, ctx := transcriptImportPeers(t, daemonimport.NewHandlers(daemonimport.Options{
		Sources: func() []pkgimport.Source {
			return []pkgimport.Source{&transcriptFakeSource{
				backend:    agent_backend_entity.TypeClaudeCode,
				transcript: &transcriptFakeTranscript{meta: pkgimport.Meta{ProviderSessionID: "s-1", Turns: 3}, turns: turns},
			}}
		},
	}))

	call := func(start int) wire.TurnsResult {
		t.Helper()
		response, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_TURNS),
			protowire.TranscriptTurnsParamsToProto(wire.TurnsParams{Backend: "claudecode", Locator: "loc-1", StartIndex: start, MaxTurns: 2}),
			func() *agentrewire.TranscriptImportTurnsResponse { return &agentrewire.TranscriptImportTurnsResponse{} })
		require.NoError(t, err)
		page, err := protowire.TranscriptTurnsResultFromProto(response)
		require.NoError(t, err)
		return page
	}

	first := call(0)
	require.Len(t, first.Turns, 2)
	assert.True(t, first.HasMore)
	assert.Equal(t, 2, first.NextIndex)
	assert.Equal(t, []agentruntime.Event{agentruntime.ToolCall{ID: "t", Name: "Read", Input: []byte(`{}`)}}, first.Turns[0].Events)
	assert.True(t, started.Equal(first.Turns[0].StartedAt), "时间取磁盘值,不是导入时刻")

	second := call(first.NextIndex)
	require.Len(t, second.Turns, 1)
	assert.Equal(t, 2, second.Turns[0].Index)
	assert.False(t, second.HasMore, "最后一页说清后面没有了")

	// 元信息与缺口同样过真实往返 —— 远端导入也要在导入前说清缺口。
	openResponse, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_OPEN),
		protowire.TranscriptOpenParamsToProto(wire.OpenParams{Backend: "claudecode", Locator: "loc-1"}),
		func() *agentrewire.TranscriptImportOpenResponse { return &agentrewire.TranscriptImportOpenResponse{} })
	require.NoError(t, err)
	assert.Equal(t, "s-1", protowire.TranscriptOpenResultFromProto(openResponse).Meta.ProviderSessionID)
}

// TestProtobufTranscriptImportOnOldDaemonIsMethodNotFound 是硬约束 3 的判据:
// 不认识这个方法族的旧 agentred 回 -32601,而不是一个看着正常的空应答。
// host 侧据这个码报 unsupported —— 静默的空列表会被读成「这台机器没有会话」。
func TestProtobufTranscriptImportOnOldDaemonIsMethodNotFound(t *testing.T) {
	client, ctx := transcriptImportPeers(t, nil)

	_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_SCAN),
		&agentrewire.TranscriptImportScanRequest{},
		func() *agentrewire.TranscriptImportScanResponse { return &agentrewire.TranscriptImportScanResponse{} })

	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, protorpc.CodeMethodNotFound, rpcErr.Code)
}

// TestProtobufTranscriptImportUnknownBackendKeepsTypedCode 这台机器上没有那个后端的
// 读取器时给出稳定的 typed code,而不是笼统的 internal —— 与「daemon 太旧」区分开。
func TestProtobufTranscriptImportUnknownBackendKeepsTypedCode(t *testing.T) {
	client, ctx := transcriptImportPeers(t, daemonimport.NewHandlers(daemonimport.Options{
		Sources: func() []pkgimport.Source { return nil },
	}))

	_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_OPEN),
		&agentrewire.TranscriptImportOpenRequest{Backend: "codex", Locator: "loc"},
		func() *agentrewire.TranscriptImportOpenResponse { return &agentrewire.TranscriptImportOpenResponse{} })

	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, int32(wire.ErrCodeBackendUnavailable), rpcErr.Code)
}

// ── harness ─────────────────────────────────────────────────────────────────

func transcriptImportPeers(t *testing.T, handlers *daemonimport.Handlers) (*protorpc.Conn, context.Context) {
	t.Helper()
	registry := protorpc.NewRegistry()
	protobufadapter.RegisterPeripheralMethods(registry, protobufadapter.PeripheralDeps{TranscriptImport: handlers})
	clientTransport, serverTransport := protobufTestPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, registry)
	server.SetAuth(protorpc.AuthState{Authenticated: true})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go client.Serve(ctx)
	go server.Serve(ctx)
	return client, ctx
}

type transcriptFakeTranscript struct {
	meta  pkgimport.Meta
	turns []pkgimport.Turn
}

func (f *transcriptFakeTranscript) Meta() pkgimport.Meta { return f.meta }

func (f *transcriptFakeTranscript) Turns(ctx context.Context, yield func(pkgimport.Turn) error) error {
	for _, turn := range f.turns {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := yield(turn); err != nil {
			return err
		}
	}
	return nil
}

func (f *transcriptFakeTranscript) Close() error { return nil }

type transcriptFakeSource struct {
	backend    agent_backend_entity.BackendType
	candidates []pkgimport.Candidate
	scanErr    error
	transcript *transcriptFakeTranscript
}

func (f *transcriptFakeSource) Backend() agent_backend_entity.BackendType { return f.backend }

func (f *transcriptFakeSource) Scan(_ context.Context, _ pkgimport.Filter) ([]pkgimport.Candidate, error) {
	if f.scanErr != nil {
		return nil, f.scanErr
	}
	return f.candidates, nil
}

func (f *transcriptFakeSource) Open(_ context.Context, _ pkgimport.Locator) (pkgimport.Transcript, error) {
	if f.transcript == nil {
		return nil, errors.New("no transcript")
	}
	return f.transcript, nil
}

// TestProtobufTranscriptImportExecuteOwnsTheSessionAndFeedsCatchup 是执行侧的终局
// 判据,走的是**真的那台 daemon**(真库、真配对、真 protorpc):导完之后,
// SESSION_LIST 报出这条会话带着转录的工作目录与 provider 会话身份(下一轮据此在那个
// 目录里、对着那条原生会话续跑),SESSION_PULL 把回放出的轮次按序服务出去 ——
// 导入的会话因此和别的会话走同一条镜像通路,不需要第二条。
func TestProtobufTranscriptImportExecuteOwnsTheSessionAndFeedsCatchup(t *testing.T) {
	restore := pkgimport.SwapSourceForTest(agent_backend_entity.TypeClaudeCode, &transcriptFakeSource{
		backend: agent_backend_entity.TypeClaudeCode,
		transcript: &transcriptFakeTranscript{
			meta: pkgimport.Meta{
				Backend: agent_backend_entity.TypeClaudeCode, ProviderSessionID: "prov-e2e",
				Title: "磁盘上那条", Cwd: "/srv/imported", Turns: 2,
			},
			turns: []pkgimport.Turn{
				{Index: 0, UserText: "第一问", Model: "claude-opus-5",
					Events: []agentruntime.Event{agentruntime.TextDelta{Text: "第一答"}}},
				{Index: 1, UserText: "第二问", Model: "claude-opus-5",
					Events: []agentruntime.Event{agentruntime.ToolCall{ID: "t1", Name: "Read", Input: []byte(`{}`)}}},
			},
		},
	})
	t.Cleanup(restore)
	rig := bootRemoteRig(t, nil)

	execute := func(conversationID string) wire.ExecuteResult {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		response, err := protorpc.CallMethod(ctx, rig.cli.Conn(),
			uint32(agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_EXECUTE),
			protowire.TranscriptExecuteParamsToProto(wire.ExecuteParams{
				Backend: string(agent_backend_entity.TypeClaudeCode), Locator: "loc-1",
				ConversationID: conversationID, AgentID: 7, AgentSyncID: "agent-sync-e2e",
			}),
			func() *agentrewire.TranscriptImportExecuteResponse {
				return &agentrewire.TranscriptImportExecuteResponse{}
			})
		require.NoError(t, err)
		return protowire.TranscriptExecuteResultFromProto(response)
	}

	imported := execute(convID(4242))
	assert.Equal(t, convID(4242), imported.ConversationID)
	assert.Equal(t, 2, imported.Turns)
	assert.False(t, imported.AlreadyImported)

	var list remotewire.SessionListResult
	require.NoError(t, callRig(t, rig.cli, remotewire.MethodSessionList, nil, &list))
	require.Len(t, list.Sessions, 1, "导入的会话归这台机器所有,清单里必须有它")
	session := list.Sessions[0]
	assert.Equal(t, convID(4242), session.ConversationID)
	assert.Equal(t, "/srv/imported", session.Cwd, "续跑要回到转录记的那个目录")
	assert.Equal(t, "prov-e2e", session.ProviderSessionID, "续跑要对上那条 provider 原生会话")
	assert.Equal(t, string(agent_backend_entity.TypeClaudeCode), session.BackendType)
	assert.Equal(t, "磁盘上那条", session.Title)
	assert.Equal(t, "agent-sync-e2e", session.AgentSyncID)
	assert.Equal(t, remotewire.SessionLifecycleIdle, session.LifecycleState, "导完在等下一轮,不是在跑")
	assert.Equal(t, int64(8), session.LatestSeq, "最新 seq 取自通知日志:两轮各 4 条")

	methods, events := pullTranscript(t, rig, convID(4242))
	assert.Equal(t, []string{
		remotewire.NotifyEvent, remotewire.NotifyEvent, remotewire.NotifyEvent, remotewire.NotifyRunResultDone,
		remotewire.NotifyEvent, remotewire.NotifyEvent, remotewire.NotifyEvent, remotewire.NotifyRunResultDone,
	}, methods, "补齐服务的就是那两轮本该发出的通知,按序")
	require.Len(t, events, 6)
	assert.Equal(t, agentruntime.UserMessageEvent{Text: "第一问"}, events[0])
	assert.Equal(t, agentruntime.TextDelta{Text: "第一答"}, events[1])
	assert.Equal(t, agentruntime.Done{}, events[2])
	assert.Equal(t, agentruntime.UserMessageEvent{Text: "第二问"}, events[3])
	assert.Equal(t, agentruntime.ToolCall{ID: "t1", Name: "Read", Input: []byte(`{}`)}, events[4])

	// 同一条 provider 会话再导一次:指回库里那条,既不建第二条会话,也不往日志里
	// 再叠一份转录 —— 叠上去客户端会把整段历史读成「又发生了一遍」。
	again := execute(convID(4243))
	assert.True(t, again.AlreadyImported)
	assert.Equal(t, convID(4242), again.ConversationID)
	assert.Equal(t, 0, again.Turns)

	var after remotewire.SessionListResult
	require.NoError(t, callRig(t, rig.cli, remotewire.MethodSessionList, nil, &after))
	require.Len(t, after.Sessions, 1, "第二次导入不得建第二条会话")
	assert.Equal(t, int64(8), after.Sessions[0].LatestSeq, "日志一条都不该再涨")
}

// pullTranscript 按游标把整段日志拉平,交回每一行的 method 与其中的事件。
func pullTranscript(t *testing.T, rig *pairedTestRig, conversationID string) ([]string, []agentruntime.Event) {
	t.Helper()
	var (
		methods []string
		events  []agentruntime.Event
		cursor  int64
	)
	for pages := 0; ; pages++ {
		require.Less(t, pages, 10, "翻页没有收敛")
		var page remotewire.SessionPullResult
		require.NoError(t, callRig(t, rig.cli, remotewire.MethodSessionPull,
			remotewire.SessionPullParams{ConversationID: conversationID, Cursor: cursor}, &page))
		for _, notification := range page.Notifications {
			methods = append(methods, notification.Method)
			if notification.Method == remotewire.NotifyEvent {
				// callRig 把每条通知的 params 交成线上 JSON 原样,事件由 EventFrame
				// 自己的解码器还原成密封事件 —— 与桌面端补齐走的是同一条路径。
				raw, ok := notification.Params.([]byte)
				require.True(t, ok, "日志行必须带上那条通知的 params 原样")
				var frame remotewire.EventFrame
				require.NoError(t, json.Unmarshal(raw, &frame))
				events = append(events, frame.Event)
			}
			cursor = notification.Seq
		}
		if !page.HasMore {
			return methods, events
		}
	}
}
