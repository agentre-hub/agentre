package piagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
)

const (
	fixtureSessionID = "22223333-4444-5555-6666-777788889999"
	fixtureSlug      = "-tmp-fixture-pi-plot"
)

// eventNames 把事件流压成可读的类型序列,断言整轮形状而不是抽查其中一条。做法照抄
// claudecode/transcript_test.go,两个读取器的回放形状可比对但字段各按自己的方言来。
func eventNames(evs []agentruntime.Event) []string {
	out := make([]string, 0, len(evs))
	for _, ev := range evs {
		switch e := ev.(type) {
		case agentruntime.TextDelta:
			out = append(out, "text:"+e.Text)
		case agentruntime.ThinkingDelta:
			out = append(out, "thinking:"+e.Text)
		case agentruntime.ToolCall:
			out = append(out, fmt.Sprintf("toolCall:%s:%s:%s", e.Name, e.ID, e.ParentToolCallID))
		case agentruntime.ToolResult:
			out = append(out, fmt.Sprintf("toolResult:%s:%s", e.ToolCallID, e.ParentToolCallID))
		case agentruntime.UsageUpdate:
			out = append(out, "usage")
		case agentruntime.SubagentStarted:
			out = append(out, "subagentStarted:"+e.ToolCallID)
		case agentruntime.SubagentProgress:
			out = append(out, "subagentProgress:"+e.ToolCallID)
		case agentruntime.SubagentDone:
			out = append(out, "subagentDone:"+e.ToolCallID)
		case agentruntime.CompactBoundary:
			out = append(out, fmt.Sprintf("compact:%d:%d:%s", e.PreTokens, e.PostTokens, e.Trigger))
		default:
			out = append(out, fmt.Sprintf("%T", ev))
		}
	}
	return out
}

func collectTurns(t *testing.T, tr transcriptimport.Transcript) []transcriptimport.Turn {
	t.Helper()
	var turns []transcriptimport.Turn
	err := tr.Turns(context.Background(), func(turn transcriptimport.Turn) error {
		turns = append(turns, turn)
		return nil
	})
	So(err, ShouldBeNil)
	return turns
}

func gapCount(m transcriptimport.Meta, kind transcriptimport.GapKind) int {
	for _, g := range m.Gaps {
		if g.Kind == kind {
			return g.Count
		}
	}
	return 0
}

