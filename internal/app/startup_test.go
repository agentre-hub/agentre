package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

func TestResetStaleSessionsOnStartup(t *testing.T) {
	called := 0
	orig := resetStaleActiveSessions
	resetStaleActiveSessions = func(context.Context) error {
		called++
		return nil
	}
	t.Cleanup(func() { resetStaleActiveSessions = orig })
	origSettle := settleOrphanedAgrctlCards
	settleOrphanedAgrctlCards = func(context.Context) (int, error) { return 0, nil }
	t.Cleanup(func() { settleOrphanedAgrctlCards = origSettle })

	a := &App{}
	a.resetStaleSessionsOnStartup(context.Background())

	if called != 1 {
		t.Fatalf("resetStaleActiveSessions called %d times, want 1", called)
	}
}

// 重启后仍 running/waiting 的本机 agrctl 卡随进程一起成了孤儿:收尾必须排在会话重置之后
// (同一个启动清理步骤里),且重置失败不挡它 —— 两者各管各的行。
func TestResetStaleSessionsOnStartup_SettlesOrphanedAgrctlCardsAfterReset(t *testing.T) {
	var order []string
	origReset, origSettle := resetStaleActiveSessions, settleOrphanedAgrctlCards
	resetStaleActiveSessions = func(context.Context) error {
		order = append(order, "reset")
		return errors.New("reset failed")
	}
	settleOrphanedAgrctlCards = func(context.Context) (int, error) {
		order = append(order, "settle")
		return 1, nil
	}
	t.Cleanup(func() { resetStaleActiveSessions, settleOrphanedAgrctlCards = origReset, origSettle })

	(&App{}).resetStaleSessionsOnStartup(context.Background())

	if len(order) != 2 || order[0] != "reset" || order[1] != "settle" {
		t.Fatalf("startup cleanup order = %v, want [reset settle]", order)
	}
}

// catchUpChatSvc 只实现补齐那一个方法;其余靠嵌入接口占位(测试里不会被调到)。
type catchUpChatSvc struct {
	chat_svc.ChatSvc
	devices chan int64
}

func (f *catchUpChatSvc) CatchUpRemoteDevice(_ context.Context, deviceID int64) error {
	f.devices <- deviceID
	return nil
}

// 启动补齐只在 Startup 跑一次:开机自启早于 Wi-Fi/VPN 就绪、或 daemon 正在重启时那一次
// 必然拨不通,而设备重新上线时没有任何东西重跑它 —— 该设备上的会话此后再不会被补齐或
// 接管。设备监视本来就在报上线/下线(usage ticker 已经挂在这条信号上),补齐挂上去即可。
func TestOnRemoteDeviceOnline_DrivesCatchUpForThatDevice(t *testing.T) {
	fake := &catchUpChatSvc{devices: make(chan int64, 4)}
	prev := chat_svc.Chat()
	chat_svc.RegisterChat(fake)
	t.Cleanup(func() { chat_svc.RegisterChat(prev) })

	a := &App{}
	a.onRemoteDeviceOnline(7, true)

	select {
	case got := <-fake.devices:
		if got != 7 {
			t.Fatalf("caught up device %d, want 7", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("设备重新上线后没有重跑补齐:那一次拨号失败就是终局")
	}

	// 掉线是另一半信号:那时拨号必失败,补齐没有意义。
	a.onRemoteDeviceOnline(7, false)
	select {
	case got := <-fake.devices:
		t.Fatalf("device %d went offline but catch-up ran anyway", got)
	case <-time.After(100 * time.Millisecond):
	}
}
