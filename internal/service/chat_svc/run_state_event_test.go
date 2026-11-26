package chat_svc

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
)

/*
关联窗「同一份代码上在跑」列的是本桌面端**此刻**在跑(含后台活动)的其它会话。会话
开始 / 不再运行的那一刻,前端要有一条不依赖「这条会话的对话面板开着」的信号:派到一条
没打开的会话上的 agrctl send、agent 派发、对端发起的轮次,此前只在这条会话自己的
旁路流上留痕,别的会话的关联窗看不见它开始跑,要等重新加载才出现。

RunStateEvent 就是这条信号:应用级 Wails 事件,载荷是会话 id,在「运行中」集合
(库里 running / waiting,或后台活动集合非空)可能变化的每一处发一次。
*/

// runStateRecorder 只收 RunStateEvent,其余事件不管。
type runStateRecorder struct {
	mu  sync.Mutex
	ids []int64
}

func (r *runStateRecorder) Emit(_ context.Context, name string, payload any) {
	if name != RunStateEvent {
		return
	}
	id, _ := payload.(int64)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, id)
}

func (r *runStateRecorder) got() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.ids...)
}

// Given 一条会话的轮次开始或收场(用户轮 / compact / 派发轮与自主续轮各两处边界),
// When 走到轮次边界的发布点, Then 应用级 RunStateEvent 带着这条会话的 id 发一次。
func TestRunStateEvent_GivenTurnBoundary_ThenAppWideEventCarriesTheSession(t *testing.T) {
	for name, publish := range map[string]func(s *chatSvc){
		"turn started":            func(s *chatSvc) { s.publishPeerTurnStarted(41) },
		"turn ended":              func(s *chatSvc) { s.publishPeerTurnEnded(41, &chat_entity.Message{}, nil) },
		"turn failed":             func(s *chatSvc) { s.publishPeerTurnEnded(41, nil, errors.New("boom")) },
		"autonomous turn started": func(s *chatSvc) { s.publishPeerAutonomousTurnStarted(41, "background_task") },
		"autonomous turn ended":   func(s *chatSvc) { s.publishPeerAutonomousTurnEnded(41, &chat_entity.Message{}, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			rec := &runStateRecorder{}
			s := &chatSvc{emitter: rec}

			publish(s)

			assert.Equal(t, []int64{41}, rec.got())
		})
	}
}

// Given 后台活动集合, When 它在空与非空之间翻转, Then 发一次;集合里加减但仍非空时不发
// (这条会话一直在跑,「谁在跑」没变)。
func TestRunStateEvent_GivenBackgroundSetFlips_ThenEmittedOnlyOnTheFlip(t *testing.T) {
	rec := &runStateRecorder{}
	s := &chatSvc{emitter: rec}

	s.addBgRunning(7, "a")
	assert.Equal(t, []int64{7}, rec.got(), "空 → 非空:开始有后台活动")

	s.addBgRunning(7, "b")
	s.removeBgRunning(7, "a")
	assert.Equal(t, []int64{7}, rec.got(), "仍非空:不发")

	s.removeBgRunning(7, "b")
	assert.Equal(t, []int64{7, 7}, rec.got(), "非空 → 空:后台活动全部结束")

	s.removeBgRunning(7, "b")
	s.clearBgRunning(7)
	assert.Equal(t, []int64{7, 7}, rec.got(), "本来就空:不发")

	s.addBgRunning(7, "c")
	s.clearBgRunning(7)
	assert.Equal(t, []int64{7, 7, 7, 7}, rec.got(), "清空非空集合(子进程退出)也是一次翻转")
}

// Given 一条停在 running、却没有轮次在收尾的会话(遗孤), When 停止把它翻回 idle,
// Then 落库之后发一次;落库失败时不发(库里仍是 running,「谁在跑」没变)。
func TestRunStateEvent_GivenOrphanReconciledToIdle_ThenEmittedAfterThePersist(t *testing.T) {
	ctrl := gomock.NewController(t)
	sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
	prev := chat_repo.Session()
	chat_repo.RegisterSession(sessRepo)
	t.Cleanup(func() { chat_repo.RegisterSession(prev) })

	rec := &runStateRecorder{}
	s := &chatSvc{emitter: rec}

	sessRepo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
		func(context.Context, *chat_entity.Session) error {
			assert.Empty(t, rec.got(), "落库之前不发:前端据它重取,看到的必须已经是 idle")
			return nil
		})
	assert.NoError(t, s.reconcileSessionToIdle(context.Background(), &chat_entity.Session{ID: 9, AgentStatus: "running"}))
	assert.Equal(t, []int64{9}, rec.got())

	sessRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(errors.New("db locked")).Times(2)
	assert.Error(t, s.reconcileSessionToIdle(context.Background(), &chat_entity.Session{ID: 10, AgentStatus: "running"}))
	assert.Equal(t, []int64{9}, rec.got())
}

// 没有接 emitter 的 chatSvc(单元测试里的裸构造)不炸。
func TestRunStateEvent_GivenNoEmitter_ThenNoPanic(t *testing.T) {
	s := &chatSvc{}
	assert.NotPanics(t, func() {
		s.addBgRunning(3, "a")
		s.publishPeerTurnStarted(3)
	})
}
