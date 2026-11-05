package chat_svc

import (
	"context"
	"errors"
	"math"
	"sort"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap/zapcore"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	transcriptblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo/mock_transcript_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/view"
)

// turn_end_persist_test.go 钉住收尾那一发(轮末 / 中止 / 插话分段 / 出错)的写法:块行只写
// 相对已落库版本变了的那些,不再走 MessageRepo.Update(它无条件 DELETE … WHERE message_id = ?
// 再整条重插)。仓储是 gomock 严格 mock:没给 Update 配期望,谁调它用例当场判红。
//
// blockStore 把每一次 UpsertBlocks 按仓储语义(先截断、再按 idx upsert)施加到内存里的块行上,
// 最后与「整条重写」那一版(SplitBlocksJSON(SetBlocks(期望的整份正文)))逐行比较 —— 落库
// 内容必须与改动前一字不差。

type upsertCall struct {
	ctxErr       error
	idx          []int
	truncateFrom int
}

type blockStore struct {
	rows  map[int64]map[int]*transcript_entity.MessageBlock
	calls []upsertCall
	fail  error
}

func newBlockStore() *blockStore {
	return &blockStore{rows: map[int64]map[int]*transcript_entity.MessageBlock{}}
}

func (s *blockStore) upsert(ctx context.Context, m *chat_entity.Message, rows []*transcript_entity.MessageBlock, truncateFrom int) error {
	call := upsertCall{ctxErr: ctx.Err(), truncateFrom: truncateFrom}
	for _, r := range rows {
		call.idx = append(call.idx, r.Idx)
	}
	s.calls = append(s.calls, call)
	if s.fail != nil {
		return s.fail
	}
	held := s.rows[m.ID]
	if held == nil {
		held = map[int]*transcript_entity.MessageBlock{}
		s.rows[m.ID] = held
	}
	if truncateFrom >= 0 {
		for idx := range held {
			if idx >= truncateFrom {
				delete(held, idx)
			}
		}
	}
	for _, r := range rows {
		cp := *r
		held[r.Idx] = &cp
	}
	return nil
}