// TestTranscriptSource_ScanOpenTurns 用一份真实方言的 pi session JSONL 切片(虚构内容)
// 走完 Scan → Open → Turns:明文思维、子代理内容取自 details.messages、未闭合工具调用
// 的缺口声明,以及 pi 磁盘上不存在来源标记这四件事都在这一条转录里覆盖到。
func TestTranscriptSource_ScanOpenTurns(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("testdata", "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTRE_PI_SESSIONS_DIR", root)
	ctx := context.Background()

	Convey("Given 注册表里的 pi 读取器", t, func() {
		src := transcriptimport.SourceFor(agent_backend_entity.TypePiAgent)
		So(src, ShouldNotBeNil)

		Convey("When 扫描, Then 只出元信息候选,且不标来源", func() {
			cands, err := src.Scan(ctx, transcriptimport.Filter{})
			So(err, ShouldBeNil)
			So(len(cands), ShouldEqual, 1)
			c := cands[0]
			So(c.Backend, ShouldEqual, agent_backend_entity.TypePiAgent)
			So(c.ProviderSessionID, ShouldEqual, fixtureSessionID)
			So(c.Cwd, ShouldEqual, "/tmp/fixture-pi-plot")
			So(c.Title, ShouldEqual, "给苗圃拟一份浇水时间表")
			// pi 磁盘上没有任何来源字段,不猜、不标 terminal/agentre。
			So(c.Origin, ShouldEqual, transcriptimport.OriginUnknown)
			So(string(c.Locator), ShouldNotBeBlank)
			So(c.StartedAt.UTC().Format("2006-01-02T15:04:05Z"), ShouldEqual, "2026-08-26T02:00:00Z")
			So(c.EndedAt.UTC().Format("2006-01-02T15:04:05Z"), ShouldEqual, "2026-08-26T02:05:06Z")
		})

		Convey("When 过滤条件不匹配, Then 候选被排除", func() {
			cands, err := src.Scan(ctx, transcriptimport.Filter{CwdPrefix: "/nowhere"})
			So(err, ShouldBeNil)
			So(len(cands), ShouldEqual, 0)
		})

		Convey("When 打开, Then 元信息与缺口声明齐全", func() {
			cands, err := src.Scan(ctx, transcriptimport.Filter{})
			So(err, ShouldBeNil)
			tr, err := src.Open(ctx, cands[0].Locator)
			So(err, ShouldBeNil)
			defer func() { _ = tr.Close() }()

			m := tr.Meta()
			So(m.ProviderSessionID, ShouldEqual, fixtureSessionID)
			So(m.Cwd, ShouldEqual, "/tmp/fixture-pi-plot")
			So(m.Model, ShouldEqual, "glm-5.3")
			So(m.Turns, ShouldEqual, 2)
			So(m.ToolCalls, ShouldEqual, 3)
			So(m.Compactions, ShouldEqual, 1)
			So(m.Origin, ShouldEqual, transcriptimport.OriginUnknown)
			// 一条空思维块(a2)、一行坏数据、一次未闭合的工具调用(tc-read-orphan)。
			So(gapCount(m, transcriptimport.GapThinkingUnavailable), ShouldEqual, 1)
			So(gapCount(m, transcriptimport.GapUnparsableRecords), ShouldEqual, 1)
			So(gapCount(m, transcriptimport.GapUnclosedToolCall), ShouldEqual, 1)
			// pi 的子代理内部过程内联在 details.messages 里,结构上不存在"文件缺失"
			// 这种缺口,不得替它编一个。
			So(gapCount(m, transcriptimport.GapSubagentInternals), ShouldEqual, 0)
		})

		Convey("When 回放, Then 明文思维、子代理内容与未闭合工具调用都在正确的位置上", func() {
			cands, err := src.Scan(ctx, transcriptimport.Filter{})
			So(err, ShouldBeNil)
			tr, err := src.Open(ctx, cands[0].Locator)
			So(err, ShouldBeNil)
			defer func() { _ = tr.Close() }()

			turns := collectTurns(t, tr)
			So(len(turns), ShouldEqual, 2)

			t1 := turns[0]
			So(t1.UserText, ShouldEqual, "给苗圃拟一份浇水时间表")
			So(t1.ForkAnchor, ShouldBeBlank)
			So(t1.Model, ShouldEqual, "glm-5.3")
			So(t1.ErrorText, ShouldBeBlank)
			So(t1.Usage, ShouldNotBeNil)
			So(t1.Usage.PromptTokens, ShouldEqual, 50)
			So(t1.Usage.CompletionTokens, ShouldEqual, 25)
			So(t1.Usage.TotalTokens, ShouldEqual, 75)

			innerReadID := nestedToolCallID("tc-subagent-1", "run-0", "inner-read-1")
			So(eventNames(t1.Events), ShouldResemble, []string{
				// pi 的思维是明文,非空的直接原样回放,不是缺口。
				"thinking:先看看现有的浇水记录文件。",
				"toolCall:bash:tc-bash-1:",
				"usage",
				"toolResult:tc-bash-1:",
				"toolCall:subagent:tc-subagent-1:",
				"subagentStarted:tc-subagent-1",
				"usage",
				// 子代理内容取自 toolResult.details.messages,经既有 tracker 展开。
				"toolCall:read:" + innerReadID + ":tc-subagent-1",
				"toolResult:" + innerReadID + ":tc-subagent-1",
				"subagentProgress:tc-subagent-1",
				"subagentDone:tc-subagent-1",
				"toolResult:tc-subagent-1:",
				"text:浇水时间表整理好了，我再确认一下异常记录的文件。",
				// tc-read-orphan 在全文里没有对应的 toolResult,已经在 Meta.Gaps
				// 里报了未闭合缺口;回放里它仍然是一次正常的工具调用事件。
				"toolCall:read:tc-read-orphan:",
				"usage",
				"compact:0:0:manual",
			})

			// a2 那条空思维块不产出任何事件 —— 缺口已经在 Meta 里声明过一次,
			// 回放里不该再假装有一段看不见的思维文字。
			So(eventNames(t1.Events), ShouldNotContain, "thinking:")

			t2 := turns[1]
			So(t2.UserText, ShouldEqual, "再加一列备注")
			// fork 锚点 = 这一轮 user 之前最近的 assistant 条目 id。
			So(t2.ForkAnchor, ShouldEqual, "a3")
			So(t2.Usage.TotalTokens, ShouldEqual, 40)
			So(eventNames(t2.Events), ShouldResemble, []string{
				"text:已加上备注列。",
				"usage",
			})
		})

		Convey("When 消费方中途返回错误, Then 回放立刻停下并透出该错误", func() {
			cands, err := src.Scan(ctx, transcriptimport.Filter{})
			So(err, ShouldBeNil)
			tr, err := src.Open(ctx, cands[0].Locator)
			So(err, ShouldBeNil)
			defer func() { _ = tr.Close() }()

			stop := fmt.Errorf("stop here")
			var seen int
			err = tr.Turns(ctx, func(transcriptimport.Turn) error {
				seen++
				return stop
			})
			So(errors.Is(err, stop), ShouldBeTrue)
			So(seen, ShouldEqual, 1)
		})

		Convey("When 定位符越出 sessions 根, Then 拒绝打开", func() {
			_, err := src.Open(ctx, transcriptimport.Locator("../../../etc/passwd"))
			So(err, ShouldNotBeNil)
		})
	})
}

