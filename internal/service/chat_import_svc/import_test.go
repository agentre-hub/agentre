package chat_import_svc

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/agents/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
)

const (
	testCwd     = "/tmp/agentre-import-fixture"
	testSession = "prov-sess-1"
)

func ms(t time.Time) int64 { return t.UnixMilli() }

var (
	turn0Start = time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	turn0End   = time.Date(2026, 5, 1, 9, 0, 30, 0, time.UTC)
	turn1Start = time.Date(2026, 5, 1, 9, 1, 0, 0, time.UTC)
	turn1End   = time.Date(2026, 5, 1, 9, 2, 0, 0, time.UTC)
)

// twoTurnTranscript 是一份两轮的 claude 转录:第一轮纯文本(磁盘上思维是空的),
// 第二轮带一次工具调用与结果 + 用量。
func twoTurnTranscript() *fakeTranscript {
	return &fakeTranscript{
		meta: transcriptimport.Meta{
			Backend:           agent_backend_entity.TypeClaudeCode,
			ProviderSessionID: testSession,
			Title:             "修一个 bug",
			Cwd:               testCwd,
			Model:             "claude-opus-5",
			Turns:             2,
			ToolCalls:         1,
			StartedAt:         turn0Start,
			EndedAt:           turn1End,
			Origin:            transcriptimport.OriginTerminal,
			Gaps: []transcriptimport.Gap{
				{Kind: transcriptimport.GapThinkingUnavailable, Count: 2},
			},
		},
		turns: []transcriptimport.Turn{
			{
				Index:      0,
				UserText:   "第一轮问题\n第二行",
				Events:     []agentruntime.Event{agentruntime.TextDelta{Text: "第一轮回答"}, agentruntime.Done{}},
				Model:      "claude-opus-5",
				StartedAt:  turn0Start,
				EndedAt:    turn0End,
				ForkAnchor: "uuid-turn-0",
			},
			{
				Index:    1,
				UserText: "第二轮问题",
				Events: []agentruntime.Event{
					agentruntime.ToolCall{ID: "toolu_1", Name: "Read", Input: json.RawMessage(`{"file_path":"a.go"}`)},
					agentruntime.ToolResult{ToolCallID: "toolu_1", Content: "package main"},
					agentruntime.TextDelta{Text: "第二轮回答"},
					agentruntime.Done{},
				},
				Usage: &provider.Usage{
					PromptTokens: 100, CompletionTokens: 20, CachedTokens: 7,
					CacheCreationTokens: 3, ReasoningTokens: 5,
				},
				Model:      "claude-opus-5",
				StartedAt:  turn1Start,
				EndedAt:    turn1End,
				ForkAnchor: "uuid-turn-1",
			},
		},
	}
}

func claudeAgentAndBackend(m *repoMocks) {
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).
		Return(&agent_entity.Agent{ID: 7, Name: "阿呆", AgentBackendID: 42}, nil).AnyTimes()
	m.backend.EXPECT().Find(gomock.Any(), int64(42)).
		Return(&agent_backend_entity.AgentBackend{ID: 42, Type: string(agent_backend_entity.TypeClaudeCode)}, nil).AnyTimes()
}

func importReq() *ImportRequest {
	return &ImportRequest{
		Backend:   string(agent_backend_entity.TypeClaudeCode),
		Locator:   "projects/x/sess.jsonl",
		AgentID:   7,
		ProjectID: 3,
	}
}

