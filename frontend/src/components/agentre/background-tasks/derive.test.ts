import { describe, expect, it } from "vitest";

import type { chat_svc } from "../../../../wailsjs/go/models";

import { deriveBackgroundTasks } from "./derive";

// A genuine background task block carries a local_bash OR local_agent overlay
// AND the run_in_background tool input — the discriminator is run_in_background,
// not the kind (both a backgrounded Bash and a backgrounded Agent qualify). The
// helpers default to run_in_background:true so fixtures stay valid under that
// "is this a background task" contract.
const makeBlock = (
  type: string,
  toolUseId: string,
  subagent: Record<string, unknown>,
) =>
  ({
    type,
    toolUseId,
    toolInput: { run_in_background: true },
    subagent,
  }) as unknown as chat_svc.ChatBlock;
const makeMessage = (createtime: number, blocks: chat_svc.ChatBlock[]) =>
  ({ createtime, blocks }) as unknown as chat_svc.ChatMessage;

const tu = (over: Record<string, unknown> = {}) =>
  ({
    type: "tool_use",
    toolUseId: "tu1",
    toolInput: { run_in_background: true },
    subagent: {
      kind: "local_bash",
      taskDescription: "sleep 20",
      status: "running",
    },
    ...over,
  }) as unknown as Parameters<typeof deriveBackgroundTasks>[1][number];

