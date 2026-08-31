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
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	runtimewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	pkgimport "github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport/wire"
)

// TestExecute_OwnsTheSessionAndJournalsEveryTurn 是执行侧的主路径:一次导入之后,
// 这条会话在**这台机器**名下有身份行(转录的 cwd / provider 会话身份 / 后端 / 标题),
// 回放出的每一轮都按序落进通知日志 —— 普通的 SESSION_PULL 因此就能把它服务出去,
// 不需要第二条镜像通路。
func TestExecute_OwnsTheSessionAndJournalsEveryTurn(t *testing.T) {
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

	// 一轮 = 用户那一行 + 事件 + (用量) + (错误) + Done,再由一条 runResultDone 收尾;
	// 收尾帧是补齐轮的终点,少了它客户端那一轮永远不结束。
	assert.Equal(t, []string{
		runtimewire.NotifyEvent, runtimewire.NotifyEvent, runtimewire.NotifyEvent,
		runtimewire.NotifyEvent, runtimewire.NotifyRunResultDone,
		runtimewire.NotifyEvent, runtimewire.NotifyEvent, runtimewire.NotifyEvent,
		runtimewire.NotifyEvent, runtimewire.NotifyRunResultDone,
	}, rig.journal.methods(t))

	events := rig.journal.events(t)
	assert.Equal(t, agentruntime.UserMessageEvent{Text: "第一问"}, events[0],
		"用户那一行来自转录,不带来源设备 —— 这一轮不是任何在线设备此刻发起的")
	assert.Equal(t, agentruntime.TextDelta{Text: "第一答"}, events[1])
	require.IsType(t, agentruntime.UsageUpdate{}, events[2])
	assert.Equal(t, 7, events[2].(agentruntime.UsageUpdate).Usage.CompletionTokens)
	assert.Equal(t, agentruntime.Done{}, events[3])
	assert.Equal(t, agentruntime.UserMessageEvent{Text: "第二问"}, events[4])
	require.IsType(t, agentruntime.ErrorEvent{}, events[6])
	assert.EqualError(t, events[6].(agentruntime.ErrorEvent).Err, "被中断")

	dones := rig.journal.dones(t)
	require.Len(t, dones, 2)
	assert.Equal(t, convID(907), dones[0].ConversationID)
	assert.Equal(t, "prov-1", dones[0].ProviderSessionID)
	assert.Equal(t, "claude-opus-5", dones[0].Model)
	require.NotNil(t, dones[0].Usage)
	assert.Equal(t, 11, dones[0].Usage.PromptTokens)
	assert.Equal(t, "anchor-2", dones[1].UserAnchor, "续跑锚点跟着那一轮走")
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
	journaled := len(rig.journal.rows)

	// 第二次连对话身份都换了:判重的锚点是 provider 会话身份,不是调用方铸的号。
	params.ConversationID = convID(908)
	second, err := rig.handlers.Execute(context.Background(), params)

	require.NoError(t, err)
	assert.True(t, second.AlreadyImported)
	assert.Equal(t, first.ConversationID, second.ConversationID, "指回库里那条,不建第二条")
	assert.Equal(t, 0, second.Turns)
	assert.Len(t, rig.sessions.started, 1)
	assert.Len(t, rig.journal.rows, journaled, "日志一条都不该再涨")
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
	assert.Empty(t, rig.journal.rows)
}

// TestExecute_ClearsALeftoverJournalBeforeReplaying:上一次导入写到一半失败会在库里
// 留下一段没有主人的日志(身份行**最后**才写,正是为了让这种残留认得出来)。同号重来
// 必须先把它清掉,否则两次回放会首尾相接叠成一份双倍长的转录。
func TestExecute_ClearsALeftoverJournalBeforeReplaying(t *testing.T) {
	rig := newExecuteRig(t, &fakeTranscript{
		meta:  pkgimport.Meta{ProviderSessionID: "prov-1", Cwd: "/srv/work"},
		turns: makeTurns(1),
	})
	rig.journal.rows = append(rig.journal.rows, journalEntry{peerSessionID: convID(907), payload: []byte("残留")})

	_, err := rig.handlers.Execute(context.Background(), wire.ExecuteParams{
		Backend: string(agent_backend_entity.TypeClaudeCode), Locator: "loc-1", ConversationID: convID(907),
	})

	require.NoError(t, err)
	assert.Equal(t, []string{convID(907)}, rig.purged, "同一条对话上的残留日志先清")
	for _, row := range rig.journal.rows {
		assert.NotEqual(t, []byte("残留"), row.payload)
	}
}