// TestImport_WritesTurnPairs 是本切片的主判据:一轮磁盘转录 = 一条 user + 一条
// assistant,seq 按回放顺序递增,时间取磁盘值,fork 锚点落在 user 行(续跑 / 重新生成
// 从那里锚回后端记录),用量与模型逐轮落在 assistant 行,块由既有 turn.Accumulator
// 产出(不另开第二条 blocks_json 生成路径)。
func TestImport_WritesTurnPairs(t *testing.T) {
	m := withMocks(t, testCwd)
	claudeAgentAndBackend(m)
	src := installSource(t, &fakeSource{
		backend:    agent_backend_entity.TypeClaudeCode,
		transcript: twoTurnTranscript(),
	})
	ctx := context.Background()

	m.session.EXPECT().ListIDsByProviderSessions(gomock.Any(), []string{testSession}).
		Return(map[string]int64{}, nil)
	var created *chat_entity.Session
	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
			s.ID = 55
			created = s
			return nil
		})
	var wrote []*chat_entity.Message
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).Times(4).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			require.True(t, m.tx.inTx, "每一条消息都必须写在同一个事务里")
			msg.ID = int64(100 + len(wrote))
			wrote = append(wrote, msg)
			return nil
		})
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

	var progress [][2]int
	got, err := m.svc.Import(ctx, importReq(), func(done, total int) {
		progress = append(progress, [2]int{done, total})
	})

	require.NoError(t, err)
	assert.Equal(t, int64(55), got.SessionID)
	assert.False(t, got.AlreadyImported)
	assert.False(t, got.ReadOnly, "cwd 还在 → 可续跑")
	assert.Equal(t, 2, got.ImportedTurns)
	assert.True(t, m.tx.committed)

	// 会话:钉住 provider session + agent/项目,标题取磁盘,最后活动时间取最后一轮。
	require.NotNil(t, created)
	assert.Equal(t, testSession, created.ProviderSessionID)
	assert.Equal(t, int64(7), created.AgentID)
	assert.Equal(t, int64(3), created.ProjectID)
	assert.Equal(t, "修一个 bug", created.Title)
	assert.Equal(t, "idle", created.AgentStatus)
	assert.Equal(t, testCwd, created.Cwd, "工作目录取磁盘转录里记录的 cwd,不靠 agent 默认目录现算")
	assert.Equal(t, ms(turn1End), created.LastMessageAt)
	assert.Equal(t, ms(turn0Start), created.Createtime, "会话不该排到列表最前:建档时间取转录起点")

	// 四条消息:user/assistant 交替,seq 递增。
	require.Len(t, wrote, 4)
	assert.Equal(t, []string{"user", "assistant", "user", "assistant"},
		[]string{wrote[0].Role, wrote[1].Role, wrote[2].Role, wrote[3].Role})
	assert.Equal(t, []int{1, 2, 3, 4},
		[]int{wrote[0].Seq, wrote[1].Seq, wrote[2].Seq, wrote[3].Seq})

	// 时间取磁盘值:user 是这一轮开始,assistant 是这一轮结束。
	assert.Equal(t, ms(turn0Start), wrote[0].Createtime)
	assert.Equal(t, ms(turn0End), wrote[1].Createtime)
	assert.Equal(t, ms(turn1Start), wrote[2].Createtime)
	assert.Equal(t, ms(turn1End), wrote[3].Createtime)

	// fork 锚点落在 user 行(backendForkAnchor 读的就是 userMsg.ForkAnchor)。
	assert.Equal(t, "uuid-turn-0", wrote[0].ForkAnchor)
	assert.Equal(t, "uuid-turn-1", wrote[2].ForkAnchor)
	assert.Empty(t, wrote[1].ForkAnchor)

	// 逐轮用量与模型:磁盘上没有的轮留零,不猜。
	assert.Equal(t, 0, wrote[1].PromptTokens)
	assert.Equal(t, 100, wrote[3].PromptTokens)
	assert.Equal(t, 20, wrote[3].CompletionTokens)
	assert.Equal(t, 7, wrote[3].CachedTokens)
	assert.Equal(t, 3, wrote[3].CacheCreationTokens)
	assert.Equal(t, 5, wrote[3].ReasoningTokens)
	assert.Equal(t, "claude-opus-5", wrote[3].Model)

	// user 正文进 TextBlock。
	userBlocks, err := wrote[0].GetBlocks()
	require.NoError(t, err)
	require.Len(t, userBlocks, 1)
	assert.Equal(t, "text", userBlocks[0].Type())
	assert.Equal(t, "第一轮问题\n第二行", textOf(t, userBlocks[0]))

	// assistant 块来自 accumulator:工具卡是 canonical ToolUse/ToolResult,不是手搓 JSON。
	secondBlocks, err := wrote[3].GetBlocks()
	require.NoError(t, err)
	kinds := make([]string, 0, len(secondBlocks))
	for _, b := range secondBlocks {
		kinds = append(kinds, b.Type())
	}
	assert.Equal(t, []string{"tool_use", "tool_result", "text"}, kinds)

	// 缺口:思维块缺失处写一条就地说明,并且整份转录只说一次(决策 11)。
	notices := 0
	for _, msg := range wrote {
		bs, err := msg.GetBlocks()
		require.NoError(t, err)
		for _, b := range bs {
			if b.Type() == "notice" {
				notices++
			}
		}
	}
	assert.Equal(t, 1, notices, "思维缺口在转录内说一次")
	firstBlocks, err := wrote[1].GetBlocks()
	require.NoError(t, err)
	require.Len(t, firstBlocks, 2)
	assert.Equal(t, "notice", firstBlocks[1].Type(), "说明块落在缺思维的那一轮")

	assert.Equal(t, [][2]int{{1, 2}, {2, 2}}, progress, "进度按轮计")
	assert.True(t, src.transcript.closed, "转录用完必须 Close")
	assert.Equal(t, []transcriptimport.Locator{"projects/x/sess.jsonl"}, src.openedWith)
}

