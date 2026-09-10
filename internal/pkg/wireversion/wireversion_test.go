package wireversion_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
)

// unreachableWindow 是一个本方永远落不进去的对端窗口。
//
// 号取得夸张,是为了让这条守卫不随本方 Protocol 的抬升而失效:换成一个挨着当前版本的
// 号,总有一天本方会走到它上面,断言就会在没人注意的情况下从「拒绝」变成「接受」。
const unreachableWindow = "9.9.9"

// Given 本方出示的窗口是一个点(MinSupported == Protocol),
// When  一台对端出示的窗口装不下本方的 Protocol,
// Then  Match 判否,Reject 用双方的号说清楚为什么。
//
// 窗口比较本身的全部方向由 window_internal_test.go 那张表覆盖,用的是合成值。这条补的
// 是导出的那一对:Match 确实把活的 Protocol / MinSupported 喂了进去,而不是拿别的什么去
// 比;以及 Reject 的那句话里同时有对端报出的号和本方的窗口 —— 握手被拒时用户看到的就是
// 它,也是这两个数字唯一露面的地方。
func TestMatch_GivenAPeerWindowThatCannotHoldThisBuild_WhenItHandshakes_ThenItIsRejectedWithBothNumbers(t *testing.T) {
	t.Parallel()

	require.False(t, wireversion.Match(unreachableWindow, unreachableWindow),
		"对端窗口装不下本方 Protocol 时必须在握手处判否")

	reason := wireversion.Reject(unreachableWindow, unreachableWindow)
	require.Contains(t, reason, unreachableWindow, "拒绝理由要带上对端报出的版本")
	require.Contains(t, reason, wireversion.Protocol, "拒绝理由要带上本方窗口的上沿")
	require.Contains(t, reason, wireversion.MinSupported, "拒绝理由要带上本方窗口的下沿")
}

// Given 本方与对端出示同一个窗口,When 握手比较,Then 判是 —— 且 Reject 对一个匹配的
// 对端不产生理由。这条钉的是上一条不是靠「Match 恒为假」蒙对的。
func TestMatch_GivenAPeerOnThisSameWindow_WhenItHandshakes_ThenItIsAccepted(t *testing.T) {
	t.Parallel()

	require.True(t, wireversion.Match(wireversion.Protocol, wireversion.MinSupported),
		"出示同一个窗口的对端必须握得上手")
	require.Empty(t, wireversion.Reject(wireversion.Protocol, wireversion.MinSupported),
		"匹配的对端不产生拒绝理由")
}
