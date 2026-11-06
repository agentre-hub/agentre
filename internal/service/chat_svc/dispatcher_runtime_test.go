package chat_svc

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
)

// checkpointStoreFake 是轮内 checkpoint 的仓储替身:把交来的块行写进内存里的「块表」,
// 记下每次交来的行数。只实现 checkpoint 用到的方法,其余方法调到就是 nil 接口 panic ——
// checkpoint 不该碰它们。
type checkpointStoreFake struct {
	transcript_repo.MessageRepo
	rows         map[int]*transcript_entity.MessageBlock
	upsertCalls  int
	lastUpserts  []*transcript_entity.MessageBlock
	lastTruncate int
	legacyCalls  int
}

func newCheckpointStoreFake() *checkpointStoreFake {
	return &checkpointStoreFake{rows: map[int]*transcript_entity.MessageBlock{}}
}

// CheckpointBlocks 是旧的整份差分入口:按真实仓储的做法解两份正文求差分再落。
func (f *checkpointStoreFake) CheckpointBlocks(_ context.Context, m *transcript_entity.Message, prev string) error {
	f.legacyCalls++
	diff, err := transcript_entity.DiffBlocks(m.ID, prev, m.BlocksJSON)
	if err != nil {
		return err
	}
	f.apply(diff.Upserts, diff.TruncateFrom)
	return nil
}

func (f *checkpointStoreFake) UpsertBlocks(
	_ context.Context, _ *transcript_entity.Message, rows []*transcript_entity.MessageBlock, truncateFrom int,
) error {
	f.upsertCalls++
	f.lastUpserts = rows
	f.lastTruncate = truncateFrom
	f.apply(rows, truncateFrom)
	return nil
}

func (f *checkpointStoreFake) apply(rows []*transcript_entity.MessageBlock, truncateFrom int) {
	if truncateFrom >= 0 {
		for idx := range f.rows {
			if idx >= truncateFrom {
				delete(f.rows, idx)
			}
		}
	}
	for _, r := range rows {
		f.rows[r.Idx] = r
	}
}

func (f *checkpointStoreFake) sortedRows() []*transcript_entity.MessageBlock {
	out := make([]*transcript_entity.MessageBlock, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Idx < out[j].Idx })
	return out
}

func useCheckpointStore(t testing.TB, f *checkpointStoreFake) {
	prev := transcript_repo.Message()
	transcript_repo.RegisterMessage(f)
	t.Cleanup(func() { transcript_repo.RegisterMessage(prev) })
}

// fullSetBlocksJSON 是改动前 checkpoint 的编码:整份 Snapshot 走 SetBlocks。
func fullSetBlocksJSON(t testing.TB, acc *turn.Accumulator) string {
	m := &chat_entity.Message{}
	if err := m.SetBlocks(acc.Snapshot()); err != nil {
		t.Fatalf("SetBlocks: %v", err)
	}
	return m.BlocksJSON
}

// countingBlock 每被编码一次就记一次数。
type countingBlock struct {
	Text  string `json:"text"`
	count *atomic.Int64
}

func (countingBlock) Type() string                      { return "counting" }
func (countingBlock) Audience() cagoblocks.AudienceMask { return cagoblocks.ToAll }

func (b *countingBlock) MarshalJSON() ([]byte, error) {
	b.count.Add(1)
	return json.Marshal(struct {
		Text string `json:"text"`
	}{b.Text})
}

