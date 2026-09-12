package claudecode

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
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
)

const (
	fixtureSessionID = "7f9e1c40-1111-4222-8333-444455556666"
	fixtureSlug      = "-tmp-fixture-garden"
)

// eventNames 把事件流压成可读的类型序列,断言整轮形状而不是抽查其中一条。
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
		case agentruntime.Retry:
			out = append(out, "retry:"+e.Message)
		case agentruntime.SubagentStarted:
			out = append(out, "subagentStarted:"+e.ToolCallID)
		case agentruntime.SubagentDone:
			out = append(out, "subagentDone:"+e.ToolCallID)
		case agentruntime.SubagentModel:
			out = append(out, "subagentModel:"+e.ToolCallID+":"+e.Model)
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

// TestTranscriptSource_ScanOpenTurns 用一份真实方言的 claude JSONL 切片(虚构内容)
// 走完 Scan → Open → Turns:元信息 + 缺口声明 + 叶子链轮次。
func TestTranscriptSource_ScanOpenTurns(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("testdata", "projects"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTRE_CLAUDE_PROJECTS_DIR", root)
	ctx := context.Background()

	Convey("Given 注册表里的 claude 读取器", t, func() {
		src := transcriptimport.SourceFor(agent_backend_entity.TypeClaudeCode)
		So(src, ShouldNotBeNil)

		Convey("When 扫描, Then 只出元信息候选", func() {
			cands, err := src.Scan(ctx, transcriptimport.Filter{})
			So(err, ShouldBeNil)
			So(len(cands), ShouldEqual, 1)
			c := cands[0]
			So(c.Backend, ShouldEqual, agent_backend_entity.TypeClaudeCode)
			So(c.ProviderSessionID, ShouldEqual, fixtureSessionID)
			So(c.Cwd, ShouldEqual, "/tmp/fixture-garden")
			So(c.Title, ShouldEqual, "请把花园的浇水计划整理成表格")
			So(c.Origin, ShouldEqual, transcriptimport.OriginAgentre)
			So(string(c.Locator), ShouldNotBeBlank)
			So(c.StartedAt.UTC().Format("2006-01-02T15:04:05Z"), ShouldEqual, "2026-08-26T01:00:00Z")
			So(c.EndedAt.UTC().Format("2006-01-02T15:04:05Z"), ShouldEqual, "2026-08-26T01:03:08Z")
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
			So(m.Cwd, ShouldEqual, "/tmp/fixture-garden")
			So(m.Model, ShouldEqual, "claude-opus-5")
			So(m.Turns, ShouldEqual, 2)
			So(m.ToolCalls, ShouldEqual, 4)
			So(m.Compactions, ShouldEqual, 1)
			// 思维文本在磁盘上只剩签名、坏行只丢那一行、末尾工具调用没有结果 ——
			// 三条都是一等缺口声明,不是日志备注。
			So(gapCount(m, transcriptimport.GapThinkingUnavailable), ShouldEqual, 1)
			So(gapCount(m, transcriptimport.GapUnparsableRecords), ShouldEqual, 1)
			So(gapCount(m, transcriptimport.GapUnclosedToolCall), ShouldEqual, 1)
			So(gapCount(m, transcriptimport.GapSubagentInternals), ShouldEqual, 0)
		})

		Convey("When 回放, Then 只走 leafUuid 叶子链并按轮切分", func() {
			cands, err := src.Scan(ctx, transcriptimport.Filter{})
			So(err, ShouldBeNil)
			tr, err := src.Open(ctx, cands[0].Locator)
			So(err, ShouldBeNil)
			defer func() { _ = tr.Close() }()

			turns := collectTurns(t, tr)
			So(len(turns), ShouldEqual, 2)

			t1 := turns[0]
			So(t1.UserText, ShouldEqual, "请把花园的浇水计划整理成表格")
			So(len(t1.UserImages), ShouldEqual, 1)
			So(t1.UserImages[0].MediaType, ShouldEqual, "image/png")
			So(string(t1.UserImages[0].Source.Inline), ShouldEqual, "hello")
			So(t1.ForkAnchor, ShouldBeBlank)
			So(t1.Model, ShouldEqual, "claude-opus-5")
			So(t1.ErrorText, ShouldBeBlank)
			So(t1.Usage, ShouldNotBeNil)
			So(t1.Usage.PromptTokens, ShouldEqual, 150)
			So(t1.Usage.CachedTokens, ShouldEqual, 1200)
			So(t1.StartedAt.UTC().Format("15:04:05"), ShouldEqual, "01:00:00")
			So(t1.EndedAt.UTC().Format("15:04:05"), ShouldEqual, "01:01:40")
			So(eventNames(t1.Events), ShouldResemble, []string{
				"text:好的,我先看看现有的计划文件。",
				"toolCall:Read:toolu_read_1:",
				"usage",
				// 磁盘上 error 是对象,旧壳按 string 解会让整行 unmarshal 失败被丢掉。
				"retry:HTTP 529 overloaded_error",
				"toolResult:toolu_read_1:",
				"toolCall:Task:toolu_task_1:",
				"usage",
				"subagentStarted:toolu_task_1",
				"subagentModel:toolu_task_1:claude-haiku-5",
				"toolCall:Read:toolu_sub_read_1:toolu_task_1",
				"toolResult:toolu_sub_read_1:toolu_task_1",
				"subagentDone:toolu_task_1",
				"toolResult:toolu_task_1:",
				"text:浇水计划整理好了。",
				"usage",
				"compact:120000:8000:auto",
			})
			// Task 调用落 canonical AgentSpawn,与线上路径同一份识别。
			var spawn *agentruntime.ToolCall
			for i := range t1.Events {
				if tc, ok := t1.Events[i].(agentruntime.ToolCall); ok && tc.ID == "toolu_task_1" {
					spawn = &tc
				}
			}
			So(spawn, ShouldNotBeNil)
			_, ok := spawn.Canonical.(canonical.AgentSpawn)
			So(ok, ShouldBeTrue)

			t2 := turns[1]
			So(t2.UserText, ShouldEqual, "再加一列备注")
			// fork 锚点 = 这一轮 user 之前最近的 assistant,claudecode 续跑就锚在它上面。
			So(t2.ForkAnchor, ShouldEqual, "11111111-0000-4000-8000-000000000007")
			So(eventNames(t2.Events), ShouldResemble, []string{
				"toolCall:Bash:toolu_bash_1:",
				"usage",
				"toolResult:toolu_bash_1:",
				"text:已加上备注列。",
				"toolCall:Grep:toolu_grep_1:",
				"usage",
			})

			// 被抛弃的分支(更早的 last-prompt 指向它)一个字都不许出现。
			for _, turn := range turns {
				So(turn.UserText, ShouldNotContainSubstring, "算了")
				for _, name := range eventNames(turn.Events) {
					So(name, ShouldNotContainSubstring, "保持现状")
				}
			}
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

		Convey("When 定位符越出 projects 根, Then 拒绝打开", func() {
			_, err := src.Open(ctx, transcriptimport.Locator("../../../etc/passwd"))
			So(err, ShouldNotBeNil)
		})
	})
}

// TestTranscriptSource_SubagentFileMissing 子代理内部过程缺失时声明缺口,而不是静默少一段。
func TestTranscriptSource_SubagentFileMissing(t *testing.T) {
	srcRoot, err := filepath.Abs(filepath.Join("testdata", "projects"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(srcRoot, fixtureSlug, fixtureSessionID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, fixtureSlug), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, fixtureSlug, fixtureSessionID+".jsonl"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTRE_CLAUDE_PROJECTS_DIR", root)

	Convey("Given 主文件在、subagents 目录不在", t, func() {
		src := transcriptimport.SourceFor(agent_backend_entity.TypeClaudeCode)
		cands, err := src.Scan(context.Background(), transcriptimport.Filter{})
		So(err, ShouldBeNil)
		So(len(cands), ShouldEqual, 1)
		tr, err := src.Open(context.Background(), cands[0].Locator)
		So(err, ShouldBeNil)
		defer func() { _ = tr.Close() }()

		Convey("When 打开, Then 声明子代理内部过程缺失", func() {
			So(gapCount(tr.Meta(), transcriptimport.GapSubagentInternals), ShouldEqual, 1)
		})

		Convey("When 回放, Then 父侧卡片仍在,只是没有内部步骤", func() {
			turns := collectTurns(t, tr)
			names := strings.Join(eventNames(turns[0].Events), "|")
			So(names, ShouldContainSubstring, "subagentDone:toolu_task_1")
			So(names, ShouldNotContainSubstring, "toolu_sub_read_1")
		})
	})
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
	root, err := filepath.Abs(filepath.Join("testdata", "projects"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTRE_CLAUDE_PROJECTS_DIR", root)

	src := transcriptimport.SourceFor(agent_backend_entity.TypeClaudeCode)
	if src == nil {
		t.Fatal("claude 读取器没注册进来")
	}
	if got := scaffoldBaseline(t, src); got != scaffoldBaselineWant {
		t.Errorf("扫描脚手架输出相对基线发生漂移\n--- got ---\n%s\n--- want ---\n%s", got, scaffoldBaselineWant)
	}
}

const scaffoldBaselineWant = `candidate[0] backend=claudecode sid=7f9e1c40-1111-4222-8333-444455556666 title="请把花园的浇水计划整理成表格" cwd="/tmp/fixture-garden" started=2026-08-26T01:00:00Z ended=2026-08-26T01:03:08Z turns=0 origin="agentre" locator="-tmp-fixture-garden/7f9e1c40-1111-4222-8333-444455556666.jsonl"
meta[-tmp-fixture-garden/7f9e1c40-1111-4222-8333-444455556666.jsonl] backend=claudecode sid=7f9e1c40-1111-4222-8333-444455556666 title="请把花园的浇水计划整理成表格" cwd="/tmp/fixture-garden" model=claude-opus-5 turns=2 toolCalls=4 compactions=1 started=2026-08-26T01:00:00Z ended=2026-08-26T01:03:08Z origin="agentre"
  gap[0] kind=thinking_unavailable count=1 detail=""
  gap[1] kind=unclosed_tool_call count=1 detail=""
  gap[2] kind=unparsable_records count=1 detail=""
  turn[0] userText="请把花园的浇水计划整理成表格" model=claude-opus-5 started=2026-08-26T01:00:00Z ended=2026-08-26T01:01:40Z fork="" err="" usage={"PromptTokens":150,"CompletionTokens":20,"ReasoningTokens":0,"CachedTokens":1200,"CacheCreationTokens":0,"TotalTokens":0}
    image[0] mediaType=image/png inline="hello" url=""
    event[0] agentruntime.TextDelta {"kind":"text_delta","text":"好的,我先看看现有的计划文件。"}
    event[1] agentruntime.ToolCall canonical=<nil> {"kind":"tool_use_start","id":"toolu_read_1","name":"Read","input":{"file_path":"/tmp/fixture-garden/plan.md"}}
    event[2] agentruntime.UsageUpdate {"kind":"usage","usage":{"promptTokens":120,"completionTokens":30,"reasoningTokens":0,"cachedTokens":1000,"cacheCreationTokens":10,"totalTokens":0},"totalInputTokens":1130}
    event[3] agentruntime.Retry {"kind":"retry","message":"HTTP 529 overloaded_error","details":"≈601ms 后重试","attempt":1,"max":10}
    event[4] agentruntime.ToolResult {"kind":"tool_result","toolCallId":"toolu_read_1","content":"周一 浇水\n周四 浇水","meta":{"type":"text","file":{"filePath":"/tmp/fixture-garden/plan.md","numLines":2}}}
    event[5] agentruntime.ToolCall canonical=canonical.AgentSpawn {"kind":"tool_use_start","id":"toolu_task_1","name":"Task","input":{"description":"整理浇水表格","subagent_type":"general-purpose","prompt":"把计划整理成 markdown 表格"},"canonical":{"kind":"agent.spawn","taskId":"","subagentType":"general-purpose","taskDescription":"整理浇水表格","prompt":"把计划整理成 markdown 表格"}}
    event[6] agentruntime.UsageUpdate {"kind":"usage","usage":{"promptTokens":140,"completionTokens":50,"reasoningTokens":0,"cachedTokens":1100,"cacheCreationTokens":0,"totalTokens":0},"totalInputTokens":1240}
    event[7] agentruntime.SubagentStarted {"kind":"subagent_started","toolCallId":"toolu_task_1","info":{"taskId":"a1b2c3d4e5f60718","subagentType":"general-purpose","kind":"local_agent","prompt":"把计划整理成 markdown 表格","toolUses":1,"totalTokens":2048,"durationMs":81000,"status":"completed"}}
    event[8] agentruntime.SubagentModel {"kind":"subagent_model","toolCallId":"toolu_task_1","model":"claude-haiku-5"}
    event[9] agentruntime.ToolCall canonical=<nil> {"kind":"tool_use_start","id":"toolu_sub_read_1","name":"Read","input":{"file_path":"/tmp/fixture-garden/plan.md"},"parentToolCallId":"toolu_task_1"}
    event[10] agentruntime.ToolResult {"kind":"tool_result","toolCallId":"toolu_sub_read_1","content":"周一 浇水\n周四 浇水","parentToolCallId":"toolu_task_1","meta":{"type":"text","file":{"filePath":"/tmp/fixture-garden/plan.md","numLines":2}}}
    event[11] agentruntime.SubagentDone {"kind":"subagent_done","toolCallId":"toolu_task_1","info":{"taskId":"a1b2c3d4e5f60718","subagentType":"general-purpose","kind":"local_agent","prompt":"把计划整理成 markdown 表格","toolUses":1,"totalTokens":2048,"durationMs":81000,"status":"completed"}}
    event[12] agentruntime.ToolResult {"kind":"tool_result","toolCallId":"toolu_task_1","content":"表格已整理","meta":{"status":"completed","prompt":"把计划整理成 markdown 表格","agentId":"a1b2c3d4e5f60718","agentType":"general-purpose","resolvedModel":"claude-haiku-5","totalDurationMs":81000,"totalTokens":2048,"totalToolUseCount":1}}
    event[13] agentruntime.TextDelta {"kind":"text_delta","text":"浇水计划整理好了。"}
    event[14] agentruntime.UsageUpdate {"kind":"usage","usage":{"promptTokens":150,"completionTokens":20,"reasoningTokens":0,"cachedTokens":1200,"cacheCreationTokens":0,"totalTokens":0},"totalInputTokens":1350}
    event[15] agentruntime.CompactBoundary {"kind":"compact_boundary","preTokens":120000,"postTokens":8000,"trigger":"auto","durationMs":3000}
  turn[1] userText="再加一列备注" model=claude-opus-5 started=2026-08-26T01:03:00Z ended=2026-08-26T01:03:08Z fork="11111111-0000-4000-8000-000000000007" err="" usage={"PromptTokens":200,"CompletionTokens":40,"ReasoningTokens":0,"CachedTokens":2000,"CacheCreationTokens":0,"TotalTokens":0}
    event[0] agentruntime.ToolCall canonical=<nil> {"kind":"tool_use_start","id":"toolu_bash_1","name":"Bash","input":{"command":"ls"}}
    event[1] agentruntime.UsageUpdate {"kind":"usage","usage":{"promptTokens":180,"completionTokens":25,"reasoningTokens":0,"cachedTokens":1500,"cacheCreationTokens":0,"totalTokens":0},"totalInputTokens":1680}
    event[2] agentruntime.ToolResult {"kind":"tool_result","toolCallId":"toolu_bash_1","content":"plan.md","meta":{"stdout":"plan.md","stderr":"","interrupted":false}}
    event[3] agentruntime.TextDelta {"kind":"text_delta","text":"已加上备注列。"}
    event[4] agentruntime.ToolCall canonical=<nil> {"kind":"tool_use_start","id":"toolu_grep_1","name":"Grep","input":{"pattern":"备注"}}
    event[5] agentruntime.UsageUpdate {"kind":"usage","usage":{"promptTokens":200,"completionTokens":40,"reasoningTokens":0,"cachedTokens":2000,"cacheCreationTokens":0,"totalTokens":0},"totalInputTokens":2200}
`
