package chat_svc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/pkg/shellloc"
	chatblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/mock_chat_svc"
	"github.com/agentre-hub/agentre/internal/service/workspace_fs_svc"
)

// 本文件钉住 spec 2026-11-24-chat-right-toolwindows「识别信号」:工具事件一到就更新会话
// 的 AI 当前位置并认领、推送工作根变化;子代理只参与认领;重开会话取最后一个带位置记录
// 的工具块;ExitWorktree 选择删除时撤销那个根。会话固定是 7(stubMessages 的会话)。

const (
	locRepo = "/repo"
	locWT   = "/repo/.claude/worktrees/x"
	locWT2  = "/repo/.worktrees/y"
)

var (
	primaryRoot = workspace_fs_svc.WorkRootView{Path: locRepo, Name: "repo", IsPrimary: true}
	wtRoot      = workspace_fs_svc.WorkRootView{Path: locWT, Name: "x", IsWorktree: true}
	wt2Root     = workspace_fs_svc.WorkRootView{Path: locWT2, Name: "y", IsWorktree: true}
)

// workRootsRecorder 记下本会话工作根变化流上的每一发。
type workRootsRecorder struct{ events []WorkRootsEvent }

func (r *workRootsRecorder) Emit(_ context.Context, name string, payload any) {
	if name != WorkRootsStreamName(7) {
		return
	}
	if ev, ok := payload.(WorkRootsEvent); ok {
		r.events = append(r.events, ev)
	}
}

// newLocationSvc 造一个只带认领端口桩与录制发射器的 chatSvc;认领同步执行,断言不必等
// 后台 goroutine。msgs 是会话里已落库的消息(重开时恢复位置的来源)。
func newLocationSvc(t *testing.T, msgs ...*chat_entity.Message) (*chatSvc, *mock_chat_svc.MockWorkRootsPort, *workRootsRecorder) {
	t.Helper()
	stubMessages(t, msgs, nil)
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	port := mock_chat_svc.NewMockWorkRootsPort(ctrl)
	rec := &workRootsRecorder{}
	svc := &chatSvc{emitter: rec, workRoots: port, workRootsRunner: func(f func()) { f() }}
	return svc, port, rec
}

// cwdMsg 造一条已落库的消息:一个外层工具调用,旁边记着它的所在目录。
func cwdMsg(t *testing.T, id int64, callID, cwd, resultCwd string) *chat_entity.Message {
	return msgWithBlocks(t, id,
		blocks.ToolUseBlock{ID: callID, Name: "Bash", Input: map[string]any{"command": "ls"}},
		&chatblocks.ToolCwdBlock{ToolCallID: callID, Cwd: cwd, ResultCwd: resultCwd},
	)
}

func location(t *testing.T, svc *chatSvc) string {
	t.Helper()
	dir, err := svc.AILocation(context.Background(), 7)
	require.NoError(t, err)
	return dir
}

// Given AI 在主仓库, When 一条工具调用报告它在同一仓库的另一个 worktree 里(轮次进行中),
// Then 位置当场移过去、那个目录作为候选交给认领,并推一发带「AI 在这里」的新集合。
func TestObserveToolEvent_MovesLocationMidTurnAndClaims(t *testing.T) {
	svc, port, rec := newLocationSvc(t)
	ctx := context.Background()
	port.EXPECT().Claim(gomock.Any(), int64(7), []string{locWT}).Return([]workspace_fs_svc.WorkRootView{wtRoot}, nil)
	after := []workspace_fs_svc.WorkRootView{primaryRoot, withAIHere(wtRoot)}
	port.EXPECT().WorkRoots(gomock.Any(), int64(7)).Return(after, nil)

	svc.observeToolEvent(ctx, 7, agentruntime.ToolCall{ID: "c1", Name: "Bash", Cwd: locWT})

	assert.Equal(t, locWT, location(t, svc))
	require.Len(t, rec.events, 1)
	assert.Equal(t, WorkRootsEvent{SessionID: 7, Roots: after}, rec.events[0])
}

