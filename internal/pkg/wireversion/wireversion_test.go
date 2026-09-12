package wireversion_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
)

// 两个显然不是本 build 的版本号:一个在本 build 之上,一个在本 build 之下。
//
// 相等比较下「新」和「旧」不再是两种结局,它们只是同一件事的两个方向 —— 都不是本
// build 的号,都握不上手。号取得夸张,是为了让这两条守卫不随本 build 的版本抬升而
// 失效;下面每条用例都先断言它们确实不等于本 build 的号,以免哪天退化成同义反复。
const (
	newerVersion = "9.9.9"
	olderVersion = "0.0.1"
)

// Given 桌面端与 agentred 是两个独立部署的二进制,协议版本对不上是常态失败模式,
// When  对端出示的号比本 build 新,
// Then  握手判否 —— 相等才算兼容,没有「落在窗口内」这一说。
//
// 这条与下一条一起钉住本轮的判据变化:从前「比我新、但声明的地板还能覆盖我」的对端
// 是放行的(区间协商),现在不是。
func TestMatch_GivenAPeerNewerThanThisBuild_WhenItHandshakes_ThenItIsRejected(t *testing.T) {
	t.Parallel()

	require.NotEqual(t, wireversion.Protocol, newerVersion,
		"fixture 必须真的不是本 build 的号,否则这条用例不再验它声称的东西")

	require.False(t, wireversion.Match(newerVersion),
		"对端的号与本 build 不等时必须在握手处判否,哪怕它更新")
}

// Given 一台还没升级的对端,When 它出示一个更旧的号,Then 同样判否。
func TestMatch_GivenAPeerOlderThanThisBuild_WhenItHandshakes_ThenItIsRejected(t *testing.T) {
	t.Parallel()

	require.NotEqual(t, wireversion.Protocol, olderVersion,
		"fixture 必须真的不是本 build 的号")

	require.False(t, wireversion.Match(olderVersion),
		"对端的号比本 build 旧时必须在握手处判否")
}

// Given proto3 给缺失字段和显式空串同一个零值,When 对端根本没填这一格,
// Then 必须当成不匹配,而不是「和我一样」—— 这就是判据不能写成
// `peer != "" && peer != ours` 的全部理由。
func TestMatch_GivenAPeerThatReportsNoVersion_WhenItHandshakes_ThenItIsRejected(t *testing.T) {
	t.Parallel()

	require.False(t, wireversion.Match(""),
		"没报版本的对端必须判否")
}

// Given 本方与对端出自同一次发布,When 两边出示同一个号,Then 判是 —— 且 Reject 对
// 一个匹配的对端不产生理由。这条钉的是上面几条不是靠「Match 恒为假」蒙对的。
func TestMatch_GivenAPeerOnThisSameProtocolVersion_WhenItHandshakes_ThenItIsAccepted(t *testing.T) {
	t.Parallel()

	require.True(t, wireversion.Match(wireversion.Protocol),
		"出示同一个号的对端必须握得上手")
	require.Empty(t, wireversion.Reject(wireversion.Protocol),
		"匹配的对端不产生拒绝理由")
}

// Given 握手被拒时用户看到的就是 Reject 这一句(桌面端把它裹进自己的哨兵错误,daemon
// 把它当 rpcerror.CodeProtocolVersion 放到线上),
// When  渲染一次拒绝理由,
// Then  它同时带上对端报出的号与本 build 的号,并且**不再**用区间说法 —— 窗口拆掉之后
//
//	「accepts protocol versions 0.1.0 to 0.1.0」这种下沿等于上沿的句子是读者的负担,
//	它读起来像还有得谈,其实没有。
func TestReject_GivenAMismatchedPeer_WhenTheReasonIsRendered_ThenItNamesBothVersionsWithoutRangeWording(t *testing.T) {
	t.Parallel()

	reason := wireversion.Reject(newerVersion)

	require.NotEmpty(t, reason, "不匹配的对端必须有拒绝理由")
	require.Contains(t, reason, newerVersion, "拒绝理由要带上对端报出的版本")
	require.Contains(t, reason, wireversion.Protocol, "拒绝理由要带上本 build 的版本")
	require.NotContains(t, reason, " to ",
		"拒绝理由不能再渲染成一个区间 —— 判据是相等,句子也要这么说")
}

// Given MinSupported 是宿主策略(「本 build 只接受自己这一版」)而不是协议事实,
//       所以它被写成字面量而不是读协议模块 —— 代价是没有任何东西钉住它,
// When  有人抬了 wire.proto 里的 protocol_version 却忘了同步这个字面量,
// Then  这条守卫变红。
//
// 它断言的不是「两个数必须相等」这种同义反复,而是这个 build 当前选择的地板恰好
// 就是它自己的号。将来真要保留一个更低的地板(接受旧一版的对端),改的是这条用例
// 的意图说明,而不是顺手把断言删掉 —— 那时 Match 也不再是相等比较了。
func TestMinSupported_GivenThisBuildAcceptsOnlyItsOwnRelease_WhenTheFloorIsRead_ThenItIsThisBuildsVersion(t *testing.T) {
	t.Parallel()

	require.Equal(t, wireversion.Protocol, wireversion.MinSupported,
		"本 build 的地板应当就是它自己的协议号;如果你刚抬了 wire.proto 的 protocol_version,"+
			"请同步 wireversion.MinSupported(它是握手里出示的那格 min_supported_protocol_version)")
}
