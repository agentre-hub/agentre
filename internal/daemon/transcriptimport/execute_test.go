package transcriptimport_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cago-frame/agents/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/transcriptimport"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	runtimewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	pkgimport "github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport/wire"
)

// TestExecute_OwnsTheSessionAndPersistsEveryTurn 是执行侧的主路径:一次导入之后,
// 这条会话在**这台机器**名下有身份行(转录的 cwd / provider 会话身份 / 后端 / 标题),
// 回放出的每一轮都落成与跑一轮**同形**的转录(用户一行 + 一条 assistant 消息),
// 普通的 SESSION_PULL 因此就能把它服务出去,不需要第二条镜像通路。
func TestExecute_OwnsTheSessionAndPersistsEveryTurn(t *testing.T) {
	started := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	rig := newExecuteRig(t, &fakeTranscript{
		meta: pkgimport.Meta{
			Backend: agent_backend_entity.TypeClaudeCode, ProviderSessionID: "prov-1",
			Title: "磁盘上那条", Cwd: "/srv/work", Turns: 2,
		},
		turns: []pkgimport.Turn{
			{
				Index: 0, UserText: "第一问", Model: "claude-opus-5", StartedAt: started,
				Events: []agentruntime.Event{agentruntime.TextDelta{Text: "第一答"}},
				Usage:  &provider.Usage{PromptTokens: 11, CompletionTokens: 7},
			},
			{
				Index: 1, UserText: "第二问", Model: "claude-opus-5", ForkAnchor: "anchor-2",
				Events:    []agentruntime.Event{agentruntime.ToolCall{ID: "t1", Name: "Read", Input: []byte(`{}`)}},
				ErrorText: "被中断",
			},
		},
	})

	got, err := rig.handlers.Execute(context.Background(), wire.ExecuteParams{
		Backend: string(agent_backend_entity.TypeClaudeCode), Locator: "loc-1",
		ConversationID: convID(907), AgentID: 42, AgentSyncID: "agent-sync-1",
	})

	require.NoError(t, err)
	assert.Equal(t, convID(907), got.ConversationID)
	assert.Equal(t, "prov-1", got.ProviderSessionID)
	assert.Equal(t, "/srv/work", got.Cwd)
	assert.Equal(t, 2, got.Turns)
	assert.False(t, got.AlreadyImported)

	require.Len(t, rig.sessions.started, 1, "导入的会话归这台机器所有,身份行必须落库")
	row := rig.sessions.started[0]
	assert.Equal(t, convID(907), row.PeerSessionID)
	assert.Equal(t, "/srv/work", row.Cwd, "续跑要回到转录记的那个目录")
	assert.Equal(t, "prov-1", row.ProviderSessionID, "续跑要对上那条 provider 原生会话")
	assert.Equal(t, string(agent_backend_entity.TypeClaudeCode), row.BackendType)
	assert.Equal(t, "磁盘上那条", row.Title)
	assert.Equal(t, int64(42), row.AgentID)
	assert.Equal(t, "agent-sync-1", row.AgentSyncID)
	assert.Equal(t, runtimewire.SessionLifecycleIdle, row.LifecycleState, "导完就在等下一轮,不是在跑")

	// 一轮 = 用户那一行 + 一条 assistant 消息;正文由共用的累积器攒出来,与跑一轮
	// 落下的那一份出自同一行代码。
	assert.Equal(t, []string{"user", "assistant", "user", "assistant"}, rig.transcript.roles())

	msgs := rig.transcript.msgs
	assert.Equal(t, `[{"type":"text","data":{"text":"第一问"}}]`, msgs[0].BlocksJSON,
		"用户那一行来自转录原文")
	assert.Equal(t, `[{"type":"text","data":{"text":"第一答"}}]`, msgs[1].BlocksJSON)
	assert.Equal(t, "claude-opus-5", msgs[1].Model)
	assert.Equal(t, 11, msgs[1].PromptTokens)
	assert.Equal(t, 7, msgs[1].CompletionTokens)

	assert.Equal(t, `[{"type":"text","data":{"text":"第二问"}}]`, msgs[2].BlocksJSON)
	assert.Contains(t, msgs[3].BlocksJSON, `"type":"tool_use"`)
	assert.Equal(t, "被中断", msgs[3].ErrorText)
	assert.Equal(t, "anchor-2", msgs[3].ForkAnchor, "续跑锚点跟着那一轮走")
}