// 推送按「集合或 AI 的根变了」一次,不按事件一次:同一目录的结果帧、同一根之内换子目录
// 都不推;回到主仓库(根变了)再推一发。
func TestObserveToolEvent_PushesOncePerChangeNotPerEvent(t *testing.T) {
	svc, port, rec := newLocationSvc(t)
	ctx := context.Background()
	gomock.InOrder(
		port.EXPECT().Claim(gomock.Any(), int64(7), []string{locWT}).Return([]workspace_fs_svc.WorkRootView{wtRoot}, nil),
		port.EXPECT().WorkRoots(gomock.Any(), int64(7)).Return([]workspace_fs_svc.WorkRootView{primaryRoot, withAIHere(wtRoot)}, nil),
		port.EXPECT().Claim(gomock.Any(), int64(7), []string{locWT + "/pkg"}).Return(nil, nil),
		port.EXPECT().WorkRoots(gomock.Any(), int64(7)).Return([]workspace_fs_svc.WorkRootView{primaryRoot, withAIHere(wtRoot)}, nil),
		port.EXPECT().Claim(gomock.Any(), int64(7), []string{locRepo}).Return(nil, nil),
		port.EXPECT().WorkRoots(gomock.Any(), int64(7)).Return([]workspace_fs_svc.WorkRootView{withAIHere(primaryRoot), wtRoot}, nil),
	)

	svc.observeToolEvent(ctx, 7, agentruntime.ToolCall{ID: "c1", Cwd: locWT})
	svc.observeToolEvent(ctx, 7, agentruntime.ToolResult{ToolCallID: "c1", Cwd: locWT})
	svc.observeToolEvent(ctx, 7, agentruntime.ToolCall{ID: "c2", Cwd: locWT + "/pkg"})
	svc.observeToolEvent(ctx, 7, agentruntime.ToolCall{ID: "c3", Cwd: locRepo})

	require.Len(t, rec.events, 2, "进 worktree 一发、回主仓库一发;同根内换目录与重复目录都不推")
	assert.True(t, rec.events[1].Roots[0].AIHere)
	assert.Equal(t, locRepo, location(t, svc))
}

// 决策 16:子代理的位置变化只参与认领,不移动主会话的 AI 当前位置;同一目录不重复认领。
func TestObserveToolEvent_SubagentClaimsButDoesNotMoveLocation(t *testing.T) {
	svc, port, rec := newLocationSvc(t, cwdMsg(t, 1, "c0", locRepo, ""))
	ctx := context.Background()
	port.EXPECT().Claim(gomock.Any(), int64(7), []string{locWT2}).Return([]workspace_fs_svc.WorkRootView{wt2Root}, nil).Times(1)
	port.EXPECT().WorkRoots(gomock.Any(), int64(7)).Return([]workspace_fs_svc.WorkRootView{withAIHere(primaryRoot), wt2Root}, nil).Times(1)

	svc.observeToolEvent(ctx, 7, agentruntime.ToolCall{ID: "n1", ParentToolCallID: "agent-1", Cwd: locWT2})
	svc.observeToolEvent(ctx, 7, agentruntime.ToolResult{ToolCallID: "n1", SubagentRunID: "run-1", Cwd: locWT2})

	assert.Equal(t, locRepo, location(t, svc), "子代理不拖动主位置")
	require.Len(t, rec.events, 1, "认领出新根 → 集合变了,推一发")
}

// 目录没变的工具事件什么都不做:不认领、不取集合、不推送(严格 mock 没有任何期望)。
func TestObserveToolEvent_UnchangedCwdIsANoOp(t *testing.T) {
	svc, _, rec := newLocationSvc(t, cwdMsg(t, 1, "c0", locWT, ""))
	ctx := context.Background()

	svc.observeToolEvent(ctx, 7, agentruntime.ToolCall{ID: "c1", Cwd: locWT})
	svc.observeToolEvent(ctx, 7, agentruntime.ToolResult{ToolCallID: "c1", Cwd: locWT})
	svc.observeToolEvent(ctx, 7, agentruntime.ToolCall{ID: "c2"}) // 没报目录

	assert.Empty(t, rec.events)
	assert.Equal(t, locWT, location(t, svc))
}