describe("deriveBackgroundTasks", () => {
  it("derives running task from a live tool_use block with .subagent", () => {
    const tasks = deriveBackgroundTasks([], [tu()]);
    expect(tasks).toEqual([
      {
        toolUseId: "tu1",
        kind: "local_bash",
        description: "sleep 20",
        status: "running",
      },
    ]);
  });

  // CLI 的英式 "cancelled" 由后端 normalizeTaskStatus 在边界归一,前端只认 canceled。
  it("maps a stopped subagent status to canceled", () => {
    const stopped = deriveBackgroundTasks(
      [],
      [tu({ subagent: { kind: "local_bash", status: "canceled" } })],
    );
    expect(stopped[0].status).toBe("canceled");
  });

  it("includes a background local_agent (run_in_background subagent) with its real kind", () => {
    const msg = {
      blocks: [
        tu({
          toolUseId: "tu2",
          subagent: {
            kind: "local_agent",
            taskDescription: "Explore repo",
            status: "completed",
          },
        }),
      ],
    };
    const tasks = deriveBackgroundTasks([msg as never], []);
    expect(tasks).toHaveLength(1);
    expect(tasks[0]).toMatchObject({
      toolUseId: "tu2",
      kind: "local_agent",
      description: "Explore repo",
    });
  });

  it("live overrides history for the same toolUseId (dedupe, live wins)", () => {
    const msg = {
      blocks: [
        tu({
          subagent: {
            kind: "local_bash",
            taskDescription: "x",
            status: "running",
          },
        }),
      ],
    };
    const live = [
      tu({
        subagent: {
          kind: "local_bash",
          taskDescription: "x",
          status: "completed",
        },
      }),
    ];
    const tasks = deriveBackgroundTasks([msg as never], live);
    expect(tasks).toHaveLength(1);
    expect(tasks[0].status).toBe("completed");
  });

  it("ignores tool_use blocks without .subagent and non-tool_use blocks", () => {
    const tasks = deriveBackgroundTasks(
      [
        {
          blocks: [
            { type: "tool_use", toolUseId: "x" },
            { type: "text", text: "hi" },
          ],
        } as never,
      ],
      [],
    );
    expect(tasks).toEqual([]);
  });

  it("empty/unknown kind is excluded (only local_bash passes)", () => {
    const tasks = deriveBackgroundTasks(
      [],
      [tu({ subagent: { taskDescription: "y", status: "running" } })],
    );
    expect(tasks).toHaveLength(0);
  });

  it("excludes a foreground bash — local_bash overlay without run_in_background is not a background task", () => {
    // The real CLI emits task_type:"local_bash" frames for EVERY Bash, not just
    // run_in_background ones, so the kind alone is not enough — gate on the
    // tool input's run_in_background flag (same discriminator the inline pill uses).
    const foreground = {
      type: "tool_use",
      toolUseId: "tu-fg",
      toolName: "Bash",
      toolInput: { command: "git stash -u" },
      subagent: {
        kind: "local_bash",
        status: "running",
        taskDescription: "Stash untracked files",
      },
    } as unknown as Parameters<typeof deriveBackgroundTasks>[1][number];
    const tasks = deriveBackgroundTasks([], [foreground]);
    expect(tasks).toHaveLength(0);
  });

  it("includes a bash with run_in_background:true", () => {
    const background = {
      type: "tool_use",
      toolUseId: "tu-bg",
      toolName: "Bash",
      toolInput: { command: "sleep 20", run_in_background: true },
      subagent: {
        kind: "local_bash",
        status: "running",
        taskDescription: "sleep 20",
      },
    } as unknown as Parameters<typeof deriveBackgroundTasks>[1][number];
    const tasks = deriveBackgroundTasks([], [background]);
    expect(tasks.map((t) => t.toolUseId)).toEqual(["tu-bg"]);
  });

  it("threads startedAt from the containing message createtime + durationMs + summary", () => {
    const msg = {
      createtime: 1700000000000,
      blocks: [
        tu({
          toolUseId: "tu9",
          subagent: {
            kind: "local_bash",
            taskDescription: "sleep",
            status: "completed",
            summary: 'Background command "sleep 20" completed (exit code 0)',
          },
        }),
      ],
    };
    const tasks = deriveBackgroundTasks([msg as never], []);
    expect(tasks[0]).toMatchObject({
      toolUseId: "tu9",
      startedAt: 1700000000000,
      summary: 'Background command "sleep 20" completed (exit code 0)',
    });
  });

  it("reads durationMs from subagent for completed local_bash tasks", () => {
    const msg = {
      createtime: 1,
      blocks: [
        tu({
          toolUseId: "tA",
          subagent: {
            kind: "local_bash",
            taskDescription: "run build",
            status: "completed",
            durationMs: 4200,
          },
        }),
      ],
    };
    expect(deriveBackgroundTasks([msg as never], [])[0].durationMs).toBe(4200);
  });

  it("live blocks (no containing message) have undefined startedAt", () => {
    const tasks = deriveBackgroundTasks([], [tu()]);
    expect(tasks[0].startedAt).toBeUndefined();
  });

  it("keeps history startedAt when a running task is also in liveBlocks (live wins but preserves elapsed base)", () => {
    const msg = {
      createtime: 1700000000000,
      blocks: [
        tu({
          toolUseId: "tuBoth",
          subagent: {
            kind: "local_bash",
            taskDescription: "sleep 20",
            status: "running",
          },
        }),
      ],
    };
    const live = [
      tu({
        toolUseId: "tuBoth",
        subagent: {
          kind: "local_bash",
          taskDescription: "sleep 20",
          status: "running",
        },
      }),
    ];
    const tasks = deriveBackgroundTasks([msg as never], live);
    expect(tasks).toHaveLength(1);
    expect(tasks[0]).toMatchObject({
      toolUseId: "tuBoth",
      startedAt: 1700000000000,
      status: "running",
    });
  });

  it("includes both a background bash and a background subagent, each with its kind", () => {
    const messages = [
      makeMessage(1000, [
        makeBlock("tool_use", "tu-bash", {
          kind: "local_bash",
          status: "running",
          taskDescription: "sleep 5",
        }),
        makeBlock("tool_use", "tu-agent", {
          kind: "local_agent",
          status: "running",
          taskDescription: "Explore",
        }),
      ]),
    ];
    const tasks = deriveBackgroundTasks(messages, []);
    expect(tasks.map((t) => t.toolUseId)).toEqual(["tu-bash", "tu-agent"]);
    expect(tasks.map((t) => t.kind)).toEqual(["local_bash", "local_agent"]);
  });

  it("includes a background local_agent even without run_in_background (Agent tool defaults to background)", () => {
    // Unlike Bash (foreground by default), the real CLI Agent tool runs subagents
    // in the BACKGROUND by default and only carries run_in_background:false when
    // made synchronous — so a local_agent overlay with NO run_in_background input
    // is a genuine background task (verified against real transcripts: 63 background
    // Agent calls all omit the flag).
    const background = {
      type: "tool_use",
      toolUseId: "tu-bg-agent",
      toolName: "Agent",
      toolInput: { subagent_type: "general-purpose", description: "explore" },
      subagent: {
        kind: "local_agent",
        status: "running",
        taskDescription: "Explore repo",
      },
    } as unknown as Parameters<typeof deriveBackgroundTasks>[1][number];
    const tasks = deriveBackgroundTasks([], [background]);
    expect(tasks.map((t) => t.toolUseId)).toEqual(["tu-bg-agent"]);
  });

  it("excludes a foreground (synchronous) local_agent — run_in_background:false", () => {
    // Passing run_in_background:false makes the Agent run synchronously/inline;
    // that is the ONLY case a subagent is not a background task.
    const foreground = {
      type: "tool_use",
      toolUseId: "tu-fg-agent",
      toolName: "Agent",
      toolInput: {
        subagent_type: "general-purpose",
        description: "explore",
        run_in_background: false,
      },
      subagent: {
        kind: "local_agent",
        status: "running",
        taskDescription: "Explore repo",
      },
    } as unknown as Parameters<typeof deriveBackgroundTasks>[1][number];
    const tasks = deriveBackgroundTasks([], [foreground]);
    expect(tasks).toHaveLength(0);
  });

  it("carries the real task_id through to BackgroundTask.taskId", () => {
    const messages = [
      makeMessage(1000, [
        makeBlock("tool_use", "tu-bash", {
          kind: "local_bash",
          status: "running",
          taskId: "b3875slp0",
        }),
      ]),
    ];
    const tasks = deriveBackgroundTasks(messages, []);
    expect(tasks[0].taskId).toBe("b3875slp0");
  });

  it("filters out cleared toolUseIds", () => {
    const messages = [
      makeMessage(1000, [
        makeBlock("tool_use", "tu-a", {
          kind: "local_bash",
          status: "completed",
        }),
        makeBlock("tool_use", "tu-b", {
          kind: "local_bash",
          status: "running",
        }),
      ]),
    ];
    const tasks = deriveBackgroundTasks(messages, [], new Set(["tu-a"]));
    expect(tasks.map((t) => t.toolUseId)).toEqual(["tu-b"]);
  });

  it("maps a canceled task to canceled (terminal, clearable — 用户停止)", () => {
    const messages = [
      makeMessage(1000, [
        makeBlock("tool_use", "tu-x", {
          kind: "local_bash",
          status: "canceled",
          taskDescription: "sleep",
        }),
      ]),
    ];
    const tasks = deriveBackgroundTasks(messages, []);
    expect(tasks[0].status).toBe("canceled");
  });
});