// TestExecute_SecondImportOfTheSameProviderSessionReusesTheSession:同一台对端把同
// 一条 provider 会话导第二次,必须指回库里那条 —— 既不建第二条会话,也不往日志里再
// 叠一份转录(叠上去客户端会把整段历史读成"又发生了一遍")。
func TestExecute_SecondImportOfTheSameProviderSessionReusesTheSession(t *testing.T) {
	rig := newExecuteRig(t, &fakeTranscript{
		meta:  pkgimport.Meta{ProviderSessionID: "prov-1", Cwd: "/srv/work"},
		turns: makeTurns(2),
	})
	params := wire.ExecuteParams{
		Backend: string(agent_backend_entity.TypeClaudeCode), Locator: "loc-1", ConversationID: convID(907), AgentID: 42,
	}
	first, err := rig.handlers.Execute(context.Background(), params)
	require.NoError(t, err)
	persisted := len(rig.transcript.msgs)

	// 第二次连对话身份都换了:判重的锚点是 provider 会话身份,不是调用方铸的号。
	params.ConversationID = convID(908)
	second, err := rig.handlers.Execute(context.Background(), params)

	require.NoError(t, err)
	assert.True(t, second.AlreadyImported)
	assert.Equal(t, first.ConversationID, second.ConversationID, "指回库里那条,不建第二条")
	assert.Equal(t, 0, second.Turns)
	assert.Len(t, rig.sessions.started, 1)
	assert.Len(t, rig.transcript.msgs, persisted, "转录一条都不该再涨")
}

// TestExecute_RefusesToOverwriteAnotherSessionOnTheSameID:调用方铸的号已经被这台
// 对端的**另一条**会话占着时必须拒掉。会话 id 是各客户端本地自增的,复用一个正在跑
// 的号会把那条会话的身份行改写成一份磁盘转录的元信息,而它的日志还在继续涨。
func TestExecute_RefusesToOverwriteAnotherSessionOnTheSameID(t *testing.T) {
	rig := newExecuteRig(t, &fakeTranscript{
		meta:  pkgimport.Meta{ProviderSessionID: "prov-1", Cwd: "/srv/work"},
		turns: makeTurns(1),
	})
	rig.sessions.put(handlers.SessionRecord{
		PeerSessionID: convID(907), ProviderSessionID: "prov-other", BackendType: "claudecode",
		LifecycleState: runtimewire.SessionLifecycleRunning,
	})

	_, err := rig.handlers.Execute(context.Background(), wire.ExecuteParams{
		Backend: string(agent_backend_entity.TypeClaudeCode), Locator: "loc-1", ConversationID: convID(907),
	})

	require.ErrorIs(t, err, wire.ErrSessionInUse)
	assert.Empty(t, rig.sessions.started, "被占的号上一行都不该写")
	assert.Empty(t, rig.transcript.msgs)
}

// TestExecute_ClearsALeftoverTranscriptBeforeReplaying:上一次导入写到一半失败会在库里
// 留下一段残留转录。同号重来必须先把它清掉,否则两次回放会首尾相接叠成一份双倍长的转录。
func TestExecute_ClearsALeftoverTranscriptBeforeReplaying(t *testing.T) {
	rig := newExecuteRig(t, &fakeTranscript{
		meta:  pkgimport.Meta{ProviderSessionID: "prov-1", Cwd: "/srv/work"},
		turns: makeTurns(1),
	})
	rig.transcript.msgs = append(rig.transcript.msgs, &transcript_entity.Message{
		Role: "assistant", BlocksJSON: "残留",
	})
	rig.transcript.conv = append(rig.transcript.conv, convID(907))

	_, err := rig.handlers.Execute(context.Background(), wire.ExecuteParams{
		Backend: string(agent_backend_entity.TypeClaudeCode), Locator: "loc-1", ConversationID: convID(907),
	})

	require.NoError(t, err)
	assert.Equal(t, []string{convID(907)}, rig.purged, "同一条对话上的残留转录先清")
	for _, row := range rig.transcript.msgs {
		assert.NotEqual(t, "残留", row.BlocksJSON)
	}
}

