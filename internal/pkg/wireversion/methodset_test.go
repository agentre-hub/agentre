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
const methodSetDigest = "c043155a6d3c7cc900a48ad97c58448036c3e0be986eda47dcb7524d42c19a81"

// Given 握手把「对端版本必须与本构建逐字相等」当成唯一的兼容判据(wireversion.Match),
// When 有人给 RpcMethod 加/删一个方法却没有同时改 agentre-wire 的版本号,
// Then 这条守卫必须判红。
//
// 为什么需要它:严格相等的握手之所以能替掉所有 per-method 的 method-not-found 降级,
// 前提是「方法集变了 ⇒ 版本号变了」。这个前提今天没有任何机械保证 —— 两个都自称
// 0.1.0、方法集却不同的构建能握上手,然后在第一次调用新方法时才炸,而那正是被删掉的
// 那些降级分支原本兜住的形态。方法集指纹与版本号钉在同一个常量对上,前提就成立了。
//
// 改了方法集怎么办:把下面报出来的新指纹填进 methodSetDigest,并把
// frontend/packages/agentre-wire/package.json 的 version 与 wireversion.Protocol
// 一起往上抬。两件事一起做,这条测试才会绿。
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
