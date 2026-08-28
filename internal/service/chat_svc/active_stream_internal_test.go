package chat_svc

import (
	"testing"

	"github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/view"
)

// activeStreamName backs LoadSession.ActiveStream: when a turn is in-flight, a frontend
// opening the session mid-turn must be able to reattach to the live per-turn stream. The
// stream name is reconstructed from the in-flight (last) assistant message.
func TestActiveStreamName(t *testing.T) {
	msgs := []*chat_entity.Message{
		{ID: 1, Role: "user"},
		{ID: 2, Role: "assistant"},
		{ID: 3, Role: "user"},
		{ID: 4, Role: "assistant"},
	}

	t.Run("active turn points at the last assistant message", func(t *testing.T) {
		if got, want := activeStreamName(true, 7, msgs), StreamName(7, 4); got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})

	t.Run("no active turn returns empty", func(t *testing.T) {
		if got := activeStreamName(false, 7, msgs); got != "" {
			t.Fatalf("got %q want empty", got)
		}
	})

	t.Run("active turn with no assistant message yet returns empty", func(t *testing.T) {
		only := []*chat_entity.Message{{ID: 1, Role: "user"}}
		if got := activeStreamName(true, 7, only); got != "" {
			t.Fatalf("got %q want empty", got)
		}
	})

	// 轮中切换供应商会把一条只承载 notice 的旁白行(appendProviderSwitchNotice,
	// role=assistant、NextSeq 排在在跑那条之后)追加进 transcript。它不是一轮:流名
	// 若按它算,重挂上来的前端就订到一条**没人 emit** 的流名 —— 这一轮余下的流式内容
	// 全看不见,也永远等不到终态。
	t.Run("provider notice row does not steal the in-flight stream name", func(t *testing.T) {
		withNotice := []*chat_entity.Message{
			{ID: 1, Role: "user"},
			{ID: 2, Role: "assistant"},
			noticeOnlyAssistantMessage(t, 3),
		}
		if got, want := activeStreamName(true, 7, withNotice), StreamName(7, 2); got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})

	// 反向边界:轮刚起、内容还没落地的 assistant 行 BlocksJSON 恒为 "[]" —— 没有块
	// ≠ 旁白行,流名就该指向它,否则 Send 后立刻重挂的前端反而订不到在跑的那一轮。
	t.Run("assistant row with no blocks yet is still the in-flight turn", func(t *testing.T) {
		pending := []*chat_entity.Message{
			{ID: 1, Role: "user"},
			{ID: 2, Role: "assistant", BlocksJSON: "[]"},
		}
		if got, want := activeStreamName(true, 7, pending), StreamName(7, 2); got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})

	// 另一条反向边界:回退 notice 不是旁白行 —— 它由 runTurn 追加进**这一轮自己**的
	// assistant 消息(chat.go finalize),所以「块全是 notice」也可能就是一轮真实对话:
	// 用户发完立刻点停止、这一轮零内容收尾时,该消息的块正好只剩这条回退 notice。
	// finalize 写完块到 activeCancels 清掉之间有一个窗口,此刻的 LoadSession 若把它
	// 当旁白行跳过,流名就会退到更早的 assistant —— 前端重挂到一条已经收尾的流上,
	// 永远等不到终态。判据必须是「独立落库的切换 notice」,不是「块全是 notice」。
	t.Run("turn carrying only a fallback notice is still a real turn", func(t *testing.T) {
		fallback := &chat_entity.Message{ID: 3, Role: "assistant"}
		if err := fallback.SetBlocks([]blocks.ContentBlock{blocks.NoticeBlock{
			Level: "info", Text: view.EncodeProviderFallback("gone-provider", ""),
		}}); err != nil {
			t.Fatalf("SetBlocks: %v", err)
		}
		msgs := []*chat_entity.Message{
			{ID: 1, Role: "user"},
			{ID: 2, Role: "assistant"},
			fallback,
		}
		if got, want := activeStreamName(true, 7, msgs), StreamName(7, 3); got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})
}

// noticeOnlyAssistantMessage 造一条「只承载供应商切换 notice 的旁白行」,形状与
// appendProviderSwitchNotice 落库的完全一致(role=assistant + 单个 NoticeBlock)。
func noticeOnlyAssistantMessage(t *testing.T, id int64) *chat_entity.Message {
	t.Helper()
	m := &chat_entity.Message{ID: id, Role: "assistant"}
	if err := m.SetBlocks([]blocks.ContentBlock{blocks.NoticeBlock{
		Level: "info", Text: view.EncodeProviderSwitch("session-key", "", "中转 · GLM 5.2", ""),
	}}); err != nil {
		t.Fatalf("SetBlocks: %v", err)
	}
	return m
}