// pi / builtin / ACP 从命令文本识别出 `git worktree add <路径>` 时把路径放进入参的
// shellloc.WorktreeAddKey:它是认领候选(位置不因此移动)。
func TestObserveToolEvent_WorktreeAddPathIsClaimed(t *testing.T) {
	svc, port, rec := newLocationSvc(t, cwdMsg(t, 1, "c0", locRepo, ""))
	ctx := context.Background()
	port.EXPECT().Claim(gomock.Any(), int64(7), []string{locWT2}).Return([]workspace_fs_svc.WorkRootView{wt2Root}, nil)
	port.EXPECT().WorkRoots(gomock.Any(), int64(7)).Return([]workspace_fs_svc.WorkRootView{withAIHere(primaryRoot), wt2Root}, nil)

	input, err := json.Marshal(map[string]any{"command": "git worktree add .worktrees/y", shellloc.WorktreeAddKey: locWT2})
	require.NoError(t, err)
	svc.observeToolEvent(ctx, 7, agentruntime.ToolCall{ID: "c1", Name: "bash", Input: input, Cwd: locRepo})

	assert.Equal(t, locRepo, location(t, svc))
	require.Len(t, rec.events, 1)
}

// 问题 7:写入目标是认领候选,相对路径按该调用的所在目录补全(不是会话工作目录、不跳过)。
func TestObserveToolEvent_RelativeWriteAnchoredAtCallCwd(t *testing.T) {
	svc, port, _ := newLocationSvc(t, cwdMsg(t, 1, "c0", locRepo+"/sub", ""))
	ctx := context.Background()
	port.EXPECT().Claim(gomock.Any(), int64(7), []string{locWT2 + "/pkg"}).Return(nil, nil)

	svc.observeToolEvent(ctx, 7, agentruntime.ToolCall{
		ID: "w1", Name: "write", Cwd: locRepo + "/sub",
		Canonical: canonical.FileWrite{Path: "../.worktrees/y/pkg/a.go"},
	})
}

// 落库之后的写入同样带上调用所在目录:WrittenPath.Dir 取那次调用旁记的目录(外层取
// tool_cwd 块,子代理取嵌套块自己的 Cwd),认领方据此补全相对路径。
func TestSessionWrittenPaths_DirFromCallCwd(t *testing.T) {
	stubMessages(t, []*chat_entity.Message{
		msgWithBlocks(t, 1,
			blocks.ToolUseBlock{ID: "w1", Name: "Write", Input: map[string]any{"file_path": "../wt/x.go", "content": "x"}},
			&chatblocks.ToolCwdBlock{ToolCallID: "w1", Cwd: "/repo/main"},
			blocks.ToolUseBlock{ID: "w2", Name: "Write", Input: map[string]any{"file_path": "/abs/y.go", "content": "y"}},
		),
		msgWithBlocks(t, 2, &chatblocks.NestedToolUseBlock{
			ID: "n1", Name: "Write", ParentToolCallID: "agent", Cwd: "/repo/other",
			Input: map[string]any{"file_path": "z.go", "content": "z"},
		}),
	}, nil)

	paths, err := SessionWrittenPaths(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, []workspace_fs_svc.WrittenPath{
		{Path: "../wt/x.go", Dir: "/repo/main"},
		{Path: "/abs/y.go"},
		{Path: "z.go", Dir: "/repo/other"},
	}, paths)
}