// TestCheckpointAssistant_AppendEncodesOnlyTheNewBlock 钉住轮内 checkpoint 的二次方:
// 每个 ToolResult 一次的 checkpoint 曾把整条消息的全部块重新编码,一轮 O(N²)。
// 1000 块之上再追加一块,这次 checkpoint 只能编码常数个块、只把常数个块行交给仓储。
func TestCheckpointAssistant_AppendEncodesOnlyTheNewBlock(t *testing.T) {
	Convey("1000 块的消息追加一块后 checkpoint,编码与写入的块数与 N 无关", t, func() {
		store := newCheckpointStoreFake()
		useCheckpointStore(t, store)
		var encodes atomic.Int64
		acc := turn.New()
		for i := range 1000 {
			acc.AddToolResult(&countingBlock{Text: fmt.Sprintf("block-%d", i), count: &encodes})
		}
		msg := &chat_entity.Message{ID: 7, SessionID: 1, Role: "assistant", BlocksJSON: "[]"}
		svc := &chatSvc{emitter: NoopEmitter{}}
		svc.checkpointAssistantNew(context.Background(), msg, acc)

		encodes.Store(0)
		acc.AddToolResult(&countingBlock{Text: "appended", count: &encodes})
		svc.checkpointAssistantNew(context.Background(), msg, acc)

		So(encodes.Load(), ShouldBeLessThanOrEqualTo, 2)
		So(store.legacyCalls, ShouldEqual, 0)
		So(store.upsertCalls, ShouldEqual, 2)
		So(len(store.lastUpserts), ShouldBeLessThanOrEqualTo, 2)
		So(store.lastUpserts, ShouldHaveLength, 1)
		So(store.lastUpserts[0].Idx, ShouldEqual, 1000)
		So(msg.BlocksJSON, ShouldEqual, fullSetBlocksJSON(t, acc))
	})
}

// TestCheckpointAssistant_IncrementalMatchesFullPath 是硬不变量的守卫:一段混合脚本
// (正文、工具调用、工具结果、就地修补更早的块、思考、更多正文、思考插到在长的正文
// 前面、审批卡就地改终态、子代理卡改认领)里每一次 checkpoint 之后,
//   - 交给仓储的写集合 == 改动前整份差分(DiffBlocks)算出的写集合;
//   - 块表 == 整份 SetBlocks 拆出来的块行(逐块一致);
//   - msg.BlocksJSON 与整份 SetBlocks 逐字节相等(对端帧从它投影)。
func TestCheckpointAssistant_IncrementalMatchesFullPath(t *testing.T) {
	Convey("增量 checkpoint 与改动前的整份路径逐块一致", t, func() {
		store := newCheckpointStoreFake()
		useCheckpointStore(t, store)
		acc := turn.New()
		msg := &chat_entity.Message{ID: 9, SessionID: 1, Role: "assistant", BlocksJSON: "[]"}
		svc := &chatSvc{emitter: NoopEmitter{}}
		big := strings.Repeat("compressible tool output ", 400) // > 4 KiB → deflate 行
		approval := &blocks.ToolApprovalBlock{RequestID: "ap-1", ToolName: "bash", Status: "pending"}

		steps := []struct {
			name string
			do   func()
		}{
			{"正文", func() { acc.AddText("hello ") }},
			{"正文继续长", func() { acc.AddText("world") }},
			{"工具调用", func() {
				acc.AddToolUse(&cagoblocks.ToolUseBlock{ID: "tu-1", Name: "bash", Input: map[string]any{"cmd": "ls"}}, "tool_use:tu-1")
			}},
			{"工具结果(压缩)", func() {
				acc.AddToolResult(&cagoblocks.ToolResultBlock{ToolUseID: "tu-1", Content: []cagoblocks.ContentBlock{cagoblocks.TextBlock{Text: big}}})
			}},
			{"提问卡", func() { acc.AddBlock(&blocks.UserAskBlock{RequestID: "r-1"}, "user_ask:r-1") }},
			{"子代理卡", func() {
				acc.AddBlock(&blocks.SubagentStateBlock{ParentToolCallID: "tc-9", TaskID: "task-9", Status: "running"}, "subagent_state:tc-9")
			}},
			{"就地修补更早的提问卡", func() {
				turn.Mutate[blocks.UserAskBlock](acc, "user_ask:r-1", func(b *blocks.UserAskBlock) { b.Answered = true })
			}},
			{"思考", func() { acc.AddThinking("pondering") }},
			{"更多正文", func() { acc.AddText("more text") }},
			{"思考插到在长的正文前面", func() { acc.AddThinking(" deeper") }},
			{"审批卡", func() { acc.AddBlock(approval, "") }},
			{"子代理卡改认领并更新", func() {
				turn.AdoptMutateKey(acc, "subagent_state:tc-10",
					func(b *blocks.SubagentStateBlock) bool { return b.TaskID == "task-9" },
					func(b *blocks.SubagentStateBlock) { b.Status = "completed" })
			}},
			{"审批卡就地改终态", func() {
				acc.MutateBlock(approval, func() { approval.Status, approval.Result = "approved", "ok" })
			}},
			{"无变化", func() {}},
			{"尾巴正文", func() { acc.AddText("tail") }},
		}

		prevJSON := "[]"
		for _, step := range steps {
			step.do()
			wantJSON := fullSetBlocksJSON(t, acc)
			wantDiff, err := transcript_entity.DiffBlocks(msg.ID, prevJSON, wantJSON)
			So(err, ShouldBeNil)
			wantRows, err := transcript_entity.SplitBlocksJSON(msg.ID, wantJSON)
			So(err, ShouldBeNil)

			store.lastUpserts, store.lastTruncate = nil, -2
			svc.checkpointAssistantNew(context.Background(), msg, acc)

			So(store.legacyCalls, ShouldEqual, 0)
			So(msg.BlocksJSON, ShouldEqual, wantJSON)
			So(store.lastUpserts, ShouldResemble, wantDiff.Upserts)
			So(store.lastTruncate, ShouldEqual, wantDiff.TruncateFrom)
			So(store.sortedRows(), ShouldResemble, wantRows)
			prevJSON = wantJSON
		}
	})
}

