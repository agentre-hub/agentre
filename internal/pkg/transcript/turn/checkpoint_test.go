package turn

import (
	"encoding/json"
	"sync"
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
)

// fullBlocksJSON 是整份编码的口径(Message.SetBlocks 的做法):增量 checkpoint 交出的
// BlocksJSON 必须与它逐字节相等。
func fullBlocksJSON(acc *Accumulator) string {
	stored, err := cagoblocks.EncodeAll(acc.Snapshot())
	So(err, ShouldBeNil)
	buf, err := json.Marshal(stored)
	So(err, ShouldBeNil)
	return string(buf)
}

func changedIdx(cp *Checkpoint) []int {
	out := make([]int, 0, len(cp.Changed))
	for _, c := range cp.Changed {
		out = append(out, c.Idx)
	}
	return out
}

// checkpointOnce 准备并提交一次 checkpoint,断言 BlocksJSON 与整份编码一致。
func checkpointOnce(acc *Accumulator, baseline string) *Checkpoint {
	cp, err := acc.PrepareCheckpoint(baseline)
	So(err, ShouldBeNil)
	So(cp.BlocksJSON, ShouldEqual, fullBlocksJSON(acc))
	acc.CommitCheckpoint(cp)
	return cp
}

func TestAccumulator_CheckpointEncodesOnlyNewAndDirtyBlocks(t *testing.T) {
	Convey("checkpoint 只交出新增与被改动的块", t, func() {
		acc := New()

		Convey("空累加器交出 [] 且没有变化的块", func() {
			cp := checkpointOnce(acc, "[]")
			So(cp.BlocksJSON, ShouldEqual, "[]")
			So(cp.Changed, ShouldBeEmpty)
			So(cp.TruncateFrom, ShouldEqual, -1)
		})

		acc.AddToolUse(&cagoblocks.ToolUseBlock{ID: "tu-1", Name: "bash"}, "tool_use:tu-1")
		acc.AddBlock(&blocks.UserAskBlock{RequestID: "r-1"}, "user_ask:r-1")
		approval := &blocks.ToolApprovalBlock{RequestID: "ap-1", Status: "pending"}
		acc.AddBlock(approval, "")
		acc.AddBlock(&blocks.SubagentStateBlock{ParentToolCallID: "tc-1", TaskID: "task-1", Status: "running"}, "subagent_state:tc-1")
		first := checkpointOnce(acc, "[]")
		So(changedIdx(first), ShouldResemble, []int{0, 1, 2, 3})
		So(first.Changed[0].Block.Type, ShouldEqual, "tool_use")
		So(string(first.Changed[0].Block.Data), ShouldEqual, `{"id":"tu-1","name":"bash"}`)

		Convey("追加一块只交出这一块", func() {
			acc.AddToolResult(&cagoblocks.ToolResultBlock{ToolUseID: "tu-1"})
			So(changedIdx(checkpointOnce(acc, "")), ShouldResemble, []int{4})
		})

		Convey("Mutate 更早的块只交出那一块", func() {
			Mutate[blocks.UserAskBlock](acc, "user_ask:r-1", func(b *blocks.UserAskBlock) { b.Answered = true })
			So(changedIdx(checkpointOnce(acc, "")), ShouldResemble, []int{1})
		})

		Convey("AdoptMutateKey 改到的块被交出", func() {
			hit := AdoptMutateKey(acc, "subagent_state:tc-2",
				func(b *blocks.SubagentStateBlock) bool { return b.TaskID == "task-1" },
				func(b *blocks.SubagentStateBlock) { b.Status = "completed" })
			So(hit, ShouldBeTrue)
			So(changedIdx(checkpointOnce(acc, "")), ShouldResemble, []int{3})
		})

		Convey("MutateBlock 按指针改到的块被交出", func() {
			So(acc.MutateBlock(approval, func() { approval.Status = "approved" }), ShouldBeTrue)
			So(changedIdx(checkpointOnce(acc, "")), ShouldResemble, []int{2})
		})

		Convey("MutateBlock 不认识的块不调 fn、交回 false", func() {
			called := false
			So(acc.MutateBlock(&blocks.ToolApprovalBlock{}, func() { called = true }), ShouldBeFalse)
			So(called, ShouldBeFalse)
		})

		Convey("被标脏但内容没变的块不交出", func() {
			Mutate[blocks.UserAskBlock](acc, "user_ask:r-1", func(*blocks.UserAskBlock) {})
			So(checkpointOnce(acc, "").Changed, ShouldBeEmpty)
		})

		Convey("没有任何变化时复用上一份正文", func() {
			cp := checkpointOnce(acc, "")
			So(cp.Changed, ShouldBeEmpty)
			So(cp.BlocksJSON, ShouldEqual, first.BlocksJSON)
		})
	})
}