// TestExecute_LeavesNoSessionWhenTheReplayFails:回放中途出错时不写身份行 —— 写了的
// 话下一次同号重来会被判成"已导过"并直接指回去,那条会话就永远停在半截转录上。
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
	assert.Empty(t, rig.sessions.started, "半截转录不许留下一条看着导完了的会话")
}

// TestExecute_RejectsAnEmptySessionID:会话 id 由调用方铸,零值没法定位任何一行 ——
// 拿它当会话身份会让所有零号导入互相覆盖。
func TestExecute_RejectsAnEmptySessionID(t *testing.T) {
	rig := newExecuteRig(t, &fakeTranscript{meta: pkgimport.Meta{ProviderSessionID: "prov-1"}})

	_, err := rig.handlers.Execute(context.Background(), wire.ExecuteParams{
		Backend: string(agent_backend_entity.TypeClaudeCode), Locator: "loc-1",
	})

	require.Error(t, err)
	assert.Empty(t, rig.journal.rows)
}

// ── fakes ───────────────────────────────────────────────────────────────────

type executeRig struct {
	handlers *transcriptimport.Handlers
	sessions *fakeSessionStore
	journal  *fakeJournal
	purged   []string
}

func newExecuteRig(t *testing.T, transcript *fakeTranscript) *executeRig {
	t.Helper()
	rig := &executeRig{sessions: newFakeSessionStore(), journal: &fakeJournal{}}
	src := &fakeSource{backend: agent_backend_entity.TypeClaudeCode, transcript: transcript}
	rig.handlers = transcriptimport.NewHandlers(transcriptimport.Options{
		Sources:  func() []pkgimport.Source { return []pkgimport.Source{src} },
		Sessions: rig.sessions,
		Journal:  rig.journal,
		JournalPurge: purgeFunc(func(_ context.Context, _, peerSessionID string) (int64, error) {
			rig.purged = append(rig.purged, peerSessionID)
			return rig.journal.deleteAll(peerSessionID), nil
		}),
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

func (f *fakeSessionStore) List(_ context.Context, _, _ string) ([]handlers.SessionRecord, error) {
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

type journalEntry struct {
	peerSessionID string
	payload       []byte
}

type fakeJournal struct {
	rows []journalEntry
	seq  int64
}

func (f *fakeJournal) Append(_ context.Context, _, peerSessionID string, payload []byte) (int64, error) {
	f.seq++
	f.rows = append(f.rows, journalEntry{peerSessionID: peerSessionID, payload: payload})
	return f.seq, nil
}

func (f *fakeJournal) deleteAll(peerSessionID string) int64 {
	kept := make([]journalEntry, 0, len(f.rows))
	var removed int64
	for _, row := range f.rows {
		if row.peerSessionID == peerSessionID {
			removed++
			continue
		}
		kept = append(kept, row)
	}
	f.rows = kept
	return removed
}

// decode 把日志行还原成 (method, params) —— 与 SESSION_PULL 走的是同一条解码路径。
func (f *fakeJournal) decode(t *testing.T) (methods []string, params []any) {
	t.Helper()
	for _, row := range f.rows {
		notification, err := protowire.DecodeNotification(row.payload)
		require.NoError(t, err)
		method, value, err := protowire.ProtoNotificationToWire(notification)
		require.NoError(t, err)
		methods = append(methods, method)
		params = append(params, value)
	}
	return methods, params
}

func (f *fakeJournal) methods(t *testing.T) []string {
	t.Helper()
	methods, _ := f.decode(t)
	return methods
}

func (f *fakeJournal) events(t *testing.T) []agentruntime.Event {
	t.Helper()
	_, params := f.decode(t)
	out := make([]agentruntime.Event, 0, len(params))
	for _, value := range params {
		if frame, ok := value.(*runtimewire.EventFrame); ok {
			out = append(out, frame.Event)
		}
	}
	return out
}

func (f *fakeJournal) dones(t *testing.T) []runtimewire.RunResultDoneFrame {
	t.Helper()
	_, params := f.decode(t)
	out := make([]runtimewire.RunResultDoneFrame, 0, len(params))
	for _, value := range params {
		if frame, ok := value.(*runtimewire.RunResultDoneFrame); ok {
			out = append(out, *frame)
		}
	}
	return out
}

// convID 把一个短会话号折成一条**格式合法**的 conversation_id,只在测试里用:
// 线上身份是 uuid,而这些用例真正要断言的是"同一个值原样往返"与"两条不同的对话
// 互不并轨",一个可读、可复现的映射比随机 uuid 更好读。
func convID(n int64) string {
	return fmt.Sprintf("00000000-0000-7000-8000-%012d", n)
}
