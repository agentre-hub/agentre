package transcript_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// guardedDeclarations 是「事件 → 块」的累积与「块 → 帧」的投影在本仓唯一被允许出现
// 声明的函数签名（复用边界，决策 2：「同一段内容在两个宿主上必须由同一行代码写进库、
// 由同一行代码折成帧；若某处出现第二份块拆分、第二份 checkpoint 差分或第二份投影，
// 即视为回归」）。
//
// 两个宿主（桌面端 chat_svc / agentred）共用 internal/pkg/transcript 这一份，任何地方
// 重新声明同名函数都意味着有人在别处另起了一份，而不是调用这一份 —— 而漏同步的表现
// 是转录静默少一张卡，编译期没有任何东西会报错。
//
// 先例：internal/repository/transcript_repo/duplicate_guard_test.go 守块拆分与
// checkpoint 差分；internal/guard/wire_single_source_test.go 守 wire 生成代码只有一个
// import path。守的是同一类风险（合法编译、静默漂移）。
var guardedDeclarations = map[string]string{
	// 累积：块怎么攒出来、thinking 怎么穿插、buf 什么时候落块。
	"func (a *Accumulator) AddBlock(": "internal/pkg/transcript/turn/accumulator.go",
	"func (a *Accumulator) Finalize(": "internal/pkg/transcript/turn/accumulator.go",
	// 事件类型 → handler 的注册表：第二份注册表等于第二份「哪种事件落哪种块」。
	// 守到注册动作本身而不只是构造函数名 —— 换个名字重抄一张表同样判红。
	"func NewTurnDispatcher(": "internal/pkg/transcript/dispatcher.go",
	"d.Register((*agentruntime.ThinkingDelta)(nil), handlers.ThinkingDeltaHandler{})": "internal/pkg/transcript/dispatcher.go",
	// 投影：块怎么折成持久帧，以及认不出的块怎么兜底。同样守到实现内部的块类型
	// 分支，另起一份投影（哪怕换了函数名）也会带上这一行。
	"func ProjectMessages(":            "internal/pkg/transcript/projection.go",
	"func EventForStoredBlock(":        "internal/pkg/transcript/projection.go",
	"case \"permission_mode_change\":": "internal/pkg/transcript/projection.go",
	// 帧的**位置**(哪条消息的哪个块的第几帧)：编号挂靠的就是它，两个宿主必须由
	// 同一行代码算出来。第二份位置计算 = 同一段内容在两台机器上拿到两套编号，而
	// 那正是规格 2026-09-05 问题 B 要消灭的东西。
	"func ProjectKeyedMessage(":  "internal/pkg/transcript/keyed.go",
	"func ProjectKeyedMessages(": "internal/pkg/transcript/keyed.go",
	// 单块投影必须清掉消息级派生的那几格、并砍掉尾部的派生帧；抄一份这两个私有
	// 助手就是抄了一份位置计算。
	"func clearMessageDerivedFields(":   "internal/pkg/transcript/keyed.go",
	"func messageDerivedFrameCount(":    "internal/pkg/transcript/keyed.go",
	"const MessageDerivedBlockIdx = -1": "internal/pkg/transcript/keyed.go",
	// 「此刻该发哪些帧」：轮内哪些帧还不该发（结尾还会继续长的正文块 / 消息级派生
	// 帧）、哪一次原地修补要重发。桌面端的对端发布与 agentred 的实时发布共用一份 ——
	// 抄第二份就等于让两台机器在不同的时刻给同一段内容取号。
	"func (p *FramePublisher) Pending(": "internal/pkg/transcript/publish.go",
	"func SettledFrames(":               "internal/pkg/transcript/publish.go",
	"func isGrowingTextBlock(":          "internal/pkg/transcript/publish.go",
}

// TestAccumulationAndProjectionHaveOneImplementation 判红条件：仓内除各自的 canonical
// 文件外，任何 .go 文件出现上述声明之一（转发/别名调用 ProjectMessages(...) 不算声明，
// 不触发）。
func TestAccumulationAndProjectionHaveOneImplementation(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	scanned := 0
	found := make(map[string][]string, len(guardedDeclarations))

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
		// 本文件自己的 guardedDeclarations 字面量会把自己算作一处「声明」，排除掉 ——
		// 与 transcript_repo/duplicate_guard_test.go 对自身的处理一致。
		if rel == filepath.FromSlash("internal/pkg/transcript/duplicate_guard_test.go") {
			return nil
		}
		// path 由上面的 WalkDir 从仓库根枚举，只读取后缀为 .go 的受控源码。
		content, err := os.ReadFile(path) //nolint:gosec // 测试守卫需要检查仓库内枚举出的 Go 源码。
		if err != nil {
			return err
		}
		scanned++
		for decl := range guardedDeclarations {
			if strings.Contains(string(content), decl) {
				found[decl] = append(found[decl], filepath.ToSlash(rel))
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

	for decl, canonicalFile := range guardedDeclarations {
		locations := found[decl]
		if len(locations) != 1 || locations[0] != canonicalFile {
			t.Errorf("expected %q declared exactly once in %s, found in %v", decl, canonicalFile, locations)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// internal/pkg/transcript -> 仓库根需要向上三级。
	return filepath.Clean(filepath.Join(wd, "..", "..", ".."))
}