// TestImport_Idempotent 同一条 provider session 重复导入不产生第二条会话(硬约束 4)。
func TestImport_Idempotent(t *testing.T) {
	m := withMocks(t, testCwd)
	claudeAgentAndBackend(m)
	installSource(t, &fakeSource{
		backend:    agent_backend_entity.TypeClaudeCode,
		transcript: twoTurnTranscript(),
	})

	m.session.EXPECT().ListIDsByProviderSessions(gomock.Any(), []string{testSession}).
		Return(map[string]int64{testSession: 88}, nil)
	// 一条都不写:没有 Session().Create / Message().Create 的 EXPECT,真调到就是失败。

	got, err := m.svc.Import(context.Background(), importReq(), nil)

	require.NoError(t, err)
	assert.True(t, got.AlreadyImported)
	assert.Equal(t, int64(88), got.SessionID, "指回已在库里的那条,前端据此给「打开」")
	assert.Equal(t, 0, m.tx.ran, "判重命中根本不开事务")
}

// TestImport_MidWayFailureLeavesNothing 中途失败不留半截:全部写入在同一个事务里,
// 失败时这个事务没有提交。
func TestImport_MidWayFailureLeavesNothing(t *testing.T) {
	m := withMocks(t, testCwd)
	claudeAgentAndBackend(m)
	installSource(t, &fakeSource{
		backend:    agent_backend_entity.TypeClaudeCode,
		transcript: twoTurnTranscript(),
	})

	m.session.EXPECT().ListIDsByProviderSessions(gomock.Any(), []string{testSession}).
		Return(map[string]int64{}, nil)
	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
			s.ID = 55
			return nil
		})
	calls := 0
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).Times(3).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			calls++
			if calls == 3 {
				return errBoom
			}
			return nil
		})

	got, err := m.svc.Import(context.Background(), importReq(), nil)

	require.Error(t, err)
	assert.Nil(t, got)
	assert.True(t, m.tx.rolledBack, "第三条消息写失败 → 整个事务回滚,会话与前两条消息都不留")
	assert.False(t, m.tx.committed)
}

// TestImport_ReadOnlyWhenCwdMissing 工作目录已不存在时降级为只读导入:转录照写,
// 但不写 provider_session_id —— 那条会话没法在原目录接着跑,写进去只会让下一轮
// 拿着一个跑不起来的 id 去 resume。
func TestImport_ReadOnlyWhenCwdMissing(t *testing.T) {
	m := withMocks(t) // 没有任何目录存在
	claudeAgentAndBackend(m)
	installSource(t, &fakeSource{
		backend:    agent_backend_entity.TypeClaudeCode,
		transcript: twoTurnTranscript(),
	})

	m.session.EXPECT().ListIDsByProviderSessions(gomock.Any(), []string{testSession}).
		Return(map[string]int64{}, nil)
	var created *chat_entity.Session
	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
			s.ID = 55
			created = s
			return nil
		})
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).Times(4).Return(nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

	got, err := m.svc.Import(context.Background(), importReq(), nil)

	require.NoError(t, err)
	assert.True(t, got.ReadOnly)
	assert.Equal(t, testCwd, got.Cwd, "如实报出那个已经不在的目录,界面才说得清是哪一个没了")
	require.NotNil(t, created)
	assert.Empty(t, created.ProviderSessionID)
	assert.Empty(t, created.Cwd, "那个目录已经没了,钉上去只会让下一轮起不来:不钉,按老规矩现算")
	assert.Equal(t, 2, got.ImportedTurns)
}

// TestImport_ReadOnlyWithoutProviderSessionID 转录里没有 provider session id 时同样
// 只读:没有它既 resume 不了、也判不了重,写个空串进去只会让这条会话每次都被当成
// "没导过"、被反复导入。
func TestImport_ReadOnlyWithoutProviderSessionID(t *testing.T) {
	m := withMocks(t, testCwd)
	claudeAgentAndBackend(m)
	tr := twoTurnTranscript()
	tr.meta.ProviderSessionID = ""
	installSource(t, &fakeSource{backend: agent_backend_entity.TypeClaudeCode, transcript: tr})

	m.session.EXPECT().ListIDsByProviderSessions(gomock.Any(), []string{""}).
		Return(map[string]int64{}, nil)
	var created *chat_entity.Session
	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
			s.ID = 55
			created = s
			return nil
		})
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).Times(4).Return(nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

	got, err := m.svc.Import(context.Background(), importReq(), nil)

	require.NoError(t, err)
	assert.True(t, got.ReadOnly)
	require.NotNil(t, created)
	assert.Empty(t, created.ProviderSessionID)
}

