package chat_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
)

// Given 远端那一路的插话以**预览帧**到达(applyPreview → applyLive(preview=true));
// When  turnRun 处理它;
// Then  只 emit 一发清 chip 的 StreamSteerConsumed(不带任何消息行),这一轮的
//
//	assistant 一动不动 —— 落库归宿主发来的持久帧,在这里再落一次就是同一段写两遍
//	(spec 2026-09-07-host-transcript-user-input 决策 2;规格 2026-09-05 的两级帧
//	划分:预览帧不进转录)。
//
// 这里刻意不接库:守卫一旦被拿掉,这条预览帧就会一路走到 persistConsumedSteers 去落库
// 并当场炸在没有库的 db.Ctx 上 —— 用例因此真的分得出有没有守卫,而不是两种情况下都
// 绿着(实测:去掉守卫即红)。
// 自主续轮那一路的同一条守卫另有用例
// (TestDriveAutonomousTurn_PreviewSteerConsumedRendersButDoesNotPersist)。
func TestTurnRun_PreviewSteerConsumed_ClearsTheChipWithoutSegmenting(t *testing.T) {
	emitter := &captureEmitter{}
	svc := &chatSvc{emitter: emitter}
	assistant := &chat_entity.Message{ID: 9001, SessionID: 100, Role: "assistant", BlocksJSON: "[]"}
	tr := &turnRun{
		svc:          svc,
		sess:         &chat_entity.Session{ID: 100},
		be:           &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"},
		assistantMsg: assistant,
		acc:          turn.New(),
		turnCtx:      &turn.TurnContext{Waits: turn.NewWaitTracker()},
		previewAcc:   turn.New(),
		stream:       "chat:100:1",
	}

	tr.applyPreview(context.Background(), agentruntime.SteerConsumed{
		Steers: []agentruntime.ConsumedSteer{{QueuedID: "q-1", Text: "插一句"}},
	})

	require.Len(t, emitter.events, 1, "预览帧上的插话仍要 emit 一发 StreamSteerConsumed 清 chip")
	got := emitter.events[0]
	assert.Equal(t, StreamSteerConsumed, got.Kind)
	assert.Equal(t, []string{"q-1"}, got.QueuedIDs)
	assert.Empty(t, got.UserMessages, "清 chip 那一发不得携带消息行")
	assert.Nil(t, got.AssistantMessage, "清 chip 那一发不得携带新 assistant")
	assert.Same(t, assistant, tr.assistantMsg, "预览帧不得让这一轮换掉当前 assistant")
	assert.Empty(t, tr.pendingSteers, "预览帧上的插话不进待分段队列")
}
