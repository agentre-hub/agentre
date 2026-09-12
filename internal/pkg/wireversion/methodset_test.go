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
const methodSetDigest = "af71a942cd4d3c4e8b2478c5c7c62ff9be3568a891bcb361999c90e5a05cceff"

// Given 握手把「对端与本方的 Protocol 逐字相等」当成兼容判据(wireversion.Match),
// When 有人给 RpcMethod 加/删一个方法却没有同时改协议版本号,
// Then 这条守卫必须判红。
//
// 为什么需要它:相等判据是靠版本号代表方法集的。方法集变了而版本号没动,两端就会在
// 同一个号下跑着两套方法集 —— 握手照旧通过,直到第一次调用新方法才炸,而那正是握手
// 本该当场挡住的形态。方法集指纹与版本号钉在同一个常量对上,这个前提才成立。
//
// 改了方法集怎么办:把下面报出来的新指纹填进 methodSetDigest,把 schema 上的
// (agentre.wire.protocol_version) 往上抬(它就是 wireversion.Protocol),重新生成,
// 并把 wireversion.MinSupported(本 build 出示在握手字段里的那个号)与
// frontend/packages/agentre-wire 的 package.json 一并抬到同一个新号。
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
