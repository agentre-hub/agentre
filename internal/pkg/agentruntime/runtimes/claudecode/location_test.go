package claudecode

import (
	"encoding/json"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/httpgateway"
	"github.com/agentre-hub/agentre/pkg/claudecode"
)

// AI 当前位置在执行侧产生(spec 决策 17):init 帧目录、EnterWorktree / ExitWorktree
// 结果、PostToolUse hook 的 cwd 都被盖到工具事件的 Cwd 上 —— 本机进程与 agentred 跑的是
// 同一段 drainStream,远端会话因此随事件把同样的位置带到桌面。

const (
	locRepo = "/repo"
	locWT   = "/repo/.claude/worktrees/feat"
)

func preTool(id, name string) claudecode.Event {
	return claudecode.Event{Kind: claudecode.EventPreToolUse, Tool: &claudecode.ToolEvent{ID: id, Name: name, Input: json.RawMessage(`{}`)}}
}

func postTool(id, meta string) claudecode.Event {
	tool := &claudecode.ToolEvent{ID: id, Response: "ok"}
	if meta != "" {
		tool.ResultMeta = json.RawMessage(meta)
	}
	return claudecode.Event{Kind: claudecode.EventPostToolUse, Tool: tool}
}

func nested(ev claudecode.Event, parent string) claudecode.Event {
	ev.ParentToolUseID = parent
	return ev
}

// drainLocations runs frames through drainStream on an active whose hook book is
// hooks, and returns the tool calls and results it emitted, by tool id.
func drainLocations(hooks *httpgateway.HookLocations, frames ...claudecode.Event) (map[string]agentruntime.ToolCall, map[string]agentruntime.ToolResult) {
	a := &claudeActive{handle: &fakeCCHandle{}, sessionUUID: "uuid-loc", hookLocs: hooks}
	out := make(chan agentruntime.Event, 64)
	drainStream(&eventCCStream{events: frames}, out, &agentruntime.RunResult{}, a, nil)
	close(out)
	calls := map[string]agentruntime.ToolCall{}
	results := map[string]agentruntime.ToolResult{}
	for ev := range out {
		switch e := ev.(type) {
		case agentruntime.ToolCall:
			calls[e.ID] = e
		case agentruntime.ToolResult:
			results[e.ToolCallID] = e
		}
	}
	return calls, results
}