// 重开 / 重启后,AI 当前位置取最后一个带位置记录的工具块(结果目录优先于调用目录);
// 一个都没有时为空串,即会话工作目录。
func TestAILocation_RestoredFromLastToolCwdBlock(t *testing.T) {
	t.Run("最后一块的结果目录", func(t *testing.T) {
		svc, _, _ := newLocationSvc(t,
			cwdMsg(t, 1, "c1", "/a", ""),
			cwdMsg(t, 2, "c2", "/b", "/c"),
			msgWithBlocks(t, 3, blocks.TextBlock{Text: "done"}),
		)
		assert.Equal(t, "/c", location(t, svc))
	})
	t.Run("只有调用目录", func(t *testing.T) {
		svc, _, _ := newLocationSvc(t, cwdMsg(t, 1, "c1", "/a", ""), cwdMsg(t, 2, "c2", "/b", ""))
		assert.Equal(t, "/b", location(t, svc))
	})
	t.Run("没有任何位置记录 → 空串(会话工作目录)", func(t *testing.T) {
		svc, _, _ := newLocationSvc(t, msgWithBlocks(t, 1, blocks.TextBlock{Text: "hi"}))
		assert.Equal(t, "", location(t, svc))
	})
	t.Run("仓储报错 → 原样冒泡", func(t *testing.T) {
		stubMessages(t, nil, errors.New("db down"))
		svc := &chatSvc{emitter: NoopEmitter{}}
		_, err := svc.AILocation(context.Background(), 7)
		require.Error(t, err)
	})
}

// Claude Code ExitWorktree 选择删除:位置回到恢复目录,同时撤销那个根并推一发。
func TestObserveAILocation_RevokeRootRevokesAndPushes(t *testing.T) {
	svc, port, rec := newLocationSvc(t, cwdMsg(t, 1, "c0", locWT, ""))
	ctx := context.Background()
	port.EXPECT().Revoke(gomock.Any(), int64(7), locWT).Return(true, nil)
	port.EXPECT().Claim(gomock.Any(), int64(7), []string{locRepo}).Return(nil, nil)
	after := []workspace_fs_svc.WorkRootView{withAIHere(primaryRoot)}
	port.EXPECT().WorkRoots(gomock.Any(), int64(7)).Return(after, nil)

	svc.ObserveAILocation(ctx, 7, AILocationSignal{Dir: locRepo, RevokeRoot: locWT})

	assert.Equal(t, locRepo, location(t, svc))
	require.Len(t, rec.events, 1)
	assert.Equal(t, after, rec.events[0].Roots)
}

// Given AI 在 worktree 里, When 一条工具结果(ExitWorktree 选择删除)带着回到的目录与
// Meta 里的撤销标记到达(本机或经 wire 从 agentred 来都一样), Then 撤销那个根、位置回到
// 恢复目录,并推一发。
func TestObserveToolEvent_ResultRevokeMarkerRevokesTheRoot(t *testing.T) {
	svc, port, rec := newLocationSvc(t, cwdMsg(t, 1, "c0", locWT, ""))
	ctx := context.Background()
	port.EXPECT().Revoke(gomock.Any(), int64(7), locWT).Return(true, nil)
	port.EXPECT().Claim(gomock.Any(), int64(7), []string{locRepo}).Return(nil, nil)
	after := []workspace_fs_svc.WorkRootView{withAIHere(primaryRoot)}
	port.EXPECT().WorkRoots(gomock.Any(), int64(7)).Return(after, nil)

	meta := json.RawMessage(`{"action":"remove","worktreePath":"` + locWT + `","` + agentruntime.RevokeRootMetaKey + `":"` + locWT + `"}`)
	svc.observeToolEvent(ctx, 7, agentruntime.ToolResult{ToolCallID: "x1", Meta: meta, Cwd: locRepo})

	assert.Equal(t, locRepo, location(t, svc))
	require.Len(t, rec.events, 1)
	assert.Equal(t, after, rec.events[0].Roots)
}