// persisted 按 idx 升序交回某条消息此刻的块行。
func (s *blockStore) persisted(messageID int64) []*transcript_entity.MessageBlock {
	held := s.rows[messageID]
	idx := make([]int, 0, len(held))
	for i := range held {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	out := make([]*transcript_entity.MessageBlock, 0, len(idx))
	for _, i := range idx {
		out = append(out, held[i])
	}
	return out
}

func (s *blockStore) last() upsertCall {
	return s.calls[len(s.calls)-1]
}

// fullRewriteRows 是改动前那一版收尾写出的块行:SetBlocks(want) 再整条拆行。
func fullRewriteRows(t *testing.T, messageID int64, want []cagoblocks.ContentBlock) (string, []*transcript_entity.MessageBlock) {
	t.Helper()
	ref := &chat_entity.Message{}
	require.NoError(t, ref.SetBlocks(want))
	rows, err := transcript_entity.SplitBlocksJSON(messageID, ref.BlocksJSON)
	require.NoError(t, err)
	return ref.BlocksJSON, rows
}

type turnEndRig struct {
	ctx     context.Context
	tr      *turnRun
	store   *blockStore
	emitter *captureEmitter
	msgRepo *mock_transcript_repo.MockMessageRepo
	dbMock  sqlmock.Sqlmock
}

func newTurnEndRig(t *testing.T) *turnEndRig {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	ctx, _, dbMock := testutils.Database(t)
	msgRepo := mock_transcript_repo.NewMockMessageRepo(ctrl)
	sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
	prevMsg, prevSess := transcript_repo.Message(), chat_repo.Session()
	transcript_repo.RegisterMessage(msgRepo)
	chat_repo.RegisterSession(sessRepo)
	t.Cleanup(func() {
		transcript_repo.RegisterMessage(prevMsg)
		chat_repo.RegisterSession(prevSess)
	})
	sessRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	store := newBlockStore()
	msgRepo.EXPECT().UpsertBlocks(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(store.upsert).AnyTimes()

	emitter := &captureEmitter{}
	svc := NewChat(emitter).(*chatSvc)
	assistant := &chat_entity.Message{ID: 9001, SessionID: 100, Role: "assistant", BlocksJSON: "[]"}
	sess := &chat_entity.Session{ID: 100, AgentStatus: "running"}
	tr := &turnRun{
		svc:          svc,
		sess:         sess,
		be:           &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"},
		assistantMsg: assistant,
		acc:          turn.New(),
		segmentStart: time.Now(),
		turnCtx:      svc.newTurnContext(assistant, sess, "chat:100:9001", "claudecode"),
		dispEmit:     &dispatcherEmitter{svc: svc},
		stream:       "chat:100:9001",
	}
	return &turnEndRig{ctx: ctx, tr: tr, store: store, emitter: emitter, msgRepo: msgRepo, dbMock: dbMock}
}

// Given 一轮里已经 checkpoint 过 [tool_use, 运行中的 subagent 卡],之后又来了一张没答的
// AskUserQuestion 与一段正文,轮被用户中止(turnCtx 已 cancel),会话回退了供应商
// When  finalize 收尾
// Then  只写变了的块(subagent 卡翻 canceled、ask 标 expired、正文、两条 notice),没变的
//
//	tool_use 不重写;不经 Update;写库用的 ctx 没被 cancel;落库结果与整条重写逐行相同。
func TestTurnRunFinalize_GivenAbortedTurnWithCheckpoint_ThenWritesOnlyChangedBlocks(t *testing.T) {
	rig := newTurnEndRig(t)
	tr := rig.tr
	tr.acc.AddToolUse(&cagoblocks.ToolUseBlock{ID: "tu-1", Name: "Task"}, "tool_use:tu-1")
	tr.acc.AddBlock(&transcriptblocks.SubagentStateBlock{ParentToolCallID: "tu-1", TaskID: "task-1", Status: "running"},
		"subagent_state:tu-1")
	tr.svc.checkpointAssistantNew(rig.ctx, tr.assistantMsg, tr.acc)
	require.Len(t, rig.store.calls, 1)
	assert.Equal(t, []int{0, 1}, rig.store.last().idx)

	tr.acc.AddBlock(&transcriptblocks.UserAskBlock{RequestID: "ask-1"}, "user_ask:ask-1")
	tr.acc.AddText("partial answer")
	fallback := cagoblocks.NoticeBlock{Level: "info", Text: view.EncodeProviderFallback("gone", "")}
	tr.extras.providerFallbackNotice = &fallback
	tr.result = &agentruntime.RunResult{ProviderFallbackKey: "remote-gone"}
	tr.svc.aborted.Store(tr.sess.ID, struct{}{})

	canceled, cancel := context.WithCancel(rig.ctx)
	cancel()
	tr.finalize(canceled)

	require.Len(t, rig.store.calls, 2, "收尾只写一次")
	end := rig.store.last()
	assert.NoError(t, end.ctxErr, "中止路径的收尾写库不能用已 cancel 的 ctx")
	assert.Equal(t, []int{1, 2, 3, 4, 5}, end.idx, "已落库且没变的 tool_use 不重写")
	assert.Equal(t, -1, end.truncateFrom)

	wantJSON, wantRows := fullRewriteRows(t, tr.assistantMsg.ID, []cagoblocks.ContentBlock{
		&cagoblocks.ToolUseBlock{ID: "tu-1", Name: "Task"},
		&transcriptblocks.SubagentStateBlock{ParentToolCallID: "tu-1", TaskID: "task-1", Status: "canceled"},
		&transcriptblocks.UserAskBlock{RequestID: "ask-1", Expired: true},
		&cagoblocks.TextBlock{Text: "partial answer"},
		fallback,
		cagoblocks.NoticeBlock{Level: "info", Text: view.EncodeProviderFallback("remote-gone", "")},
	})
	assert.Equal(t, wantRows, rig.store.persisted(tr.assistantMsg.ID), "落库结果与整条重写逐行相同")
	assert.Equal(t, wantJSON, tr.assistantMsg.BlocksJSON, "终态事件带的整份正文不变")
}

// Given 一轮从没 checkpoint 过(这条 assistant 一块都没落过)
// When  finalize 收尾
// Then  逐块写全,不截断,不经 Update。
func TestTurnRunFinalize_GivenNeverPersistedMessage_ThenInsertsEveryBlock(t *testing.T) {
	rig := newTurnEndRig(t)
	tr := rig.tr
	tr.acc.AddToolUse(&cagoblocks.ToolUseBlock{ID: "tu-1", Name: "Bash"}, "tool_use:tu-1")
	tr.acc.AddText("done")

	tr.finalize(rig.ctx)

	require.Len(t, rig.store.calls, 1)
	assert.Equal(t, []int{0, 1}, rig.store.last().idx)
	assert.Equal(t, -1, rig.store.last().truncateFrom)
	_, wantRows := fullRewriteRows(t, tr.assistantMsg.ID, []cagoblocks.ContentBlock{
		&cagoblocks.ToolUseBlock{ID: "tu-1", Name: "Bash"},
		&cagoblocks.TextBlock{Text: "done"},
	})
	assert.Equal(t, wantRows, rig.store.persisted(tr.assistantMsg.ID))
}

// Given 收尾写库失败
// When  finalize 收尾
// Then  记一条 Error(chat_svc.runTurn: 前缀,带 sessionId / assistantMsgId / error),界面照旧
//
//	收到带整份正文的 StreamDone。
func TestTurnRunFinalize_GivenPersistFails_ThenLogsErrorAndStillEmitsDone(t *testing.T) {
	logs := observeChatLogs(t)
	rig := newTurnEndRig(t)
	rig.store.fail = errors.New("database is locked")
	tr := rig.tr
	tr.acc.AddText("answer")

	tr.finalize(rig.ctx)

	entries := logs.FilterMessage("chat_svc.runTurn: persist assistant message failed").All()
	require.Len(t, entries, 1)
	assert.Equal(t, zapcore.ErrorLevel, entries[0].Level)
	fields := entries[0].ContextMap()
	assert.Equal(t, int64(100), fields["sessionId"])
	assert.Equal(t, int64(9001), fields["assistantMsgId"])
	assert.Equal(t, "database is locked", fields["error"])

	var done *ChatStreamEvent
	for i := range rig.emitter.events {
		if rig.emitter.events[i].Kind == StreamDone {
			done = &rig.emitter.events[i]
		}
	}
	require.NotNil(t, done)
	require.NotNil(t, done.Message)
	require.Len(t, done.Message.Blocks, 1)
}

// Given 收尾时某一块编码失败(工具入参里有 JSON 编不出的值),这一轮带着错误结束
// When  finalize 收尾
// Then  块行一行不交(库里留着上一次成功 checkpoint 的块),但元数据行照样落库 —— 错误文本 /
//
//	耗时不随编码失败一起丢掉(改动前 SetBlocks 失败也照样 Update 元数据)。
func TestTurnRunFinalize_GivenFinalBlockFailsToEncode_ThenStillPersistsMetadata(t *testing.T) {
	logs := observeChatLogs(t)
	rig := newTurnEndRig(t)
	tr := rig.tr
	tr.acc.AddToolUse(&cagoblocks.ToolUseBlock{ID: "tu-1", Name: "Bash", Input: map[string]any{"n": math.NaN()}},
		"tool_use:tu-1")
	tr.streamStopErr = errors.New("cli exited")

	tr.finalize(rig.ctx)

	require.Len(t, rig.store.calls, 1, "编码失败也要落一次元数据行")
	assert.Empty(t, rig.store.last().idx, "编码失败时不交任何块行")
	assert.Equal(t, -1, rig.store.last().truncateFrom)
	assert.Equal(t, "cli exited", tr.assistantMsg.ErrorText)
	require.Len(t, logs.FilterMessage("chat_svc.runTurn: persist assistant message failed").All(), 1,
		"编码失败按收尾落库失败记 Error")
}

// Given 一段已 checkpoint 过 tool_use 的 assistant,插话到来要分段
// When  persistConsumedSteers 在事务里收口它
// Then  只写变了的块(新 tool_result 与正文),不经 Update;事务失败时写集合不提交,收尾那一发
//
//	仍把这些块写出来(重试不丢块)。
func TestPersistConsumedSteers_WritesOnlyChangedBlocksAndRetriesAfterAFailedTransaction(t *testing.T) {
	newSegmentRig := func(t *testing.T) (*turnEndRig, *chat_entity.Message) {
		rig := newTurnEndRig(t)
		tr := rig.tr
		tr.acc.AddToolUse(&cagoblocks.ToolUseBlock{ID: "tu-1", Name: "Bash"}, "tool_use:tu-1")
		tr.svc.checkpointAssistantNew(rig.ctx, tr.assistantMsg, tr.acc)
		tr.acc.AddToolResult(&cagoblocks.ToolResultBlock{ToolUseID: "tu-1"})
		tr.acc.AddText("between")
		return rig, tr.assistantMsg
	}
	steers := []agentruntime.ConsumedSteer{{Text: "also this"}}

	t.Run("success", func(t *testing.T) {
		rig, current := newSegmentRig(t)
		dbMock := rig.dbMock
		dbMock.ExpectBegin()
		dbMock.ExpectCommit()
		rig.msgRepo.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(5, nil)
		rig.msgRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil).Times(2)

		next, payload, err := rig.tr.svc.persistConsumedSteers(rig.ctx, rig.tr.sess, rig.tr.be, current,
			rig.tr.acc, time.Now(), "", steers, rig.tr.turnCtx)
		require.NoError(t, err)
		require.NotNil(t, next)
		require.NotNil(t, payload)
		assert.Equal(t, []int{1, 2}, rig.store.last().idx, "已落库的 tool_use 不重写")
		_, wantRows := fullRewriteRows(t, current.ID, []cagoblocks.ContentBlock{
			&cagoblocks.ToolUseBlock{ID: "tu-1", Name: "Bash"},
			&cagoblocks.ToolResultBlock{ToolUseID: "tu-1"},
			&cagoblocks.TextBlock{Text: "between"},
		})
		assert.Equal(t, wantRows, rig.store.persisted(current.ID))
	})

	t.Run("transaction fails", func(t *testing.T) {
		rig, current := newSegmentRig(t)
		dbMock := rig.dbMock
		dbMock.ExpectBegin()
		dbMock.ExpectRollback()
		rig.msgRepo.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(0, errors.New("busy"))

		_, _, err := rig.tr.svc.persistConsumedSteers(rig.ctx, rig.tr.sess, rig.tr.be, current,
			rig.tr.acc, time.Now(), "", steers, rig.tr.turnCtx)
		require.Error(t, err)

		// 事务回滚了:块行实际没落。fake 的 upsert 已施加,把它退回到回滚后的样子。
		rig.store.rows[current.ID] = map[int]*transcript_entity.MessageBlock{0: rig.store.persisted(current.ID)[0]}
		rig.tr.finalize(rig.ctx)
		assert.Equal(t, []int{1, 2}, rig.store.last().idx, "回滚的分段不算落库,收尾重写这些块")
	})
}

// failTurn / abortTurnBeforeStream 收的是还没开始流式的 assistant:正文仍是建行时落下的那份,
// 只有元数据(error_text)要写。不经 Update,不交任何块行。
func TestFailAndAbortBeforeStream_WriteMetadataOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(s *chatSvc, ctx context.Context, sess *chat_entity.Session, msg *chat_entity.Message)
	}{
		{"failTurn", func(s *chatSvc, ctx context.Context, sess *chat_entity.Session, msg *chat_entity.Message) {
			s.failTurn(ctx, sess, msg, "chat:100:9001", errors.New("boom"))
		}},
		{"abortTurnBeforeStream", func(s *chatSvc, ctx context.Context, sess *chat_entity.Session, msg *chat_entity.Message) {
			s.abortTurnBeforeStream(ctx, sess, msg, "chat:100:9001")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newTurnEndRig(t)
			canceled, cancel := context.WithCancel(rig.ctx)
			cancel()
			tc.run(rig.tr.svc, canceled, rig.tr.sess, rig.tr.assistantMsg)
			require.Len(t, rig.store.calls, 1)
			assert.Empty(t, rig.store.last().idx)
			assert.Equal(t, -1, rig.store.last().truncateFrom)
			assert.NoError(t, rig.store.last().ctxErr)
		})
	}
}

