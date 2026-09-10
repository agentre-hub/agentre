package guard

import (
	"strings"
	"testing"
)

// wireSingleSource 是 wire 协议生成 Go 代码在本仓唯一被允许的 import path。它是独立
// module github.com/agentre-hub/agentre/pkg/wire 的一部分，agentre-server 钉住已推送的
// revision 来消费同一份代码，两仓因此不再各存一份手工拷贝。
const wireSingleSource = "github.com/agentre-hub/agentre/pkg/wire/agentrewire"

// wireDuplicateImports 列出会形成第二份生成代码的仓内 import path。
var wireDuplicateImports = []string{
	"github.com/agentre-hub/agentre/internal/gen/agentrewire",
}

// TestWireHasOneImportPath 守住 wire 生成代码在本仓只有一个 import path。
//
// 这类副本在编译期是完全合法的：两份包各自能编译、各自能过测试，漂移只在两侧被同一条
// 连接的两端同时使用时才暴露。守卫因此放在源码层面，而不是等运行期。
func TestWireHasOneImportPath(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	scanned := 0

	err := walkRepositoryGoFiles(root, func(rel string, content []byte) error {
		scanned++
		if rel == "internal/guard/wire_single_source_test.go" {
			return nil
		}
		for _, duplicate := range wireDuplicateImports {
			if strings.Contains(string(content), `"`+duplicate+`"`) {
				t.Errorf("%s imports %q; wire 生成代码只有 %q 一份", rel, duplicate, wireSingleSource)
			}
			if strings.Contains(string(content), `"`+duplicate+`/v1"`) {
				t.Errorf("%s imports duplicate versioned wire package %q/v1", rel, duplicate)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// 自证不空过：读不到任何 Go 源码时全绿是没有意义的，它既可能是「没人违规」，
	// 也可能是 root 算错了而一个文件都没走到。
	if scanned == 0 {
		t.Fatalf("walked %s but found no Go sources; the guard would pass vacuously", root)
	}
}