// TestImport_RejectsBackendMismatch 选了一个 codex agent 去接一条 claude 会话:
// CLI 那边根本不认识这个 id,必须当场拒绝而不是导完再发现续不上。
func TestImport_RejectsBackendMismatch(t *testing.T) {
	m := withMocks(t, testCwd)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).
		Return(&agent_entity.Agent{ID: 7, AgentBackendID: 42}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(42)).
		Return(&agent_backend_entity.AgentBackend{ID: 42, Type: string(agent_backend_entity.TypeCodex)}, nil)
	installSource(t, &fakeSource{
		backend:    agent_backend_entity.TypeClaudeCode,
		transcript: twoTurnTranscript(),
	})

	got, err := m.svc.Import(context.Background(), importReq(), nil)

	require.Error(t, err)
	assert.Nil(t, got)
	assert.Equal(t, 0, m.tx.ran, "拒绝发生在任何写入之前")
}

// TestImport_UnregisteredBackendIsRefused 该后端在这台机器上没有读取器时如实报错,
// 不静默返回一条空会话。builtin 是最干净的例子:它的会话本来就只在 agentre 库里,
// 没有"本地磁盘档案"这回事,永远不会有读取器注册进来。
func TestImport_UnregisteredBackendIsRefused(t *testing.T) {
	m := withMocks(t, testCwd)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).
		Return(&agent_entity.Agent{ID: 7, AgentBackendID: 42}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(42)).
		Return(&agent_backend_entity.AgentBackend{ID: 42, Type: string(agent_backend_entity.TypeBuiltin)}, nil)

	req := importReq()
	req.Backend = string(agent_backend_entity.TypeBuiltin)
	_, err := m.svc.Import(context.Background(), req, nil)
	require.Error(t, err)
	assert.Equal(t, 0, m.tx.ran)
}

// textOf 取一个已解码块的正文(注册表解回来的是值还是指针由 factory 决定,
// 断言不该押在这上面)。
func textOf(t *testing.T, b cagoblocks.ContentBlock) string {
	t.Helper()
	switch v := b.(type) {
	case *cagoblocks.TextBlock:
		return v.Text
	case cagoblocks.TextBlock:
		return v.Text
	}
	return ""
}

