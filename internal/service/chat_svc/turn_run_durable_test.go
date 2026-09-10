package chat_svc

import (
	"context"
	"testing"
	"time"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo/mock_transcript_repo"
)

// turn_run_durable_test.go 钉住「宿主分段之后发来的那一行用户消息」在**哪条流**上被
// 认领。
//
// 远端执行是两级帧:插话在实时那一路是预览帧(不进转录),宿主把它落进自己的转录、
// 取号,再作为**持久帧**发回来。持久帧只到 turnRun.applyDurable —— applyLive 那一路
// 在远端执行时只跑 preview=true(见 turnRun.consumeEvents)。认领若写在 applyLive
// 的 preview=false 分支上,生产上一次都跑不到:插话在消费方的转录里一个字都不剩,
// 而用本机 runtime(SwapRuntimeForTest 直接吐一条 UserMessageEvent)写的用例照旧绿着
// —— 本机后端从不产生这类事件(它只出自 transcript.ProjectMessages 与 protowire 解码)。

// durableSegmentationRig 搭一条能真的走完 persistConsumedSteers 的 turnRun:
// sqlmock 的事务 + 消息/会话仓储 mock,发出去的流事件由 captureEmitter 收着。
type durableSegmentationRig struct {
	tr        *turnRun
	ctx       context.Context
	emitter   *captureEmitter
	assistant *chat_entity.Message
	created   []*chat_entity.Message
}

func newDurableSegmentationRig(t *testing.T, expectSegment bool) *durableSegmentationRig {
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

	rig := &durableSegmentationRig{emitter: &captureEmitter{}}
	// 分段那一步的库全部配成**会成功**,不该分段的用例也一样:不配的话
	// persistConsumedSteers 会在 Begin 那一步就失败返回、一条消息都建不出来,
	// 「没分段」于是在分不分段两种情况下都成立 —— 断言恒真,盖住它本该发现的缺陷。
	dbMock.ExpectBegin()
	dbMock.ExpectCommit()
	msgRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	msgRepo.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(7, nil).AnyTimes()
	sessRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	msgRepo.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, m *chat_entity.Message) error {
			if !expectSegment {
				t.Errorf("这一条持久用户消息不该分段,却落了一行 role=%s", m.Role)
				return nil
			}
			m.ID = int64(9100 + len(rig.created))
			rig.created = append(rig.created, m)
			return nil
		}).AnyTimes()

	rig.assistant = &chat_entity.Message{ID: 9001, SessionID: 100, Role: "assistant", BlocksJSON: "[]"}
	sess := &chat_entity.Session{ID: 100}
	// 走 NewChat 而不是字面量:dispatcher 与那几张 map 都由它装配,不带来源的那一条
	// 因此真的会走到与生产同一只 dispatcher 上(它不认 UserMessageEvent、静默丢弃),
	// 而不是靠一个 nil dispatcher 炸出「没分段」的假象。
	svc := NewChat(rig.emitter).(*chatSvc)
	rig.tr = &turnRun{
		svc:          svc,
		sess:         sess,
		be:           &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"},
		assistantMsg: rig.assistant,
		acc:          turn.New(),
		segmentStart: time.Now(),
		turnCtx:      svc.newTurnContext(rig.assistant, sess, "chat:100:9001", "claudecode"),
		stream:       "chat:100:9001",
	}
	rig.ctx = ctx
	return rig
}

// Given 远端宿主把一段插话落进它自己的转录、取号,并作为**持久帧**发回来
//
//	(投影成带提交方来源的 UserMessageEvent);
//
// When  turnRun 在持久帧那条流上收到它(applyDurable);
// Then  它照 SteerConsumed 同一套语义分段:收口当前 assistant、落这一行 user、
//
//	开新一段 assistant,并 emit 一发带消息行的 StreamSteerConsumed。
//
// 认领写在 applyLive 上时这条用例是红的:远端执行那一路的持久帧根本不经过 applyLive
// (实测:把 applyDurable 里的认领删掉,本用例与下面「工具在途」那条当场判红)。
func TestTurnRun_DurableUserMessageWithSource_SegmentsOnTheDurableStream(t *testing.T) {
	rig := newDurableSegmentationRig(t, true)

	rig.tr.applyDurable(rig.ctx, agentruntime.UserMessageEvent{
		Text: "follow-up", SourceDevice: "sha256:peer", SourceDeviceName: "web",
	})

	require.Len(t, rig.emitter.events, 1, "分段要 emit 一发 StreamSteerConsumed")
	got := rig.emitter.events[0]
	assert.Equal(t, StreamSteerConsumed, got.Kind)
	require.Len(t, got.UserMessages, 1, "插话那一行必须随事件交给前端")
	assert.Equal(t, "follow-up", got.UserMessages[0].Blocks[0].Text)
	assert.Equal(t, "sha256:peer", got.UserMessages[0].SourceDevice,
		"提交方来源要一路带到前端 —— 它是「谁说的」唯一依据")
	require.NotNil(t, got.AssistantMessage, "分段必须带上新一段 assistant")
	assert.NotSame(t, rig.assistant, rig.tr.assistantMsg, "后半段要落进新开的那条 assistant")
	assert.Empty(t, rig.tr.pendingSteers)
}

// Given 本轮**自己那条提问**也投影成 UserMessageEvent,但它不带来源(宿主的
//
//	StartTurn 不盖);
//
// When  游标闸门不成立、它照常送达持久帧这条流;
// Then  一行都不落 —— 无条件分段会凭空多出一行提问。
// 判据是来源标识而不是内容比对(2026-09-07 决策 1 的 Rejected A 拒的正是内容比对)。
func TestTurnRun_DurableUserMessageWithoutSource_DoesNotSegment(t *testing.T) {
	rig := newDurableSegmentationRig(t, false)

	rig.tr.applyDurable(rig.ctx, agentruntime.UserMessageEvent{Text: "hi"})

	assert.Empty(t, rig.emitter.events, "不带来源的用户消息不得 emit StreamSteerConsumed")
	assert.Same(t, rig.assistant, rig.tr.assistantMsg, "这一轮的 assistant 一动不动")
	assert.Empty(t, rig.tr.pendingSteers)
}

// Given 工具还在途(tool_use 已到、tool_result 还没到)时插话那一行先到了;
// When  turnRun 收到它;
// Then  先攒着不分段 —— 此刻收口会把 tool_use 冻在旧消息里,随后的 tool_result 在新
//
//	累加器里查不到它、被当孤儿丢弃,工具卡永远停在 running。判据与 SteerConsumed
//	那一处、与 agentred 做宿主时同一条。
func TestTurnRun_DurableUserMessageWhileToolInFlight_DefersTheSegmentation(t *testing.T) {
	rig := newDurableSegmentationRig(t, false)
	rig.tr.acc.AddToolUse(&cagoblocks.ToolUseBlock{ID: "tu-1", Name: "Bash"}, "tool_use:tu-1")

	rig.tr.applyDurable(rig.ctx, agentruntime.UserMessageEvent{
		Text: "mid-tool", SourceDevice: "sha256:peer",
	})

	assert.Empty(t, rig.emitter.events, "工具在途时不得分段")
	assert.Same(t, rig.assistant, rig.tr.assistantMsg)
	require.Len(t, rig.tr.pendingSteers, 1, "插话要攒着,等工具收口再分段")
	assert.Equal(t, "mid-tool", rig.tr.pendingSteers[0].Text)
	assert.Equal(t, "sha256:peer", rig.tr.pendingSteers[0].SourcePeer)
}