func TestAccumulator_CheckpointTracksTheGrowingTail(t *testing.T) {
	Convey("还在长的正文 / 思考尾巴每次都重新比较", t, func() {
		acc := New()
		acc.AddToolUse(&cagoblocks.ToolUseBlock{ID: "tu-1"}, "tool_use:tu-1")
		acc.AddText("hel")
		So(changedIdx(checkpointOnce(acc, "[]")), ShouldResemble, []int{0, 1})

		acc.AddText("lo")
		So(changedIdx(checkpointOnce(acc, "")), ShouldResemble, []int{1})

		Convey("思考插到正文前面:两个位置都变", func() {
			acc.AddThinking("hmm")
			So(changedIdx(checkpointOnce(acc, "")), ShouldResemble, []int{1, 2})
		})

		Convey("尾巴落成块、内容不变时不交出,只交出新块", func() {
			acc.AddBlock(&blocks.UserAskBlock{RequestID: "r-1"}, "user_ask:r-1")
			So(changedIdx(checkpointOnce(acc, "")), ShouldResemble, []int{2})
		})
	})
}

func TestAccumulator_CheckpointCommitSemantics(t *testing.T) {
	Convey("只有提交过的 checkpoint 才算落库", t, func() {
		acc := New()
		acc.AddToolUse(&cagoblocks.ToolUseBlock{ID: "tu-1"}, "tool_use:tu-1")
		checkpointOnce(acc, "[]")

		Convey("没提交(写库失败)时下一次照样交出这些块", func() {
			acc.AddToolResult(&cagoblocks.ToolResultBlock{ToolUseID: "tu-1"})
			cp, err := acc.PrepareCheckpoint("")
			So(err, ShouldBeNil)
			So(changedIdx(cp), ShouldResemble, []int{1})
			acc.AddToolResult(&cagoblocks.ToolResultBlock{ToolUseID: "tu-2"})
			So(changedIdx(checkpointOnce(acc, "")), ShouldResemble, []int{1, 2})
		})

		Convey("准备与提交之间被改动的块下一次仍交出", func() {
			acc.AddBlock(&blocks.UserAskBlock{RequestID: "r-1"}, "user_ask:r-1")
			cp, err := acc.PrepareCheckpoint("")
			So(err, ShouldBeNil)
			Mutate[blocks.UserAskBlock](acc, "user_ask:r-1", func(b *blocks.UserAskBlock) { b.Skipped = true })
			acc.CommitCheckpoint(cp)
			So(changedIdx(checkpointOnce(acc, "")), ShouldResemble, []int{1})
		})

		Convey("过期的提交(中间已有更新的提交)被忽略", func() {
			acc.AddToolResult(&cagoblocks.ToolResultBlock{ToolUseID: "tu-1"})
			stale, err := acc.PrepareCheckpoint("")
			So(err, ShouldBeNil)
			checkpointOnce(acc, "")
			acc.AddToolResult(&cagoblocks.ToolResultBlock{ToolUseID: "tu-2"})
			acc.CommitCheckpoint(stale)
			So(changedIdx(checkpointOnce(acc, "")), ShouldResemble, []int{2})
		})
	})
}

func TestAccumulator_CheckpointBaseline(t *testing.T) {
	Convey("第一次 checkpoint 以消息已有的正文为基准", t, func() {
		acc := New()
		acc.AddText("a")
		cp, err := acc.PrepareCheckpoint(
			`[{"type":"text","data":{"text":"a"}},{"type":"text","data":{"text":"stale"}}]`)
		So(err, ShouldBeNil)
		So(cp.Changed, ShouldBeEmpty)
		So(cp.TruncateFrom, ShouldEqual, 1)
		So(cp.BlocksJSON, ShouldEqual, fullBlocksJSON(acc))
		acc.CommitCheckpoint(cp)

		Convey("之后的 checkpoint 不再看基准", func() {
			acc.AddText("b")
			cp := checkpointOnce(acc, `[{"type":"text","data":{"text":"ignored"}}]`)
			So(changedIdx(cp), ShouldResemble, []int{0})
			So(cp.TruncateFrom, ShouldEqual, -1)
		})

		Convey("读不懂的基准报错", func() {
			_, err := New().PrepareCheckpoint("{")
			So(err, ShouldNotBeNil)
		})
	})
}

func TestAccumulator_CheckpointConcurrentWithMutations(t *testing.T) {
	Convey("checkpoint 与并发改动同跑不竞争(-race),最后一次结果与整份编码一致", t, func() {
		acc := New()
		acc.AddBlock(&blocks.UserAskBlock{RequestID: "r-1"}, "user_ask:r-1")
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				Mutate[blocks.UserAskBlock](acc, "user_ask:r-1", func(b *blocks.UserAskBlock) { b.Answered = !b.Answered })
				acc.AddText("x")
				_ = acc.Snapshot()
			}
		}()
		for range 50 {
			cp, err := acc.PrepareCheckpoint("[]")
			So(err, ShouldBeNil)
			acc.CommitCheckpoint(cp)
		}
		wg.Wait()
		checkpointOnce(acc, "")
	})
}