// TestImport_CancelStopsMidwayAndCommitsNothing 钉住 spec「导入过程给出按轮计的
// 进度,可取消」:用户在写到一半时取消,这一笔整个回滚 —— 取消与失败一样,不留半截
// 会话。取消的抓手是发起时那个 RequestID(同 agent_backend_svc.CancelTest 的先例)。
func TestImport_CancelStopsMidwayAndCommitsNothing(t *testing.T) {
	m := withMocks(t, testCwd)
	claudeAgentAndBackend(m)
	installSource(t, &fakeSource{
		backend:    agent_backend_entity.TypeClaudeCode,
		transcript: twoTurnTranscript(),
	})
	ctx := context.Background()

	m.session.EXPECT().ListIDsByProviderSessions(gomock.Any(), []string{testSession}).
		Return(map[string]int64{}, nil)
	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	// 只写得下第一轮那一对:第二轮之前取消已经生效。
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).Times(2).Return(nil)

	req := importReq()
	req.RequestID = "req-cancel-1"
	var canceled *CancelImportResponse
	resp, err := m.svc.Import(ctx, req, func(done, _ int) {
		if done != 1 {
			return
		}
		var cerr error
		canceled, cerr = m.svc.Cancel(ctx, &CancelImportRequest{RequestID: req.RequestID})
		require.NoError(t, cerr)
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	require.NotNil(t, canceled)
	assert.True(t, canceled.Canceled, "取消要命中那一笔在跑的导入")
	assert.True(t, m.tx.rolledBack, "取消后事务必须回滚")
	assert.False(t, m.tx.committed)
}

// TestImport_UnknownRequestIDIsNotAnError 未知 RequestID 只答「没命中」:导入刚返回、
// 取消慢半拍是前端常态,报错只会平白刷红。
func TestImport_UnknownRequestIDIsNotAnError(t *testing.T) {
	m := withMocks(t)
	resp, err := m.svc.Cancel(context.Background(), &CancelImportRequest{RequestID: "nobody"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.False(t, resp.Canceled)
}

// TestImport_PinsExecDeviceWhenResumable 钉住 spec「续跑」:目录存在时,导入的会话
// 写入 provider_session_id **与执行设备** —— 从另一台机器导进来的会话,下一轮要回到
// 那台机器上跑;记成本机就等于拿着一个本机根本没有的 provider session id 去 resume。
func TestImport_PinsExecDeviceWhenResumable(t *testing.T) {
	m := withMocks(t, testCwd)
	claudeAgentAndBackend(m)
	tr := twoTurnTranscript()
	installSource(t, &fakeSource{backend: agent_backend_entity.TypeClaudeCode, transcript: tr})
	// 远端设备的读取器由 sources 解析器给出:这里只关心落库那一列,读取器仍用 fake。
	m.svc.sources = func(_ context.Context, _ int64) ([]transcriptimport.Source, error) {
		return transcriptimport.Sources(), nil
	}
	ctx := context.Background()

	m.session.EXPECT().ListIDsByProviderSessions(gomock.Any(), []string{testSession}).
		Return(map[string]int64{}, nil)
	var created *chat_entity.Session
	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
			created = s
			s.ID = 55
			return nil
		})
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

	req := importReq()
	req.DeviceID = 5
	resp, err := m.svc.Import(ctx, req, nil)
	require.NoError(t, err)
	assert.False(t, resp.ReadOnly)
	require.NotNil(t, created)
	assert.Equal(t, testSession, created.ProviderSessionID)
	assert.Equal(t, int64(5), created.ExecDeviceID, "执行设备要跟着转录来的那台机器")
}

// TestImport_AdoptsPickedCwd 钉死 spec「续跑」里那条出口:目录不存在时降级为只读,
// 「并给出「选择新目录」的出口」—— 选中的目录必须成为**这条会话的工作目录**
// (落在 chat_sessions.cwd 上),而不是只改扫描筛选。
//
// 它仍然只读:provider session id 是按原目录记的,claude 的 --resume 换个目录就
// 找不到那条会话(决策 16)。换来的是「这条会话接着聊时从哪儿起 CLI」有了答案。
func TestImport_AdoptsPickedCwd(t *testing.T) {
	m := withMocks(t, "/picked/dir") // 转录记的那个目录已经没了,新选的这个在
	claudeAgentAndBackend(m)
	installSource(t, &fakeSource{
		backend:    agent_backend_entity.TypeClaudeCode,
		transcript: twoTurnTranscript(),
	})

	m.session.EXPECT().ListIDsByProviderSessions(gomock.Any(), []string{testSession}).
		Return(map[string]int64{}, nil)
	var created *chat_entity.Session
	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
			s.ID = 55
			created = s
			return nil
		})
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).Times(4).Return(nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

	req := importReq()
	req.Cwd = "/picked/dir"
	got, err := m.svc.Import(context.Background(), req, nil)

	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, "/picked/dir", created.Cwd, "选中的目录就是这条会话的工作目录")
	assert.True(t, got.ReadOnly, "换了目录仍然只读:provider session id 是按原目录记的")
	assert.Empty(t, created.ProviderSessionID)
	assert.Equal(t, testCwd, got.Cwd, "回执如实报出转录里记的那个目录")
}

// TestImport_PickedCwdNeverResumes 原目录还在、用户偏要另选一个时同样只读:
// 续跑必须在原目录启动 CLI(决策 16),钉一个换了目录的 provider session id
// 只会让下一轮报一个更难懂的错。
func TestImport_PickedCwdNeverResumes(t *testing.T) {
	m := withMocks(t, testCwd, "/picked/dir") // 两个目录都在
	claudeAgentAndBackend(m)
	installSource(t, &fakeSource{
		backend:    agent_backend_entity.TypeClaudeCode,
		transcript: twoTurnTranscript(),
	})

	m.session.EXPECT().ListIDsByProviderSessions(gomock.Any(), []string{testSession}).
		Return(map[string]int64{}, nil)
	var created *chat_entity.Session
	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
			s.ID = 55
			created = s
			return nil
		})
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).Times(4).Return(nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

	req := importReq()
	req.Cwd = "/picked/dir"
	got, err := m.svc.Import(context.Background(), req, nil)

	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, "/picked/dir", created.Cwd)
	assert.True(t, got.ReadOnly)
	assert.Empty(t, created.ProviderSessionID)
}
