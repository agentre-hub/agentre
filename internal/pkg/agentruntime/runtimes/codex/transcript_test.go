package codex

import (
	"context"
	"encoding/json"
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

const fixtureProviderSessionID = "0a1b2c3d-1111-2222-3333-444455556666"

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
			out = append(out, fmt.Sprintf("toolCall:%s:%s", e.Name, e.ID))
		case agentruntime.ToolResult:
			out = append(out, fmt.Sprintf("toolResult:%s", e.ToolCallID))
		case agentruntime.UsageUpdate:
			out = append(out, "usage")
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

// TestTranscriptSource_ScanOpenTurns 用一份真实方言的 codex rollout 切片(虚构内容)
// 走完 Scan → Open → Turns:session_index 供的元信息、turn_id 切轮、assistant 正文
// 去重、patch_apply_end 落 canonical 文件变更块。
func TestTranscriptSource_ScanOpenTurns(t *testing.T) {
	root, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTRE_CODEX_HOME_DIR", root)
	ctx := context.Background()

	Convey("Given 注册表里的 codex 读取器", t, func() {
		src := transcriptimport.SourceFor(agent_backend_entity.TypeCodex)
		So(src, ShouldNotBeNil)

		Convey("When 扫描, Then 候选的标题与结束时间来自 session_index.jsonl, 而不是解全文", func() {
			cands, err := src.Scan(ctx, transcriptimport.Filter{})
			So(err, ShouldBeNil)
			So(len(cands), ShouldEqual, 1)
			c := cands[0]
			So(c.Backend, ShouldEqual, agent_backend_entity.TypeCodex)
			So(c.ProviderSessionID, ShouldEqual, fixtureProviderSessionID)
			So(c.Cwd, ShouldEqual, "/tmp/fixture-meadow")
			So(c.Origin, ShouldEqual, transcriptimport.OriginAgentre)
			So(string(c.Locator), ShouldNotBeBlank)
			// 索引里的标题与磁盘上第一句用户话不同 —— 断言拿到的是索引值,
			// 证明标题没有靠解全文推导。
			So(c.Title, ShouldEqual, "浇花笔记")
			So(c.Title, ShouldNotContainSubstring, "浇水计划")
			So(c.StartedAt.UTC().Format("2006-01-02T15:04:05Z"), ShouldEqual, "2026-08-20T10:00:00Z")
			// EndedAt 取自 session_index 的 updated_at(10:05:00),不是文件里最后一条
			// 记录的时间戳(10:00:15)—— 同一条断言证明扫描没有去 tail 这份转录。
			So(c.EndedAt.UTC().Format("2006-01-02T15:04:05Z"), ShouldEqual, "2026-08-20T10:05:00Z")
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
			So(m.ProviderSessionID, ShouldEqual, fixtureProviderSessionID)
			So(m.Cwd, ShouldEqual, "/tmp/fixture-meadow")
			So(m.Model, ShouldEqual, "gpt-5.5-codex")
			So(m.Turns, ShouldEqual, 2)
			// read_file + file_change(patch) + spawn_agent + list_files
			So(m.ToolCalls, ShouldEqual, 4)
			So(m.Compactions, ShouldEqual, 1)
			So(gapCount(m, transcriptimport.GapThinkingUnavailable), ShouldEqual, 1)
			So(gapCount(m, transcriptimport.GapUnparsableRecords), ShouldEqual, 1)
			So(gapCount(m, transcriptimport.GapUnclosedToolCall), ShouldEqual, 1)
			So(gapCount(m, transcriptimport.GapSubagentInternals), ShouldEqual, 1)
		})

		Convey("When 回放, Then 按 turn_id 切轮, assistant 正文只出现一次, patch 落 canonical 文件变更块", func() {
			cands, err := src.Scan(ctx, transcriptimport.Filter{})
			So(err, ShouldBeNil)
			tr, err := src.Open(ctx, cands[0].Locator)
			So(err, ShouldBeNil)
			defer func() { _ = tr.Close() }()

			turns := collectTurns(t, tr)
			So(len(turns), ShouldEqual, 2)

			t1 := turns[0]
			So(t1.UserText, ShouldEqual, "请把苗圃的浇水计划整理成表格")
			So(t1.ForkAnchor, ShouldEqual, "turn-1")
			So(t1.Model, ShouldEqual, "gpt-5.5-codex")
			So(t1.ErrorText, ShouldBeBlank)
			So(t1.Usage, ShouldNotBeNil)
			So(t1.Usage.PromptTokens, ShouldEqual, 150)
			So(t1.Usage.CachedTokens, ShouldEqual, 20)
			So(t1.Usage.CompletionTokens, ShouldEqual, 80)
			So(t1.Usage.ReasoningTokens, ShouldEqual, 10)
			So(t1.Usage.TotalTokens, ShouldEqual, 230)
			So(t1.StartedAt.UTC().Format("15:04:05"), ShouldEqual, "10:00:01")
			So(t1.EndedAt.UTC().Format("15:04:05"), ShouldEqual, "10:00:08")

			names := eventNames(t1.Events)
			So(names, ShouldResemble, []string{
				// 同一段话在 event_msg/agent_message 与 response_item/message 里各记一遍
				// (decision 8),这里只应出现一条 text —— 去重的核心断言。
				"text:好的，我先看看现有的浇水记录。",
				"toolCall:read_file:call_read_1",
				"toolResult:call_read_1",
				"toolCall:file_change:call_patch_1",
				"toolResult:call_patch_1",
				"usage",
			})
			var textCount int
			for _, n := range names {
				if n == "text:好的，我先看看现有的浇水记录。" {
					textCount++
				}
			}
			So(textCount, ShouldEqual, 1)

			// patch_apply_end 落到既有 canonical 文件变更块上。
			var patchCall *agentruntime.ToolCall
			for i := range t1.Events {
				if tc, ok := t1.Events[i].(agentruntime.ToolCall); ok && tc.ID == "call_patch_1" {
					patchCall = &tc
				}
			}
			So(patchCall, ShouldNotBeNil)
			fe, ok := patchCall.Canonical.(canonical.FileEdit)
			So(ok, ShouldBeTrue)
			So(fe.Files, ShouldHaveLength, 1)
			So(fe.Files[0].Path, ShouldEqual, "/tmp/fixture-meadow/plan.md")
			So(fe.Files[0].Kind, ShouldEqual, canonical.FileChangeKind("modified"))
			So(fe.Files[0].Plus, ShouldBeGreaterThan, 0)

			t2 := turns[1]
			So(t2.UserText, ShouldEqual, "再加一列备注")
			So(t2.ForkAnchor, ShouldEqual, "turn-2")
			So(eventNames(t2.Events), ShouldResemble, []string{
				"toolCall:spawn_agent:call_spawn_1",
				"toolResult:call_spawn_1",
				"text:已加上备注列。",
				"toolCall:list_files:call_list_1",
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
			So(err, ShouldNotBeNil)
			So(seen, ShouldEqual, 1)
		})

		Convey("When 定位符越出 codex home, Then 拒绝打开", func() {
			_, err := src.Open(ctx, transcriptimport.Locator("../../../etc/passwd"))
			So(err, ShouldNotBeNil)
		})
	})
}

// TestTranscriptSource_ScanDoesNotParseRolloutBody 证明 decision 19:扫描只读
// session_meta 首行 + session_index.jsonl,body 的其余部分即便解不出来也不影响候选。
func TestTranscriptSource_ScanDoesNotParseRolloutBody(t *testing.T) {
	root := t.TempDir()
	sid := "b2c3d4e5-6666-7777-8888-999900001111"
	dir := filepath.Join(root, "sessions", "2026", "08", "21")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	body := `{"timestamp":"2026-08-21T09:00:00.000Z","type":"session_meta","payload":{"id":"` + sid + `","cwd":"/tmp/fixture-only-scan","originator":"terminal","cli_version":"0.1.0"}}
this line is not json and would blow up any full-body parser
`
	file := filepath.Join(dir, "rollout-2026-08-21T09-00-00-"+sid+".jsonl")
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	index := `{"id":"` + sid + `","thread_name":"仅供扫描","updated_at":"2026-08-21T09:30:00.000Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "session_index.jsonl"), []byte(index), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTRE_CODEX_HOME_DIR", root)

	Convey("Given 主体除首行外全是坏数据", t, func() {
		src := transcriptimport.SourceFor(agent_backend_entity.TypeCodex)
		So(src, ShouldNotBeNil)

		Convey("When 扫描, Then 仍然出候选, 标题与结束时间来自索引", func() {
			cands, err := src.Scan(context.Background(), transcriptimport.Filter{})
			So(err, ShouldBeNil)
			So(len(cands), ShouldEqual, 1)
			So(cands[0].ProviderSessionID, ShouldEqual, sid)
			So(cands[0].Title, ShouldEqual, "仅供扫描")
			So(cands[0].EndedAt.UTC().Format("2006-01-02T15:04:05Z"), ShouldEqual, "2026-08-21T09:30:00Z")
		})
	})
}

// TestTranscriptSource_TurnModelFallsBackToMeta 钉死「元信息认得出模型时,每一轮
// 都带得出模型」—— 这是契约里 Turn.Model 的口径,claude 与 pi 两个读取器都是这么
// 兑现的(建轮时先落 Meta.Model,轮内读到更具体的再覆盖)。
//
// 破坏性输入:rollout 把 turn_context 写在 task_started **之前**(会话级只写一次
// 的那种排布),而 turn_context 是 codex 磁盘上唯一写着模型名的记录。轮内再也读不到
// 它,这一轮就没有模型 —— 落库之后这条会话的每条 assistant 消息都没有模型名,而
// 预览顶部的元信息栏偏偏显示得出来,同一份转录两种说法。
func TestTranscriptSource_TurnModelFallsBackToMeta(t *testing.T) {
	lines := []string{
		`{"timestamp":"2026-08-20T10:00:00.000Z","type":"session_meta","payload":{"id":"11112222-3333-4444-5555-666677778888","cwd":"/tmp/fixture-fallback","originator":"cli"}}`,
		`{"timestamp":"2026-08-20T10:00:00.500Z","type":"turn_context","payload":{"cwd":"/tmp/fixture-fallback","model":"gpt-5.5-codex"}}`,
		`{"timestamp":"2026-08-20T10:00:01.000Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"timestamp":"2026-08-20T10:00:02.000Z","type":"event_msg","payload":{"type":"user_message","message":"看看这个目录"}}`,
		`{"timestamp":"2026-08-20T10:00:03.000Z","type":"event_msg","payload":{"type":"agent_message","message":"看完了。"}}`,
		`{"timestamp":"2026-08-20T10:00:04.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}`,
	}

	home := t.TempDir()
	dir := filepath.Join(home, "sessions", "2026", "08", "20")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	name := "rollout-2026-08-20T10-00-00-11112222-3333-4444-5555-666677778888.jsonl"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTRE_CODEX_HOME_DIR", home)

	src := transcriptimport.SourceFor(agent_backend_entity.TypeCodex)
	tr, err := src.Open(context.Background(), transcriptimport.Locator("sessions/2026/08/20/"+name))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()

	if got := tr.Meta().Model; got != "gpt-5.5-codex" {
		t.Fatalf("Meta.Model = %q, 期望 gpt-5.5-codex", got)
	}
	var turns []transcriptimport.Turn
	if err := tr.Turns(context.Background(), func(turn transcriptimport.Turn) error {
		turns = append(turns, turn)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("轮数 = %d, 期望 1", len(turns))
	}
	if turns[0].Model != "gpt-5.5-codex" {
		t.Fatalf("Turn.Model = %q, 期望回落到 Meta.Model(gpt-5.5-codex)", turns[0].Model)
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
	root, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTRE_CODEX_HOME_DIR", root)

	src := transcriptimport.SourceFor(agent_backend_entity.TypeCodex)
	if src == nil {
		t.Fatal("codex 读取器没注册进来")
	}
	if got := scaffoldBaseline(t, src); got != scaffoldBaselineWant {
		t.Errorf("扫描脚手架输出相对基线发生漂移\n--- got ---\n%s\n--- want ---\n%s", got, scaffoldBaselineWant)
	}
}

const scaffoldBaselineWant = `candidate[0] backend=codex sid=0a1b2c3d-1111-2222-3333-444455556666 title="浇花笔记" cwd="/tmp/fixture-meadow" started=2026-08-20T10:00:00Z ended=2026-08-20T10:05:00Z turns=0 origin="agentre" locator="sessions/2026/08/20/rollout-2026-08-20T10-00-00-0a1b2c3d-1111-2222-3333-444455556666.jsonl"
meta[sessions/2026/08/20/rollout-2026-08-20T10-00-00-0a1b2c3d-1111-2222-3333-444455556666.jsonl] backend=codex sid=0a1b2c3d-1111-2222-3333-444455556666 title="浇花笔记" cwd="/tmp/fixture-meadow" model=gpt-5.5-codex turns=2 toolCalls=4 compactions=1 started=2026-08-20T10:00:01Z ended=2026-08-20T10:00:15Z origin="agentre"
  gap[0] kind=thinking_unavailable count=1 detail=""
  gap[1] kind=subagent_internals_missing count=1 detail=""
  gap[2] kind=unclosed_tool_call count=1 detail=""
  gap[3] kind=unparsable_records count=1 detail=""
  turn[0] userText="请把苗圃的浇水计划整理成表格" model=gpt-5.5-codex started=2026-08-20T10:00:01Z ended=2026-08-20T10:00:08Z fork="turn-1" err="" usage={"PromptTokens":150,"CompletionTokens":80,"ReasoningTokens":10,"CachedTokens":20,"CacheCreationTokens":0,"TotalTokens":230}
    event[0] agentruntime.TextDelta {"kind":"text_delta","text":"好的，我先看看现有的浇水记录。"}
    event[1] agentruntime.ToolCall canonical=<nil> {"kind":"tool_use_start","id":"call_read_1","name":"read_file","input":{"path":"/tmp/fixture-meadow/plan.md"}}
    event[2] agentruntime.ToolResult {"kind":"tool_result","toolCallId":"call_read_1","content":"\"周一 浇水\\n周三 施肥\""}
    event[3] agentruntime.ToolCall canonical=canonical.FileEdit {"kind":"tool_use_start","id":"call_patch_1","name":"file_change","input":{"changes":[{"path":"/tmp/fixture-meadow/plan.md","kind":"update","diff":"@@ -1,2 +1,3 @@\n 浇水计划\n-周一 浇水\n+周一 上午浇水\n+周三 施肥\n"}]},"canonical":{"kind":"file.edit","files":[{"path":"/tmp/fixture-meadow/plan.md","kind":"modified","hunks":[{"oldStart":1,"oldLines":2,"newStart":1,"newLines":3,"lines":[{"op":" ","old":1,"new":1,"text":"浇水计划"},{"op":"-","old":2,"text":"周一 浇水"},{"op":"+","new":2,"text":"周一 上午浇水"},{"op":"+","new":3,"text":"周三 施肥"}]}],"plus":2,"minus":1}]}}
    event[4] agentruntime.ToolResult {"kind":"tool_result","toolCallId":"call_patch_1","content":"{\"status\":\"completed\",\"success\":true}"}
    event[5] agentruntime.UsageUpdate {"kind":"usage","usage":{"promptTokens":150,"completionTokens":80,"reasoningTokens":10,"cachedTokens":20,"cacheCreationTokens":0,"totalTokens":230},"totalInputTokens":150}
  turn[1] userText="再加一列备注" model=gpt-5.5-codex started=2026-08-20T10:00:10Z ended=2026-08-20T10:00:15Z fork="turn-2" err="" usage=null
    event[0] agentruntime.ToolCall canonical=<nil> {"kind":"tool_use_start","id":"call_spawn_1","name":"spawn_agent","input":{"task":"核对备注格式"}}
    event[1] agentruntime.ToolResult {"kind":"tool_result","toolCallId":"call_spawn_1","content":"\"备注格式核对完成\""}
    event[2] agentruntime.TextDelta {"kind":"text_delta","text":"已加上备注列。"}
    event[3] agentruntime.ToolCall canonical=<nil> {"kind":"tool_use_start","id":"call_list_1","name":"list_files","input":{"dir":"/tmp/fixture-meadow"}}
`
