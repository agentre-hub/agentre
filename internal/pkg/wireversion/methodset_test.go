package wireversion_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// methodSetDigest 是 RpcMethod 枚举当前这一份「方法名 → 编号」的指纹。
//
// 它按名字排序后再摘要,因此**只**对方法集本身敏感:重排 .proto 里的书写顺序不动它,
// 增删一个方法、改一个方法的编号则必然改变它。
const methodSetDigest = "e2a957e0470d0db493355154d69b5ba14357ae5e973618432afcbc6518dc317f"

// Given 握手把「对端的 Protocol 落在本方 [MinSupported, Protocol] 窗口内,且本方的
// Protocol 落在对端窗口内」当成兼容判据(wireversion.Match),
// When 有人给 RpcMethod 加/删一个方法却没有同时改 agentre-wire 的版本号,
// Then 这条守卫必须判红。
//
// 为什么需要它:窗口握手允许两端版本不同,前提是「方法集没变」——一旦方法集变了,
// 旧窗口还留着就等于允许一个不认识新方法的旧构建握上手,然后在第一次调用新方法时
// 才炸,而那正是被删掉的那些降级分支原本兜住的形态。方法集指纹与版本号钉在同一个
// 常量对上,这个前提就成立了。
//
// 改了方法集怎么办:把下面报出来的新指纹填进 methodSetDigest,把
// frontend/packages/agentre-wire/package.json 的 version 与 wireversion.Protocol
// 一起往上抬,并把 wireversion.MinSupported 一并抬到与新 Protocol 相等(见下面
// TestMethodSet_GivenTheMethodSetDigestWasLastUpdated_...)——不把 MinSupported 重置,
// 新方法集就落进了旧窗口容许的范围,这条测试才会绿。
func TestMethodSet_GivenTheStrictVersionHandshake_WhenTheMethodSetChanges_ThenTheProtocolVersionMustBeBumpedToo(t *testing.T) {
	t.Parallel()

	enum := agentrewire.RpcMethod(0).Descriptor()
	values := enum.Values()
	lines := make([]string, 0, values.Len())
	for i := range values.Len() {
		v := values.Get(i)
		lines = append(lines, fmt.Sprintf("%s=%d", v.Name(), v.Number()))
	}
	sort.Strings(lines)

	sum := sha256.New()
	for _, line := range lines {
		sum.Write([]byte(line))
		sum.Write([]byte("\n"))
	}
	got := hex.EncodeToString(sum.Sum(nil))

	require.Equal(t, methodSetDigest, got,
		"RpcMethod 方法集变了,protocol version (%s) 必须跟着变 —— 见本测试的注释",
		wireversion.Protocol)
}

// Given 窗口的守恒律——方法集指纹改变时 MinSupported 必须等于 Protocol,When
// methodSetDigest 是这份方法集当下的指纹(即上一条测试为绿,方法集自版本号最近一次
// 抬升以来没有再变过),Then wireversion.MinSupported 在这同一个提交里必须与
// wireversion.Protocol 逐字相等——本轮就是这个"方法集最近一次抬升"的时刻,两者都被
// 重置到 0.2.0(本轮给方法集加了 runtime.setSessionReasoningEffort)。这条断言是手工流程的机械兜底:下次改方法集时,连同上一条测试一起改红,
// 提醒开发者把 MinSupported 也抬到新 Protocol,而不是留着旧窗口悄悄变宽。
func TestMethodSet_GivenTheMethodSetDigestWasLastUpdated_ThenMinSupportedMustEqualProtocolAtThatCommit(t *testing.T) {
	t.Parallel()

	require.Equal(t, wireversion.Protocol, wireversion.MinSupported,
		"窗口的守恒律:方法集指纹改变时 MinSupported 必须等于 Protocol —— 见本测试的注释")
}
