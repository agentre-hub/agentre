package guard_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// TestSchemaShipsWithTheModuleItGenerates 守的是「协议 module 自解释」这一条。
//
// 这个 module 是 agentre ↔ agentred 协议在 Go 侧的主人，可它整包代码都是 buf 从一份
// .proto 生成出来的。schema 若住在别处（比如前端 npm 包里），依赖方向就是反的：主人要靠消费方的目录才
// 能重新生成自己，而消费方——agentre-server 钉的是这个 module 的一个不可变 revision
// ——拿到的包里根本没有那份 schema，回答不了「这份协议长什么样」。
//
// 断言把编译进来的 descriptor 与磁盘上的文件对起来：descriptor 自己记录的
// 文件路径（agentre/wire/wire.proto，buf 模块内的相对路径）必须能在 module 根的
// proto/ 下找到，且那份文件声明的 package 与 go_package 就是本包。两者只有在生成物
// 确实由**这个 module 里的**这份 .proto 产出时才同时成立。
func TestSchemaShipsWithTheModuleItGenerates(t *testing.T) {
	t.Parallel()

	// 本包在 module 根下一层，测试的 cwd 就是包目录。
	moduleRoot := ".."

	descriptorPath := agentrewire.File_agentre_wire_wire_proto.Path()
	schemaPath := filepath.Join(moduleRoot, "proto", filepath.FromSlash(descriptorPath))
	content, err := os.ReadFile(schemaPath) //nolint:gosec // 路径来自编译进来的 descriptor，不是外部输入。
	if err != nil {
		t.Fatalf("descriptor 记录的 %q 在本 module 的 proto/ 下找不到：%v", descriptorPath, err)
	}

	schema := string(content)
	for _, want := range []string{
		"package agentre.wire;",
		`option go_package = "github.com/agentre-hub/agentre/pkg/wire/agentrewire;agentrewire";`,
	} {
		if !strings.Contains(schema, want) {
			t.Errorf("%s 缺少 %q", schemaPath, want)
		}
	}

	// buf 的模块根由 buf.yaml 的位置决定；配置与基线跟 schema 同住，`buf lint` /
	// `buf breaking` / `buf generate` 才是在协议主人这里跑的。
	for _, companion := range []string{"buf.yaml", "buf.gen.yaml", "proto/baseline.binpb"} {
		if _, err := os.Stat(filepath.Join(moduleRoot, filepath.FromSlash(companion))); err != nil {
			t.Errorf("%s 不在 module 根下：%v", companion, err)
		}
	}
}