// BenchmarkCheckpointAssistant_AppendOne 量「N 块消息上追加一块后 checkpoint」的单次代价。
// 每轮迭代在 N 块的累加器上追加一块再 checkpoint;为了让 N 不随 b.N 漂移,每 N/10 次
// 迭代(计时外)重建一次 N 块的起点。
//
// 自报两个指标:rows/op 是交给仓储的块行数;body-B/op 是 msg.BlocksJSON 的字节数 ——
// 正文要整份交出(对端帧从它投影),拼它的那一次分配必然随消息大小线性增长,
// B/op − body-B/op 才是编码与写集合的代价,它不随 N 增长。
func BenchmarkCheckpointAssistant_AppendOne(b *testing.B) {
	payload := strings.Repeat("x", 600)
	for _, n := range []int{100, 1000, 5000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			store := newCheckpointStoreFake()
			useCheckpointStore(b, store)
			svc := &chatSvc{emitter: NoopEmitter{}}
			ctx := context.Background()
			var acc *turn.Accumulator
			var msg *chat_entity.Message
			reset := func() {
				acc = turn.New()
				for i := range n {
					acc.AddToolResult(&cagoblocks.ToolResultBlock{ToolUseID: fmt.Sprintf("tu-%d", i), Content: []cagoblocks.ContentBlock{cagoblocks.TextBlock{Text: payload}}})
				}
				msg = &chat_entity.Message{ID: 1, SessionID: 1, Role: "assistant", BlocksJSON: "[]"}
				store.rows = map[int]*transcript_entity.MessageBlock{}
				svc.checkpointAssistantNew(ctx, msg, acc)
			}
			resetEvery := max(n/10, 1)
			var rows, bodyBytes int
			reset()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if i > 0 && i%resetEvery == 0 {
					b.StopTimer()
					reset()
					b.StartTimer()
				}
				acc.AddToolResult(&cagoblocks.ToolResultBlock{ToolUseID: "tu-new", Content: []cagoblocks.ContentBlock{cagoblocks.TextBlock{Text: payload}}})
				store.lastUpserts = nil
				svc.checkpointAssistantNew(ctx, msg, acc)
				rows += len(store.lastUpserts)
				bodyBytes += len(msg.BlocksJSON)
			}
			b.ReportMetric(float64(rows)/float64(b.N), "rows/op")
			b.ReportMetric(float64(bodyBytes)/float64(b.N), "body-B/op")
		})
	}
}
