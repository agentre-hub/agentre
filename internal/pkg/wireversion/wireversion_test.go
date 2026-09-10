package wireversion_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
)

// previousProtocol 是**上一档**协议版本 —— 本轮之前发布的构建报出来的就是它。
//
// 它写死成字面量而不是从 Protocol 推算:这条守卫要钉的正是"上一档被关在门外"这个
// 具体事实,推算出来的值会随 Protocol 一起漂,守卫也就跟着失效了。抬版本时这里要
// 跟着改成新的上一档 —— 忘了改,守卫就退化成在钉一个早已没人报的旧号。
const previousProtocol = "0.3.0"

// Given 两级帧改了线上契约(预览帧不带 seq、补齐只回块级持久帧),旧构建按老契约
// 解读新帧就会静默错位;
// When  一台落后一档的构建(报出 previousProtocol)来握手;
// Then  握手当场拒绝并说明双方版本 —— 而不是握上手、直到第一条新帧才炸。
//
// 这是"不做跨版本降级分支"的另一半(spec「兼容性」):不给旧构建留兼容路径,就必须
// 保证它根本连不上。窗口在本轮抬升之后收成一个点(MinSupported == Protocol,见
// methodset_test.go 的守恒律),上一档因此落在 MinSupported 之下。
func TestMatch_GivenABuildOneVersionBehind_WhenItHandshakes_ThenItIsRejectedThere(t *testing.T) {
	t.Parallel()

	require.NotEqual(t, previousProtocol, wireversion.Protocol,
		"协议版本必须已经跨过两级帧这一档,否则上一档的构建仍旧握得上手")
	require.False(t, wireversion.Match(previousProtocol, previousProtocol),
		"落后一档的构建必须在握手处被拒")
	require.Contains(t, wireversion.Reject(previousProtocol, previousProtocol), previousProtocol,
		"拒绝理由要带上对端报出的版本")
	require.Contains(t, wireversion.Reject(previousProtocol, previousProtocol), wireversion.Protocol,
		"拒绝理由要带上本方窗口")
}