func TestDrainStream_StampsAILocation(t *testing.T) {
	Convey("Given the init frame reports the CLI's directory", t, func() {
		init := claudecode.Event{Kind: claudecode.EventInit, Model: "m", Cwd: locRepo}

		Convey("When the main agent then calls a tool, Then the call carries the init directory", func() {
			calls, _ := drainLocations(httpgateway.NewHookLocations(), init, preTool("c1", "Read"))
			So(calls["c1"].Cwd, ShouldEqual, locRepo)
		})

		Convey("When EnterWorktree returns, Then its result carries worktreePath and later calls start there", func() {
			calls, results := drainLocations(httpgateway.NewHookLocations(), init,
				preTool("e1", "EnterWorktree"),
				postTool("e1", `{"worktreePath":"`+locWT+`","worktreeBranch":"worktree-feat","message":"Created worktree"}`),
				preTool("c2", "Bash"))
			So(calls["e1"].Cwd, ShouldEqual, locRepo)
			So(results["e1"].Cwd, ShouldEqual, locWT)
			So(calls["c2"].Cwd, ShouldEqual, locWT)
		})

		Convey("When ExitWorktree keeps the worktree, Then the result carries restoredCwd and no revoke marker", func() {
			_, results := drainLocations(httpgateway.NewHookLocations(), init,
				preTool("x1", "ExitWorktree"),
				postTool("x1", `{"action":"keep","originalCwd":"/repo","worktreePath":"`+locWT+`","restoredCwd":"/repo","message":"Exited worktree."}`))
			So(results["x1"].Cwd, ShouldEqual, locRepo)
			So(string(results["x1"].Meta), ShouldNotContainSubstring, agentruntime.RevokeRootMetaKey)
		})

		Convey("When ExitWorktree removes the worktree, Then the result carries restoredCwd and a revoke marker for that root", func() {
			_, results := drainLocations(httpgateway.NewHookLocations(), init,
				preTool("x2", "ExitWorktree"),
				postTool("x2", `{"action":"remove","originalCwd":"/gone","worktreePath":"`+locWT+`","restoredCwd":"/repo","discardedFiles":0,"message":"Exited and removed worktree."}`))
			So(results["x2"].Cwd, ShouldEqual, locRepo)
			var meta map[string]any
			So(json.Unmarshal(results["x2"].Meta, &meta), ShouldBeNil)
			So(meta[agentruntime.RevokeRootMetaKey], ShouldEqual, locWT)
			So(meta["action"], ShouldEqual, "remove")
			So(meta["message"], ShouldEqual, "Exited and removed worktree.")
		})

		Convey("When ExitWorktree fails, Then the location does not move", func() {
			fail := postTool("x3", `{"action":"remove","worktreePath":"`+locWT+`","restoredCwd":"/elsewhere"}`)
			fail.Tool.Err = errors.New("tool failed")
			calls, results := drainLocations(httpgateway.NewHookLocations(), init, preTool("x3", "ExitWorktree"), fail, preTool("c3", "Read"))
			So(results["x3"].Cwd, ShouldBeEmpty)
			So(string(results["x3"].Meta), ShouldNotContainSubstring, agentruntime.RevokeRootMetaKey)
			So(calls["c3"].Cwd, ShouldEqual, locRepo)
		})
	})

	Convey("Given the PostToolUse hook reported the directory after a Bash `cd sub`", t, func() {
		hooks := httpgateway.NewHookLocations()
		hooks.Record("uuid-loc", "b1", "/repo/sub", "")
		calls, results := drainLocations(hooks,
			claudecode.Event{Kind: claudecode.EventInit, Cwd: locRepo},
			preTool("b1", "Bash"), postTool("b1", ""), preTool("c4", "Read"))

		Convey("Then that tool's result carries the new directory and the next call starts there", func() {
			So(calls["b1"].Cwd, ShouldEqual, locRepo)
			So(results["b1"].Cwd, ShouldEqual, "/repo/sub")
			So(calls["c4"].Cwd, ShouldEqual, "/repo/sub")
		})
		Convey("Then the record is consumed", func() {
			_, ok := hooks.Take("b1")
			So(ok, ShouldBeFalse)
		})
	})

	Convey("Given a subagent's tool reported its directory through a hook carrying agent_id", t, func() {
		hooks := httpgateway.NewHookLocations()
		hooks.Record("uuid-loc", "s1", locWT, "agent-1")
		calls, results := drainLocations(hooks,
			claudecode.Event{Kind: claudecode.EventInit, Cwd: locRepo},
			nested(preTool("s1", "Bash"), "task-1"), nested(postTool("s1", ""), "task-1"),
			preTool("c5", "Read"))

		Convey("Then the nested result carries the directory, marked as a subagent's, so it is only claimed", func() {
			So(results["s1"].Cwd, ShouldEqual, locWT)
			So(results["s1"].ParentToolCallID, ShouldEqual, "task-1")
			So(calls["s1"].Cwd, ShouldBeEmpty)
		})
		Convey("Then the main location does not move", func() {
			So(calls["c5"].Cwd, ShouldEqual, locRepo)
		})
	})

	Convey("Given a hook carrying agent_id for an event that has no parent tool", t, func() {
		hooks := httpgateway.NewHookLocations()
		hooks.Record("uuid-loc", "s2", locWT, "agent-1")
		calls, results := drainLocations(hooks,
			claudecode.Event{Kind: claudecode.EventInit, Cwd: locRepo},
			preTool("s2", "Bash"), postTool("s2", ""), preTool("c6", "Read"))

		Convey("Then the directory is not stamped (it cannot be marked claim-only) and the main location does not move", func() {
			So(results["s2"].Cwd, ShouldBeEmpty)
			So(calls["c6"].Cwd, ShouldEqual, locRepo)
		})
	})

	Convey("Given an old CLI: no init cwd and a hook without cwd", t, func() {
		calls, results := drainLocations(httpgateway.NewHookLocations(),
			claudecode.Event{Kind: claudecode.EventInit, Model: "m"},
			preTool("o1", "Bash"), postTool("o1", ""))

		Convey("Then the events carry no directory and nothing fails", func() {
			So(calls["o1"].Cwd, ShouldBeEmpty)
			So(results["o1"].Cwd, ShouldBeEmpty)
		})
	})

	Convey("Given a runtime whose hook book was never wired", t, func() {
		calls, results := drainLocations(nil,
			claudecode.Event{Kind: claudecode.EventInit, Cwd: locRepo},
			preTool("n1", "Bash"), postTool("n1", ""))

		Convey("Then the init directory still reaches the events and nothing panics", func() {
			So(calls["n1"].Cwd, ShouldEqual, locRepo)
			So(results["n1"].Cwd, ShouldBeEmpty)
		})
	})
}

func TestRuntime_HookLocationsWiring(t *testing.T) {
	Convey("Given a runtime, When the gateway's hook book is injected, Then it reports configured", t, func() {
		r := New()
		So(r.HookLocationsConfigured(), ShouldBeFalse)
		r.SetHookLocations(httpgateway.NewHookLocations())
		So(r.HookLocationsConfigured(), ShouldBeTrue)
	})

	Convey("Given an active session with hook records, When it closes, Then its records are forgotten", t, func() {
		hooks := httpgateway.NewHookLocations()
		hooks.Record("uuid-close", "t1", "/a", "")
		a := &claudeActive{handle: &fakeCCHandle{}, sessionUUID: "uuid-close", hookLocs: hooks}
		So(a.Close(t.Context()), ShouldBeNil)
		_, ok := hooks.Take("t1")
		So(ok, ShouldBeFalse)
	})
}
