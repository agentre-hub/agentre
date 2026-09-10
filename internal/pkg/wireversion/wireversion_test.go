package wireversion_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
)

// lastPreReleaseProtocol 是首发前跑到的最后一档协议号 —— 0.1.0 首发把号收回起点之前,
// 已经装出去的桌面端与已经部署的 agentred 报出来的就是它。
//
// 它写死成字面量而不是从 Protocol 推算:这条守卫要钉的正是「首发前的构建被关在门外」
// 这个具体事实,推算出来的值会随 Protocol 一起漂,守卫也就跟着失效了。
const lastPreReleaseProtocol = "0.5.0"

// Given 0.1.0 首发把协议号从 0.5.0 收回起点(理由见 wireversion.MinSupported 的注释),
// 而首发前的构建按老契约解读帧、也没有首发这一档的方法集;
// When  一台首发前的构建(报出 lastPreReleaseProtocol)来握手;
// Then  握手当场拒绝并说明双方版本 —— 而不是握上手、直到第一次调用才炸。
//
// 号往回收之后「旧」不再等于「小」,拒绝因此不是靠地板挡住的:首发前那档的窗口是
// [0.5.0, 0.5.0],本方 Protocol 0.1.0 落在它下面,握手的第二个方向(本方 Protocol 不得
// 老于对端 MinSupported)判否。两个方向都查是这条判据自带的性质,不是本轮新加的 ——
// 这条守卫钉的是它在号收回之后依然成立。
func TestMatch_GivenABuildFromBeforeTheVersionReset_WhenItHandshakes_ThenItIsRejectedThere(t *testing.T) {
	t.Parallel()

	require.NotEqual(t, lastPreReleaseProtocol, wireversion.Protocol,
		"协议版本必须已经收回首发这一档,否则首发前的构建仍旧握得上手")
	require.False(t, wireversion.Match(lastPreReleaseProtocol, lastPreReleaseProtocol),
		"首发前的构建必须在握手处被拒")
	require.Contains(t, wireversion.Reject(lastPreReleaseProtocol, lastPreReleaseProtocol), lastPreReleaseProtocol,
		"拒绝理由要带上对端报出的版本")
	require.Contains(t, wireversion.Reject(lastPreReleaseProtocol, lastPreReleaseProtocol), wireversion.Protocol,
		"拒绝理由要带上本方窗口")
}
