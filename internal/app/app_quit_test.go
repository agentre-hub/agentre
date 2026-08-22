package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/internal/service/chat_svc"
)

func TestShouldPreventQuit(t *testing.T) {
	ctx := context.Background()

	t.Run("confirmed quit is allowed without counting or emitting", func(t *testing.T) {
		countCalls, emitCalls := 0, 0
		prevent := shouldPreventQuit(ctx, true,
			func(context.Context) (int, error) { countCalls++; return 3, nil },
			func(int) { emitCalls++ })
		if prevent {
			t.Fatal("confirmed quit must be allowed (prevent=false)")
		}
		if countCalls != 0 {
			t.Fatalf("count called %d times, want 0 (short-circuit on confirmed)", countCalls)
		}
		if emitCalls != 0 {
			t.Fatalf("emit called %d times, want 0", emitCalls)
		}
	})

	t.Run("no active sessions is allowed without emitting", func(t *testing.T) {
		emitCalls := 0
		prevent := shouldPreventQuit(ctx, false,
			func(context.Context) (int, error) { return 0, nil },
			func(int) { emitCalls++ })
		if prevent {
			t.Fatal("zero active sessions must be allowed (prevent=false)")
		}
		if emitCalls != 0 {
			t.Fatalf("emit called %d times, want 0", emitCalls)
		}
	})

	t.Run("active sessions are prevented and emit the count", func(t *testing.T) {
		emitCalls, emitted := 0, 0
		prevent := shouldPreventQuit(ctx, false,
			func(context.Context) (int, error) { return 2, nil },
			func(n int) { emitCalls++; emitted = n })
		if !prevent {
			t.Fatal("active sessions must prevent quit (prevent=true)")
		}
		if emitCalls != 1 {
			t.Fatalf("emit called %d times, want 1", emitCalls)
		}
		if emitted != 2 {
			t.Fatalf("emitted count = %d, want 2", emitted)
		}
	})

	t.Run("count error fails open (allow) without emitting", func(t *testing.T) {
		emitCalls := 0
		prevent := shouldPreventQuit(ctx, false,
			func(context.Context) (int, error) { return 0, errors.New("db down") },
			func(int) { emitCalls++ })
		if prevent {
			t.Fatal("count error must fail open (prevent=false) so the user is never trapped")
		}
		if emitCalls != 0 {
			t.Fatalf("emit called %d times, want 0", emitCalls)
		}
	})
}

func TestConfirmQuit_GivenFinalQuitBlocks_WhenUserConfirms_ThenReturnsImmediatelyAndSchedulesQuitOnce(t *testing.T) {
	t.Parallel()

	quitStarted := make(chan struct{})
	releaseQuit := make(chan struct{})
	var calls atomic.Int32
	a := NewApp()
	a.forceExit = func(int) {}
	a.finalQuit = func(context.Context) {
		calls.Add(1)
		close(quitStarted)
		<-releaseQuit
	}

	returned := make(chan struct{})
	go func() {
		a.ConfirmQuit()
		a.ConfirmQuit()
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		close(releaseQuit)
		t.Fatal("confirmed quit must return immediately even while the native quit call is blocked")
	}

	select {
	case <-quitStarted:
	case <-time.After(time.Second):
		close(releaseQuit)
		t.Fatal("confirmed quit did not schedule the final native quit")
	}
	if got := calls.Load(); got != 1 {
		close(releaseQuit)
		t.Fatalf("final native quit called %d times, want exactly once", got)
	}
	close(releaseQuit)
}

func TestShutdown_GivenResourceCleanupBlocks_WhenAppExits_ThenShutdownReturnsWithinDeadline(t *testing.T) {
	t.Parallel()

	cleanupStarted := make(chan struct{})
	releaseCleanup := make(chan struct{})
	a := NewApp()
	a.quitConfirmed.Store(true)
	a.shutdownCleanup = func(context.Context) {
		close(cleanupStarted)
		<-releaseCleanup
	}

	startedAt := time.Now()
	a.Shutdown(context.Background())
	elapsed := time.Since(startedAt)

	select {
	case <-cleanupStarted:
	case <-time.After(time.Second):
		close(releaseCleanup)
		t.Fatal("shutdown did not schedule necessary resource cleanup")
	}
	if elapsed > 100*time.Millisecond {
		close(releaseCleanup)
		t.Fatalf("shutdown took %s, want it to return within 100ms when cleanup blocks", elapsed)
	}
	close(releaseCleanup)
}

