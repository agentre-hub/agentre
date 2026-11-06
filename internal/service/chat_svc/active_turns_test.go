package chat_svc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// HasActiveTurns 是「应用此刻空不空闲」的判据(空闲页定时回收据它跳过):
// 没注册聊天服务、没有在跑的轮次时为空闲;任一会话有在跑的轮次就不空闲。
func TestHasActiveTurns(t *testing.T) {
	prev := defaultChat
	t.Cleanup(func() { defaultChat = prev })

	defaultChat = nil
	assert.False(t, HasActiveTurns(), "no chat service registered yet")

	svc := NewChat(NoopEmitter{}).(*chatSvc)
	defaultChat = svc
	assert.False(t, HasActiveTurns(), "no turn running")

	svc.activeCancels.Store(int64(7), &activeTurnControl{})
	assert.True(t, HasActiveTurns(), "a running turn means the app is not idle")

	svc.activeCancels.Delete(int64(7))
	assert.False(t, HasActiveTurns())
}