// TestTranscriptSource_LargeSubagentDoesNotWedge 钉死回放不会被子代理的内部过程卡死。
//
// 破坏性输入:一次子代理派遣的 details.messages 里有十几条内部消息 —— 本机真实的
// 子代理动辄十几次工具调用,这在磁盘上是常态。既有 tracker 是**同步**往 sink 里推
// 事件的(handleSubagentToolEvent → consumeFinal 的每条内部消息各出一到两条),推的
// 条数没有上限;回放这一侧只能等它推完再收。sink 一旦是个有限容量、又没有并发消费方
// 的通道,推到第 N 条就再也推不动 —— 整条导入永久挂起,ctx 取消都救不回来(阻塞的是
// 通道发送,不是 select)。
//
// 断言写成"限时内跑完"而不是比对事件条数:卡死的表现就是永不返回,而 go test 默认
// 十分钟后才 panic,那时候看到的是一堆无关的 goroutine。
func TestTranscriptSource_LargeSubagentDoesNotWedge(t *testing.T) {
	const innerCalls = 12
	var inner []string
	for i := 0; i < innerCalls; i++ {
		inner = append(inner,
			fmt.Sprintf(`{"role":"assistant","model":"glm-5.3","stopReason":"toolUse","content":[{"type":"toolCall","id":"inner-%d","name":"read","arguments":{"path":"a.csv"}}]}`, i),
			fmt.Sprintf(`{"role":"toolResult","toolCallId":"inner-%d","content":"ok","isError":false}`, i),
		)
	}
	lines := []string{
		`{"type":"session","version":1,"id":"wedge-session","timestamp":"2026-08-26T02:00:00Z","cwd":"/tmp/fixture-pi-wedge"}`,
		`{"type":"message","id":"u1","parentId":"wedge-session","timestamp":"2026-08-26T02:00:05Z","role":"user","content":"跑一个子代理"}`,
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-08-26T02:00:06Z","role":"assistant","model":"glm-5.3","stopReason":"toolUse","content":[{"type":"toolCall","id":"tc-sub","name":"subagent","arguments":{"task":"核对","profile":"data-analyst"}}]}`,
		`{"type":"message","id":"tr1","parentId":"a1","timestamp":"2026-08-26T02:00:40Z","role":"toolResult","toolCallId":"tc-sub","toolName":"subagent","content":"完成","isError":false,` +
			`"details":{"task":"核对","profile":"data-analyst","exitCode":0,"model":"glm-5.3","stopReason":"stop","messages":[` + strings.Join(inner, ",") + `]}}`,
	}

	root := t.TempDir()
	dir := filepath.Join(root, "-tmp-fixture-pi-wedge")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "2026-08-26T02-00-00_wedge.jsonl")
	if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTRE_PI_SESSIONS_DIR", root)

	src := transcriptimport.SourceFor(agent_backend_entity.TypePiAgent)
	tr, err := src.Open(context.Background(), transcriptimport.Locator("-tmp-fixture-pi-wedge/2026-08-26T02-00-00_wedge.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()

	type outcome struct {
		turns []transcriptimport.Turn
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		var got []transcriptimport.Turn
		err := tr.Turns(context.Background(), func(turn transcriptimport.Turn) error {
			got = append(got, turn)
			return nil
		})
		done <- outcome{turns: got, err: err}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("回放出错: %v", res.err)
		}
		if len(res.turns) != 1 {
			t.Fatalf("轮数 = %d, 期望 1", len(res.turns))
		}
		names := eventNames(res.turns[0].Events)
		var innerCallCount int
		for _, name := range names {
			if strings.HasPrefix(name, "toolCall:read:") {
				innerCallCount++
			}
		}
		if innerCallCount != innerCalls {
			t.Fatalf("子代理内部工具调用 = %d, 期望 %d(事件序列: %v)", innerCallCount, innerCalls, names)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("回放没有返回:子代理内部过程把事件推给回放这一侧时卡住了")
	}
}

// ── 共享扫描脚手架的等价基线 ────────────────────────────────────────────────
//
// scaffoldBaseline 把 Scan → Open → Turns 的**全部可观察输出**(候选列表、元信息
// 与缺口、逐轮的字段与事件序列)压成一段可逐字比对的文本。它钉的不是某一条判据,
// 而是「这份固件读出来的东西一个字都没变」—— 三个读取器共用的 JSONL 扫描脚手架
// 收敛进 transcriptimport 之前先落这条基线,收敛之后必须逐字相同。
//
// 目测两段函数体相同是不够的:matchesFilter 三抄里就藏过真实差异。
func scaffoldBaseline(t *testing.T, src transcriptimport.Source) string {
	t.Helper()
	ctx := context.Background()
	cands, err := src.Scan(ctx, transcriptimport.Filter{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	var b strings.Builder
	for i, c := range cands {
		fmt.Fprintf(&b, "candidate[%d] backend=%s sid=%s title=%q cwd=%q started=%s ended=%s turns=%d origin=%q locator=%q\n",
			i, c.Backend, c.ProviderSessionID, c.Title, c.Cwd, baselineTime(c.StartedAt), baselineTime(c.EndedAt), c.Turns, c.Origin, c.Locator)
	}
	for _, c := range cands {
		tr, err := src.Open(ctx, c.Locator)
		if err != nil {
			t.Fatalf("Open(%q): %v", c.Locator, err)
		}
		m := tr.Meta()
		fmt.Fprintf(&b, "meta[%s] backend=%s sid=%s title=%q cwd=%q model=%s turns=%d toolCalls=%d compactions=%d started=%s ended=%s origin=%q\n",
			c.Locator, m.Backend, m.ProviderSessionID, m.Title, m.Cwd, m.Model, m.Turns, m.ToolCalls, m.Compactions,
			baselineTime(m.StartedAt), baselineTime(m.EndedAt), m.Origin)
		for i, g := range m.Gaps {
			fmt.Fprintf(&b, "  gap[%d] kind=%s count=%d detail=%q\n", i, g.Kind, g.Count, g.Detail)
		}
		err = tr.Turns(ctx, func(turn transcriptimport.Turn) error {
			fmt.Fprintf(&b, "  turn[%d] userText=%q model=%s started=%s ended=%s fork=%q err=%q usage=%s\n",
				turn.Index, turn.UserText, turn.Model, baselineTime(turn.StartedAt), baselineTime(turn.EndedAt),
				turn.ForkAnchor, turn.ErrorText, baselineJSON(turn.Usage))
			for i, img := range turn.UserImages {
				fmt.Fprintf(&b, "    image[%d] mediaType=%s inline=%q url=%q\n", i, img.MediaType, img.Source.Inline, img.Source.URL)
			}
			for i, ev := range turn.Events {
				fmt.Fprintf(&b, "    event[%d] %s\n", i, baselineEvent(ev))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("Turns(%q): %v", c.Locator, err)
		}
		if err := tr.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	return b.String()
}

func baselineTime(ts time.Time) string {
	if ts.IsZero() {
		return "-"
	}
	return ts.UTC().Format(time.RFC3339Nano)
}

func baselineJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "<marshal error: " + err.Error() + ">"
	}
	return string(raw)
}

func baselineEvent(ev agentruntime.Event) string {
	out := fmt.Sprintf("%T", ev)
	if tc, ok := ev.(agentruntime.ToolCall); ok {
		out += fmt.Sprintf(" canonical=%T", tc.Canonical)
	}
	return out + " " + baselineJSON(ev)
}

// TestTranscriptScaffoldBaseline 见 scaffoldBaseline 的说明。
func TestTranscriptScaffoldBaseline(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("testdata", "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTRE_PI_SESSIONS_DIR", root)

	src := transcriptimport.SourceFor(agent_backend_entity.TypePiAgent)
	if src == nil {
		t.Fatal("pi 读取器没注册进来")
	}
	if got := scaffoldBaseline(t, src); got != scaffoldBaselineWant {
		t.Errorf("扫描脚手架输出相对基线发生漂移\n--- got ---\n%s\n--- want ---\n%s", got, scaffoldBaselineWant)
	}
}

const scaffoldBaselineWant = `candidate[0] backend=piagent sid=22223333-4444-5555-6666-777788889999 title="给苗圃拟一份浇水时间表" cwd="/tmp/fixture-pi-plot" started=2026-08-26T02:00:00Z ended=2026-08-26T02:05:06Z turns=0 origin="" locator="-tmp-fixture-pi-plot/2026-08-26T02-00-00_22223333-4444-5555-6666-777788889999.jsonl"
meta[-tmp-fixture-pi-plot/2026-08-26T02-00-00_22223333-4444-5555-6666-777788889999.jsonl] backend=piagent sid=22223333-4444-5555-6666-777788889999 title="给苗圃拟一份浇水时间表" cwd="/tmp/fixture-pi-plot" model=glm-5.3 turns=2 toolCalls=3 compactions=1 started=2026-08-26T02:00:00Z ended=2026-08-26T02:05:06Z origin=""
  gap[0] kind=thinking_unavailable count=1 detail=""
  gap[1] kind=unclosed_tool_call count=1 detail=""
  gap[2] kind=unparsable_records count=1 detail=""
  turn[0] userText="给苗圃拟一份浇水时间表" model=glm-5.3 started=2026-08-26T02:00:05Z ended=2026-08-26T02:00:50Z fork="" err="" usage={"PromptTokens":50,"CompletionTokens":25,"ReasoningTokens":0,"CachedTokens":0,"CacheCreationTokens":0,"TotalTokens":75}
    event[0] agentruntime.ThinkingDelta {"kind":"thinking_delta","text":"先看看现有的浇水记录文件。"}
    event[1] agentruntime.ToolCall canonical=<nil> {"kind":"tool_use_start","id":"tc-bash-1","name":"bash","input":{"command":"ls /tmp/fixture-pi-plot"}}
    event[2] agentruntime.UsageUpdate {"kind":"usage","usage":{"promptTokens":120,"completionTokens":40,"reasoningTokens":15,"cachedTokens":0,"cacheCreationTokens":0,"totalTokens":175},"totalInputTokens":120}
    event[3] agentruntime.ToolResult {"kind":"tool_result","toolCallId":"tc-bash-1","content":"README.md\nwatering.csv"}
    event[4] agentruntime.ToolCall canonical=canonical.AgentSpawn {"kind":"tool_use_start","id":"tc-subagent-1","name":"subagent","input":{"task":"汇总浇水记录里的异常","profile":"data-analyst","cwd":"/tmp/fixture-pi-plot"},"canonical":{"kind":"agent.spawn","taskId":"","taskDescription":"汇总浇水记录里的异常","prompt":"汇总浇水记录里的异常","mode":"single","runs":[{"id":"run-0","index":0,"profile":"data-analyst","task":"汇总浇水记录里的异常"}],"status":"running"}}
    event[5] agentruntime.SubagentStarted {"kind":"subagent_started","toolCallId":"tc-subagent-1","info":{"taskDescription":"汇总浇水记录里的异常","prompt":"汇总浇水记录里的异常","status":"running","mode":"single","runs":[{"id":"run-0","index":0,"profile":"data-analyst","task":"汇总浇水记录里的异常","status":"running"}]}}
    event[6] agentruntime.UsageUpdate {"kind":"usage","usage":{"promptTokens":200,"completionTokens":30,"reasoningTokens":0,"cachedTokens":50,"cacheCreationTokens":0,"totalTokens":280},"totalInputTokens":250}
    event[7] agentruntime.ToolCall canonical=<nil> {"kind":"tool_use_start","id":"pi-subagent:13:tc-subagent-1:5:run-0:12:inner-read-1","name":"read","input":{"path":"watering.csv"},"parentToolCallId":"tc-subagent-1","subagentRunId":"run-0"}
    event[8] agentruntime.ToolResult {"kind":"tool_result","toolCallId":"pi-subagent:13:tc-subagent-1:5:run-0:12:inner-read-1","content":"2026-08-01,ok\n2026-08-02,异常","parentToolCallId":"tc-subagent-1","subagentRunId":"run-0"}
    event[9] agentruntime.SubagentProgress {"kind":"subagent_progress","toolCallId":"tc-subagent-1","info":{"taskDescription":"汇总浇水记录里的异常","prompt":"汇总浇水记录里的异常","lastToolName":"read","toolUses":1,"status":"completed","mode":"single","runs":[{"id":"run-0","index":0,"profile":"data-analyst","task":"汇总浇水记录里的异常","model":"glm-5.3","status":"completed","lastToolName":"read","toolUses":1,"summary":"发现两天异常浇水记录。"}]}}
    event[10] agentruntime.SubagentDone {"kind":"subagent_done","toolCallId":"tc-subagent-1","info":{"taskDescription":"汇总浇水记录里的异常","prompt":"汇总浇水记录里的异常","lastToolName":"read","toolUses":1,"status":"completed","mode":"single","runs":[{"id":"run-0","index":0,"profile":"data-analyst","task":"汇总浇水记录里的异常","model":"glm-5.3","status":"completed","lastToolName":"read","toolUses":1,"summary":"发现两天异常浇水记录。"}]}}
    event[11] agentruntime.ToolResult {"kind":"tool_result","toolCallId":"tc-subagent-1","content":"发现两天异常浇水记录。"}
    event[12] agentruntime.TextDelta {"kind":"text_delta","text":"浇水时间表整理好了，我再确认一下异常记录的文件。"}
    event[13] agentruntime.ToolCall canonical=<nil> {"kind":"tool_use_start","id":"tc-read-orphan","name":"read","input":{"path":"watering.csv"}}
    event[14] agentruntime.UsageUpdate {"kind":"usage","usage":{"promptTokens":50,"completionTokens":25,"reasoningTokens":0,"cachedTokens":0,"cacheCreationTokens":0,"totalTokens":75},"totalInputTokens":50}
    event[15] agentruntime.CompactBoundary {"kind":"compact_boundary","trigger":"manual"}
  turn[1] userText="再加一列备注" model=glm-5.3 started=2026-08-26T02:05:00Z ended=2026-08-26T02:05:06Z fork="a3" err="" usage={"PromptTokens":30,"CompletionTokens":10,"ReasoningTokens":0,"CachedTokens":0,"CacheCreationTokens":0,"TotalTokens":40}
    event[0] agentruntime.TextDelta {"kind":"text_delta","text":"已加上备注列。"}
    event[1] agentruntime.UsageUpdate {"kind":"usage","usage":{"promptTokens":30,"completionTokens":10,"reasoningTokens":0,"cachedTokens":0,"cacheCreationTokens":0,"totalTokens":40},"totalInputTokens":30}
`