// Given 还没开始流式就结束的一轮(failTurn / abortTurnBeforeStream),收尾写库失败
// When  收尾
// Then  记一条 Error,带 sessionId / assistantMsgId / error(spec 2026-11-05 决策 7:轮末 / 中止 /
//
//	出错路径的落库失败一律带这两个 id)。
func TestFailAndAbortBeforeStream_GivenPersistFails_ThenLogsErrorWithSessionAndAssistantIDs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
		run     func(s *chatSvc, ctx context.Context, sess *chat_entity.Session, msg *chat_entity.Message)
	}{
		{"failTurn", "chat_svc.failTurn: persist error text failed",
			func(s *chatSvc, ctx context.Context, sess *chat_entity.Session, msg *chat_entity.Message) {
				s.failTurn(ctx, sess, msg, "chat:100:9001", errors.New("boom"))
			}},
		{"abortTurnBeforeStream", "chat_svc.abortTurnBeforeStream: persist message failed",
			func(s *chatSvc, ctx context.Context, sess *chat_entity.Session, msg *chat_entity.Message) {
				s.abortTurnBeforeStream(ctx, sess, msg, "chat:100:9001")
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := observeChatLogs(t)
			rig := newTurnEndRig(t)
			rig.store.fail = errors.New("database is locked")

			tc.run(rig.tr.svc, rig.ctx, rig.tr.sess, rig.tr.assistantMsg)

			entries := logs.FilterMessage(tc.message).All()
			require.Len(t, entries, 1)
			assert.Equal(t, zapcore.ErrorLevel, entries[0].Level)
			fields := entries[0].ContextMap()
			assert.Equal(t, int64(100), fields["sessionId"])
			assert.Equal(t, int64(9001), fields["assistantMsgId"])
			assert.Equal(t, "database is locked", fields["error"])
		})
	}
}