// Given 子代理删掉了它的 worktree, When 它的结果带撤销标记、不带目录, Then 照样撤销并推一发
// (撤销改的是认领集合),主位置不动。
func TestObserveToolEvent_SubagentResultRevokeMarkerRevokesWithoutMoving(t *testing.T) {
	svc, port, rec := newLocationSvc(t)
	ctx := context.Background()
	port.EXPECT().Revoke(gomock.Any(), int64(7), locWT2).Return(true, nil)
	after := []workspace_fs_svc.WorkRootView{withAIHere(primaryRoot)}
	port.EXPECT().WorkRoots(gomock.Any(), int64(7)).Return(after, nil)

	meta := json.RawMessage(`{"` + agentruntime.RevokeRootMetaKey + `":"` + locWT2 + `"}`)
	svc.observeToolEvent(ctx, 7, agentruntime.ToolResult{ToolCallID: "n1", ParentToolCallID: "agent-1", Meta: meta})

	assert.Equal(t, "", location(t, svc))
	require.Len(t, rec.events, 1)
}

// Given 结果 Meta 不带撤销标记(普通工具 / 旧 agentred), Then 不撤销任何根。
func TestObserveToolEvent_ResultWithoutRevokeMarkerRevokesNothing(t *testing.T) {
	svc, _, rec := newLocationSvc(t)
	svc.observeToolEvent(context.Background(), 7, agentruntime.ToolResult{ToolCallID: "r1", Meta: json.RawMessage(`{"action":"keep","worktreePath":"` + locWT + `"}`)})
	svc.observeToolEvent(context.Background(), 7, agentruntime.ToolResult{ToolCallID: "r2", Meta: json.RawMessage(`not json ` + agentruntime.RevokeRootMetaKey)})
	assert.Empty(t, rec.events)
}

// 入口同样承接 init 帧目录与 PostToolUse hook 的 cwd:子代理 hook 只认领。
func TestObserveAILocation_SubagentHookOnlyClaims(t *testing.T) {
	svc, port, rec := newLocationSvc(t)
	ctx := context.Background()
	port.EXPECT().Claim(gomock.Any(), int64(7), []string{locWT}).Return(nil, nil)

	svc.ObserveAILocation(ctx, 7, AILocationSignal{Dir: locWT, FromSubagent: true})

	assert.Equal(t, "", location(t, svc))
	assert.Empty(t, rec.events, "没认领出新根、主位置没动 → 不推")
}

// 接线:本机一轮的事件循环(applyLive)上,工具调用一到位置就移动并认领 —— 不等轮末。
func TestTurnRunApplyLive_ToolCallMovesLocationMidTurn(t *testing.T) {
	svc, port, rec := newLocationSvc(t)
	svc.dispatcher = newPackageDispatcher(svc)
	port.EXPECT().Claim(gomock.Any(), int64(7), []string{locWT}).Return([]workspace_fs_svc.WorkRootView{wtRoot}, nil)
	port.EXPECT().WorkRoots(gomock.Any(), int64(7)).Return([]workspace_fs_svc.WorkRootView{primaryRoot, withAIHere(wtRoot)}, nil)
	tr := &turnRun{
		svc:          svc,
		sess:         &chat_entity.Session{ID: 7},
		be:           &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"},
		assistantMsg: &chat_entity.Message{ID: 9001, SessionID: 7, Role: "assistant", BlocksJSON: "[]"},
		acc:          turn.New(),
		turnCtx:      &turn.TurnContext{Waits: turn.NewWaitTracker()},
		stream:       StreamName(7, 9001),
	}
	tr.dispEmit = &dispatcherEmitter{svc: svc}

	tr.applyLive(context.Background(), agentruntime.ToolCall{ID: "c1", Name: "Bash", Input: json.RawMessage(`{}`), Cwd: locWT}, false)

	assert.Equal(t, locWT, location(t, svc))
	require.Len(t, rec.events, 1)
}

func withAIHere(v workspace_fs_svc.WorkRootView) workspace_fs_svc.WorkRootView {
	v.AIHere = true
	return v
}
