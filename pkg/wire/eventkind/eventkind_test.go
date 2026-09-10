package eventkind_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/eventkind"
)

// Given schema 上每条事件分支都必须带判别值;When 从 descriptor 枚举 oneof;
// Then 一条也不能漏。
//
// 这条守卫是把手抄表搬进 .proto 之后**唯一**顶替它的东西:新加一条 oneof 分支忘了
// 标注,消费方投影不出来 → 整页转录取不出来,而不是少一行。断言从生成的 descriptor
// 枚举,不写第二张清单 —— 写清单等于把手抄再抄一遍。
func TestEveryRuntimeEventBranchDeclaresItsKind(t *testing.T) {
	t.Parallel()

	fields := (&agentrewire.RuntimeEventNotification{}).ProtoReflect().
		Descriptor().Oneofs().ByName("event").Fields()
	require.Positive(t, fields.Len())

	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		require.NotEmptyf(t, eventkind.ForField(field),
			"oneof 分支 %s 没在 .proto 上标注 (agentre.wire.event_kind)", field.Name())
	}
}

// Given 判别值是消费方 switch 的判据;When 两条分支标了同一个值;Then 后到的那条
// 会被当成前一条渲染 —— 所以判别值必须互不相同。
func TestKindsAreUnique(t *testing.T) {
	t.Parallel()

	seen := map[string]string{}
	for branch, kind := range eventkind.All() {
		if previous, duplicate := seen[kind]; duplicate {
			t.Fatalf("分支 %s 与 %s 标了同一个判别值 %q", branch, previous, kind)
		}
		seen[kind] = branch
	}
	require.Len(t, seen, len(eventkind.All()))
}

// Given 一帧置上了某条分支;When 投影;Then 交回的是这条分支标注的判别值与它的载荷。
//
// 用例特意挑 tool_call:它正是分支名与判别值拼法不同的那一档,手抄时代四条错映射里
// 的第一条。
func TestOf_GivenABranchIsSet_WhenProjected_ThenItYieldsTheDeclaredKindAndPayload(t *testing.T) {
	t.Parallel()

	frame := &agentrewire.RuntimeEventNotification{
		ConversationId: "3f2d1b7a-5c44-7a10-9e3b-6a1f0c2d4e88",
		Event: &agentrewire.RuntimeEventNotification_ToolCall{
			ToolCall: &agentrewire.ToolCall{Id: "tool-1", Name: "Read"},
		},
	}

	kind, payload, ok := eventkind.Of(frame)

	require.True(t, ok)
	require.Equal(t, "tool_use_start", kind)
	require.Equal(t, "Read", payload.(*agentrewire.ToolCall).GetName())
}

// Given 一帧一个分支也没置上;When 投影;Then 报「投影不出来」而不是交回空判别值。
func TestOf_GivenNoBranchIsSet_WhenProjected_ThenItReportsFailure(t *testing.T) {
	t.Parallel()

	_, _, ok := eventkind.Of(&agentrewire.RuntimeEventNotification{})

	require.False(t, ok)
}