func TestConfirmQuit_GivenNativeQuitDoesNotTerminate_WhenGracePeriodExpires_ThenForcesProcessExit(t *testing.T) {
	t.Parallel()

	nativeQuit := make(chan struct{})
	releaseNativeQuit := make(chan struct{})
	forcedExit := make(chan int, 1)
	a := NewApp()
	a.forcedExitDelay = time.Millisecond
	a.finalQuit = func(context.Context) {
		close(nativeQuit)
		<-releaseNativeQuit
	}
	a.forceExit = func(code int) { forcedExit <- code }
	t.Cleanup(func() { close(releaseNativeQuit) })

	a.ConfirmQuit()

	select {
	case <-nativeQuit:
	case <-time.After(time.Second):
		t.Fatal("confirmed quit must request the normal native quit first")
	}
	select {
	case code := <-forcedExit:
		if code != 0 {
			t.Fatalf("forced exit code = %d, want 0", code)
		}
	case <-time.After(time.Second):
		t.Fatal("confirmed quit must force process exit when native quit does not terminate")
	}
}

// TestOnBeforeClose_NilChatServiceFailsOpen guards the race where the user quits
// before Startup finishes wiring the chat service. Wails runs OnStartup in a
// goroutine concurrent with the window run loop (darwin/windows/linux all do), so
// cmd+Q / close button can fire OnBeforeClose while chat_svc.Chat() is still nil.
// That must fail open (allow quit), never panic on a nil-interface dereference.
func TestOnBeforeClose_NilChatServiceFailsOpen(t *testing.T) {
	prev := chat_svc.Chat()
	t.Cleanup(func() { chat_svc.RegisterChat(prev) })
	chat_svc.RegisterChat(nil) // simulate the pre-registration window

	a := NewApp()
	// quitConfirmed defaults to false → OnBeforeClose has to count active sessions.
	if prevent := a.OnBeforeClose(context.Background()); prevent {
		t.Fatal("early quit before chat service registration must fail open (prevent=false)")
	}
}

// killablePooledSession 是一个假的常驻 CLI 会话:记录自己有没有被硬杀。
type killablePooledSession struct {
	once   sync.Once
	killed chan struct{}
}

func newKillablePooledSession() *killablePooledSession {
	return &killablePooledSession{killed: make(chan struct{})}
}

func (k *killablePooledSession) Close(context.Context) error { return nil }

func (k *killablePooledSession) Kill(context.Context) error {
	k.once.Do(func() { close(k.killed) })
	return nil
}

// Given 用户已确认退出、池里还留着常驻 CLI 子进程, When Shutdown 返回, Then 那些
// 子进程必须已经被收掉。
//
// 这条路上 Shutdown 不等资源清理(见上一个用例:卡住的外部进程不得拖住退出),于是
// 从前的 RemoveAll 是丢进 goroutine 的 —— 桌面进程紧接着退出,那些 goroutine 连同
// 优雅关闭一起消失,而 CLI 自带进程组、不会被连坐,直接变成孤儿。
func TestShutdown_GivenConfirmedQuitWithPooledCLISessions_WhenShutdownReturns_ThenSubprocessesAreGone(t *testing.T) {
	session := newKillablePooledSession()
	agentruntime.DefaultCLISessionPool().Put("app-shutdown-test", session)
	t.Cleanup(func() { agentruntime.DefaultCLISessionPool().Remove("app-shutdown-test") })

	a := NewApp()
	a.quitConfirmed.Store(true)
	a.shutdownCleanup = func(context.Context) {}

	a.Shutdown(context.Background())

	select {
	case <-session.killed:
	default:
		t.Fatal("Shutdown 返回时常驻 CLI 子进程还活着:桌面进程一退它们就成了孤儿")
	}
}
