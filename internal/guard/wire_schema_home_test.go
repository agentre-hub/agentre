package guard

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// wireSchemaHome 是 agentre ↔ agentred 协议的 .proto schema 在本仓唯一被允许的位置。
//
// 它和生成的 Go 一起住在独立 module github.com/agentre-hub/agentre/pkg/wire 里：那个
// module 是这份协议在 Go 侧的主人，schema 跟着主人走，读 pkg/wire 就能回答「这份协议
// 长什么样」，不必先跳去一个前端 npm 包。TS 侧从这里生成，依赖方向因此只剩
// pkg/wire → 消费方一条边。
const wireSchemaHome = "pkg/wire/proto/agentre/wire/wire.proto"

// wireSchemaCompanions 是驱动那份 schema 的 buf 配置与破坏性变更基线。它们必须和
// schema 同住：`buf lint` / `buf breaking` 的模块根由 buf.yaml 的位置决定，配置留在
// 别的仓内目录就意味着 schema 的主人管不到自己的守卫。
var wireSchemaCompanions = []string{
	"pkg/wire/buf.yaml",
	"pkg/wire/buf.gen.yaml",
	"pkg/wire/proto/baseline.binpb",
}

// TestWireSchemaHasOneHomeInsideTheProtocolModule 守住「协议只有一份 schema，且它住在
// 协议 module 里」。
//
// 和 TestWireHasOneImportPath 是同一条不变式的两端：那条守生成物只有一份，这条守
// **来源**只有一份。第二份 .proto 在编译期同样完全合法——两份 schema 各自能生成、
// 各自能过测试，漂移只在两端被同一条连接同时使用时才暴露。
func TestWireSchemaHasOneHomeInsideTheProtocolModule(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)

	var found []string
	if err := walkRepositoryFiles(root, ".proto", func(rel string, _ []byte) error {
		found = append(found, rel)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(found)

	// 自证不空过：一条 .proto 都没枚举到时报红，否则 root 算错也会全绿。
	if len(found) != 1 || found[0] != wireSchemaHome {
		t.Errorf("仓内 .proto = %v；期望恰好一份，且位于 %q", found, wireSchemaHome)
	}

	for _, companion := range wireSchemaCompanions {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(companion))); err != nil {
			t.Errorf("%s 不在协议 module 里：%v", companion, err)
		}
	}
}