// TestExecute_LeavesNoSessionWhenTheReplayFails:回放中途出错时不留下身份行 —— 留着的
// 话下一次同号重来会被判成"已导过"并直接指回去,那条会话就永远停在半截转录上。
// 转录按本机会话主键挂靠,所以身份行必须先建;失败路径把它连同半截转录一起撤掉。
func TestExecute_LeavesNoSessionWhenTheReplayFails(t *testing.T) {
	rig := newExecuteRig(t, &fakeTranscript{
		meta:          pkgimport.Meta{ProviderSessionID: "prov-1", Cwd: "/srv/work"},
		turns:         makeTurns(3),
		yieldErr:      errors.New("转录第二轮读坏了"),
		yieldErrAfter: 1,
	})

	_, err := rig.handlers.Execute(context.Background(), wire.ExecuteParams{
		Backend: string(agent_backend_entity.TypeClaudeCode), Locator: "loc-1", ConversationID: convID(907),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "转录第二轮读坏了")
	assert.Empty(t, rig.sessions.rows, "半截转录不许留下一条看着导完了的会话")
	assert.Empty(t, rig.transcript.msgs, "半截转录也一并撤掉")
}

// TestExecute_RejectsAnEmptySessionID:会话 id 由调用方铸,零值没法定位任何一行 ——
// 拿它当会话身份会让所有零号导入互相覆盖。
func TestExecute_RejectsAnEmptySessionID(t *testing.T) {
	rig := newExecuteRig(t, &fakeTranscript{meta: pkgimport.Meta{ProviderSessionID: "prov-1"}})

	_, err := rig.handlers.Execute(context.Background(), wire.ExecuteParams{
		Backend: string(agent_backend_entity.TypeClaudeCode), Locator: "loc-1",
	})

	require.Error(t, err)
	assert.Empty(t, rig.transcript.msgs)
}

// ── fakes ───────────────────────────────────────────────────────────────────

type executeRig struct {
	handlers   *transcriptimport.Handlers
	sessions   *fakeSessionStore
	transcript *fakeTranscriptStore
	purged     []string
}

func newExecuteRig(t *testing.T, source *fakeTranscript) *executeRig {
	t.Helper()
	rig := &executeRig{sessions: newFakeSessionStore(), transcript: &fakeTranscriptStore{}}
	src := &fakeSource{backend: agent_backend_entity.TypeClaudeCode, transcript: source}
	rig.handlers = transcriptimport.NewHandlers(transcriptimport.Options{
		Sources:    func() []pkgimport.Source { return []pkgimport.Source{src} },
		Sessions:   rig.sessions,
		Transcript: rig.transcript,
		TranscriptPurge: purgeFunc(func(_ context.Context, _, peerSessionID string) (int64, error) {
			rig.purged = append(rig.purged, peerSessionID)
			return rig.transcript.deleteAll(peerSessionID), nil
		}),
		SessionDelete: rig.sessions,
	})
	return rig
}

type purgeFunc func(ctx context.Context, peerFingerprint, peerSessionID string) (int64, error)

func (f purgeFunc) DeleteAll(ctx context.Context, peerFingerprint, peerSessionID string) (int64, error) {
	return f(ctx, peerFingerprint, peerSessionID)
}

type fakeSessionStore struct {
	rows    map[string]handlers.SessionRecord
	started []handlers.SessionRecord
}

func newFakeSessionStore() *fakeSessionStore {
	return &fakeSessionStore{rows: map[string]handlers.SessionRecord{}}
}

func (f *fakeSessionStore) put(rec handlers.SessionRecord) { f.rows[rec.PeerSessionID] = rec }

func (f *fakeSessionStore) Find(_ context.Context, _, peerSessionID string) (*handlers.SessionRecord, error) {
	row, ok := f.rows[peerSessionID]
	if !ok {
		return nil, nil
	}
	return &row, nil
}

func (f *fakeSessionStore) List(_ context.Context, _, _ string, _, _ int) ([]handlers.SessionRecord, error) {
	out := make([]handlers.SessionRecord, 0, len(f.rows))
	for _, row := range f.rows {
		out = append(out, row)
	}
	return out, nil
}

func (f *fakeSessionStore) Start(_ context.Context, rec handlers.SessionRecord) error {
	f.started = append(f.started, rec)
	f.put(rec)
	return nil
}

func (f *fakeSessionStore) Delete(_ context.Context, _, peerSessionID string) (int64, error) {
	if _, ok := f.rows[peerSessionID]; !ok {
		return 0, nil
	}
	delete(f.rows, peerSessionID)
	kept := make([]handlers.SessionRecord, 0, len(f.started))
	for _, rec := range f.started {
		if rec.PeerSessionID != peerSessionID {
			kept = append(kept, rec)
		}
	}
	f.started = kept
	return 1, nil
}

// fakeTranscriptStore 是 transcriptimport.Transcript 的替身:按到达顺序记下写进去的
// 每一条消息。它不解释块 —— 块怎么攒出来归共用的累积器,这里只看落下的是什么。
type fakeTranscriptStore struct {
	msgs []*transcript_entity.Message
	conv []string
	next int64
}

func (f *fakeTranscriptStore) StartTurn(
	_ context.Context, conversationID, userText string,
) (*transcript_entity.Message, *transcript_entity.Message, error) {
	var user *transcript_entity.Message
	if userText != "" {
		f.next++
		user = &transcript_entity.Message{
			ID: f.next, Role: "user",
			BlocksJSON: `[{"type":"text","data":{"text":"` + userText + `"}}]`,
		}
		f.msgs = append(f.msgs, user)
		f.conv = append(f.conv, conversationID)
	}
	f.next++
	assistant := &transcript_entity.Message{ID: f.next, Role: "assistant", BlocksJSON: "[]"}
	f.msgs = append(f.msgs, assistant)
	f.conv = append(f.conv, conversationID)
	return user, assistant, nil
}

func (f *fakeTranscriptStore) FinishTurn(_ context.Context, _ *transcript_entity.Message) error {
	// 实体是按指针交出去的,收口时调用方就地改的就是 msgs 里那一条。
	return nil
}

func (f *fakeTranscriptStore) deleteAll(conversationID string) int64 {
	keptMsgs := make([]*transcript_entity.Message, 0, len(f.msgs))
	keptConv := make([]string, 0, len(f.conv))
	var removed int64
	for i, row := range f.msgs {
		if i < len(f.conv) && f.conv[i] == conversationID {
			removed++
			continue
		}
		keptMsgs = append(keptMsgs, row)
		if i < len(f.conv) {
			keptConv = append(keptConv, f.conv[i])
		}
	}
	f.msgs, f.conv = keptMsgs, keptConv
	return removed
}

func (f *fakeTranscriptStore) roles() []string {
	out := make([]string, 0, len(f.msgs))
	for _, row := range f.msgs {
		out = append(out, row.Role)
	}
	return out
}

// convID 把一个短会话号折成一条**格式合法**的 conversation_id,只在测试里用:
// 线上身份是 uuid,而这些用例真正要断言的是"同一个值原样往返"与"两条不同的对话
// 互不并轨",一个可读、可复现的映射比随机 uuid 更好读。
func convID(n int64) string {
	return fmt.Sprintf("00000000-0000-7000-8000-%012d", n)
}
