package transcript_repo_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// guardedDeclarations 是块拆分与 CheckpointBlocks 差分在本仓唯一被允许出现声明的
// 函数签名（复用边界，决策 8："若某处出现第二份块拆分、第二份 checkpoint 差分或第二份
// 投影，即视为本轮的回归"）。两个宿主共用同一份 transcript_entity 实现，任何地方
// 重新声明同名函数都意味着有人在别处另起了一份，而不是调用这一份。
//
// 先例：internal/guard/wire_single_source_test.go 守住 wire 生成代码只有一个 import
// path；这里守的是同一类风险（合法编译、静默漂移），只是对象换成了函数声明而不是
// import path。
var guardedDeclarations = []string{
	"func SplitBlocksJSON(",
	"func DiffBlocks(",
}

// guardedLedgerDeclarations 是「把一串持久帧映到编号上」在本仓唯一被允许出现声明的
// 函数签名。它与上面那组守的是同一类风险，只是 canonical 文件不同：编号的分配、
// 沿用与惰性补齐是决策 3 的全部内容，两个宿主抄成两份就等于让两台机器各自决定
// 「第几号是什么」。
var guardedLedgerDeclarations = []string{
	"func NumberFrames(",
	"func PredictLatestSeq(",
}

// TestBlockSplittingAndCheckpointDiffingHaveOneImplementation 判红条件:仓内除
// transcript_entity/message_block.go 外的任何 .go 文件出现上述声明之一（转发/别名
// 调用 SplitBlocksJSON(...) 不算声明,不触发)。
func TestBlockSplittingAndCheckpointDiffingHaveOneImplementation(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	scanned := 0
	found := make(map[string][]string, len(guardedDeclarations)+len(guardedLedgerDeclarations))

	all := append(append([]string{}, guardedDeclarations...), guardedLedgerDeclarations...)

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// 本文件自己的 guardedDeclarations 字面量会把自己算作一处"声明"，排除掉——
		// 与 wire_single_source_test.go 对自身的处理一致。
		if rel == "internal/repository/transcript_repo/duplicate_guard_test.go" {
			return nil
		}
		// path 由上面的 WalkDir 从仓库根枚举，只读取后缀为 .go 的受控源码。
		content, err := os.ReadFile(path) //nolint:gosec // 测试守卫需要检查仓库内枚举出的 Go 源码。
		if err != nil {
			return err
		}
		scanned++
		for _, decl := range all {
			if strings.Contains(string(content), decl) {
				found[decl] = append(found[decl], rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// 自证不空过：读不到任何 Go 源码时全绿是没有意义的。
	if scanned == 0 {
		t.Fatalf("walked %s but found no Go sources; the guard would pass vacuously", root)
	}

	const canonicalFile = "internal/model/entity/transcript_entity/message_block.go"
	const canonicalLedgerFile = "internal/repository/transcript_repo/frame_seq.go"
	for _, decl := range guardedDeclarations {
		locations := found[decl]
		if len(locations) != 1 || locations[0] != canonicalFile {
			t.Errorf("expected %q declared exactly once in %s, found in %v", decl, canonicalFile, locations)
		}
	}
	for _, decl := range guardedLedgerDeclarations {
		locations := found[decl]
		if len(locations) != 1 || locations[0] != canonicalLedgerFile {
			t.Errorf("expected %q declared exactly once in %s, found in %v", decl, canonicalLedgerFile, locations)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// internal/repository/transcript_repo -> 仓库根需要向上三级。
	return filepath.Clean(filepath.Join(wd, "..", "..", ".."))
}

// chatRepoForbiddenDeclarations 是「消息 / 块的仓储」在 chat_repo 里一旦重新出现就
// 判红的声明。决策 8 把它们整体搬进 transcript_repo,chat_repo 只留会话 ——
// 规格 Testing decisions 的守卫写的就是这一条:「chat_repo 不再持有消息与块」。
//
// 守访问器三件套与三个类型名,而不是「文件里出现 message 这个词」:
// replacement_recovery.go 会按 session_id 搬动 chat_messages 的归属(会话替换的崩溃
// 恢复,改的是行属于哪条会话),那是会话域的事,不该被这条守卫误伤。
var chatRepoForbiddenDeclarations = []string{
	"func Message()",
	"func RegisterMessage(",
	"func NewMessage(",
	"type MessageRepo",
	"type MessageUsage",
	"type SubagentProgress",
}

// TestChatRepoHoldsSessionsOnly 判红条件:internal/repository/chat_repo 下任何 .go
// 文件重新声明消息 / 块的仓储入口。
func TestChatRepoHoldsSessionsOnly(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(repositoryRoot(t), "internal", "repository", "chat_repo")
	scanned := 0
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		// path 由上面的 WalkDir 从 chat_repo 目录枚举，只读取后缀为 .go 的受控源码。
		content, err := os.ReadFile(path) //nolint:gosec // 测试守卫需要检查仓库内枚举出的 Go 源码。
		if err != nil {
			return err
		}
		scanned++
		for _, decl := range chatRepoForbiddenDeclarations {
			if strings.Contains(string(content), decl) {
				t.Errorf("chat_repo 只留会话:%s 里出现了 %q，消息与块归 transcript_repo", path, decl)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// 自证不空过：目录读空时全绿是没有意义的。
	if scanned == 0 {
		t.Fatalf("walked %s but found no Go sources; the guard would pass vacuously", dir)
	}
}
