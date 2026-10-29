import { beforeEach, describe, expect, it } from "vitest";

import {
  streamForMessage,
  useChatStreamsStore,
} from "@/stores/chat-streams-store";
import type { chat_svc } from "../../../../wailsjs/go/models";

import {
  flipSubagentStatusInMessages,
  mergeSubagentMetaInMessages,
} from "./flip-subagent-status";

// A background task's tool_use block lives in `messages` (not liveBlocks) by the
// time its cross-turn completion (autonomous `completedTask`) arrives — the main
// turn that launched it has already ended. flipSubagentStatusInMessages patches
// that persisted block's subagent.status/summary so the panel + inline pill stop
// spinning without waiting for a full session reload.
const msg = (id: number, blocks: unknown[]) =>
  ({ id, blocks }) as unknown as chat_svc.ChatMessage;
const toolUse = (
  toolUseId: string,
  subagent: Record<string, unknown> | undefined,
) =>
  ({
    type: "tool_use",
    toolUseId,
    toolInput: { run_in_background: true },
    subagent,
  }) as unknown as chat_svc.ChatBlock;

describe("flipSubagentStatusInMessages", () => {
  it("flips the matching running block to completed and sets summary", () => {
    const messages = [
      msg(1, [toolUse("tu-bg", { kind: "local_bash", status: "running" })]),
    ];
    const out = flipSubagentStatusInMessages(
      messages,
      "tu-bg",
      "completed",
      "Background command finished (exit 0)",
    );
    expect(out[0].blocks[0].subagent?.status).toBe("completed");
    expect(out[0].blocks[0].subagent?.summary).toBe(
      "Background command finished (exit 0)",
    );
  });

  it("does not mutate the input messages (immutable update)", () => {
    const messages = [
      msg(1, [toolUse("tu-bg", { kind: "local_bash", status: "running" })]),
    ];
    flipSubagentStatusInMessages(messages, "tu-bg", "completed");
    expect(messages[0].blocks[0].subagent?.status).toBe("running");
  });

  it("returns the same array reference when the toolUseId is not found", () => {
    const messages = [
      msg(1, [toolUse("tu-other", { kind: "local_bash", status: "running" })]),
    ];
    const out = flipSubagentStatusInMessages(
      messages,
      "tu-missing",
      "completed",
    );
    expect(out).toBe(messages);
  });

  it("leaves sibling blocks and other messages untouched", () => {
    const sibling = toolUse("tu-keep", {
      kind: "local_bash",
      status: "running",
    });
    const messages = [
      msg(1, [sibling]),
      msg(2, [toolUse("tu-bg", { kind: "local_agent", status: "running" })]),
    ];
    const out = flipSubagentStatusInMessages(messages, "tu-bg", "completed");
    expect(out[0]).toBe(messages[0]); // untouched message keeps its reference
    expect(out[1].blocks[0].subagent?.status).toBe("completed");
    expect(out[0].blocks[0].subagent?.status).toBe("running");
  });

  it("no-ops (empty status) leave messages unchanged", () => {
    const messages = [
      msg(1, [toolUse("tu-bg", { kind: "local_bash", status: "running" })]),
    ];
    expect(flipSubagentStatusInMessages(messages, "tu-bg", "")).toBe(messages);
    expect(flipSubagentStatusInMessages(messages, "", "completed")).toBe(
      messages,
    );
  });
});

// A background subagent keeps working while the session is idle: the CLI streams
// task_progress the whole time, but its spawn card (the Agent tool_use block) has
// long since landed in `messages`. mergeSubagentMetaInMessages patches that
// persisted block so the card's tool count / token pill keeps ticking live.
describe("mergeSubagentMetaInMessages", () => {
  it("merges live progress into the persisted spawn card", () => {
    const messages = [
      msg(1, [
        toolUse("tu-agent", {
          kind: "local_agent",
          status: "running",
          toolUses: 9,
          totalTokens: 84739,
          lastToolName: "Read",
        }),
      ]),
    ];
    const out = mergeSubagentMetaInMessages(messages, "tu-agent", {
      toolUses: 21,
      totalTokens: 132480,
      lastToolName: "Edit",
    } as unknown as chat_svc.ChatBlockSubagent);
    expect(out[0].blocks[0].subagent?.toolUses).toBe(21);
    expect(out[0].blocks[0].subagent?.totalTokens).toBe(132480);
    expect(out[0].blocks[0].subagent?.lastToolName).toBe("Edit");
    // fields the progress frame didn't carry stay put
    expect(out[0].blocks[0].subagent?.status).toBe("running");
    expect(out[0].blocks[0].subagent?.kind).toBe("local_agent");
  });

  it("does not mutate the input messages (immutable update)", () => {
    const messages = [
      msg(1, [toolUse("tu-agent", { status: "running", toolUses: 9 })]),
    ];
    mergeSubagentMetaInMessages(messages, "tu-agent", {
      toolUses: 21,
    } as unknown as chat_svc.ChatBlockSubagent);
    expect(messages[0].blocks[0].subagent?.toolUses).toBe(9);
  });

  it("returns the same array reference on no-ops", () => {
    const messages = [
      msg(1, [toolUse("tu-agent", { status: "running", toolUses: 9 })]),
    ];
    expect(
      mergeSubagentMetaInMessages(messages, "tu-missing", {
        toolUses: 21,
      } as unknown as chat_svc.ChatBlockSubagent),
    ).toBe(messages);
    expect(
      mergeSubagentMetaInMessages(
        messages,
        "tu-agent",
        undefined as unknown as chat_svc.ChatBlockSubagent,
      ),
    ).toBe(messages);
  });

  it("Given a persisted full run snapshot, When a newer snapshot arrives, Then runs replace atomically and omission preserves the latest snapshot", () => {
    const initialRuns = [
      { id: "run-a", index: 0, status: "running", task: "A" },
      { id: "run-b", index: 1, status: "waiting", task: "B" },
    ];
    const replacementRuns = [
      { id: "run-a", index: 0, status: "completed", task: "A" },
    ];
    const messages = [
      msg(1, [toolUse("tu-agent", { runs: initialRuns, status: "running" })]),
    ];

    const replaced = mergeSubagentMetaInMessages(messages, "tu-agent", {
      runs: replacementRuns,
      status: "partial",
    } as unknown as chat_svc.ChatBlockSubagent);
    expect(replaced[0].blocks[0].subagent?.runs).toEqual(replacementRuns);

    const omitted = mergeSubagentMetaInMessages(replaced, "tu-agent", {
      runs: undefined,
      status: "completed",
    } as unknown as chat_svc.ChatBlockSubagent);
    expect(omitted[0].blocks[0].subagent?.runs).toEqual(replacementRuns);
    expect(omitted[0].blocks[0].subagent?.status).toBe("completed");
  });
});

describe("live mergeSubagentMeta run snapshots", () => {
  beforeEach(() => {
    useChatStreamsStore.setState({ streams: new Map() });
  });

  it("Given a live full run snapshot, When replacement and omission updates arrive, Then replacement is atomic and omission does not clear runs", () => {
    const initialRuns = [
      { id: "run-a", index: 0, status: "running", task: "A" },
      { id: "run-b", index: 1, status: "waiting", task: "B" },
    ];
    const replacementRuns = [
      { id: "run-b", index: 1, status: "failed", task: "B" },
    ];
    const store = useChatStreamsStore.getState();
    store.openStream({
      assistantMessageId: 1,
      name: "chat:event:7:1",
      sessionId: 7,
      streamStartedAt: 1,
    });
    store.appendLiveToolUse(7, 1, {
      toolName: "subagent",
      toolUseId: "tu-agent",
      subagent: {
        runs: initialRuns,
        status: "running",
      } as unknown as chat_svc.ChatBlockSubagent,
    });

    store.mergeSubagentMeta(7, 1, "tu-agent", {
      runs: replacementRuns,
      status: "failed",
    } as unknown as chat_svc.ChatBlockSubagent);
    expect(
      streamForMessage(useChatStreamsStore.getState(), 7, 1)?.liveBlocks[0]
        .subagent?.runs,
    ).toEqual(replacementRuns);

    store.mergeSubagentMeta(7, 1, "tu-agent", {
      runs: undefined,
      summary: "settled",
    } as unknown as chat_svc.ChatBlockSubagent);
    const subagent = streamForMessage(useChatStreamsStore.getState(), 7, 1)
      ?.liveBlocks[0].subagent;
    expect(subagent?.runs).toEqual(replacementRuns);
    expect(subagent?.summary).toBe("settled");
  });
});

describe("agrctl 卡:已落库的 shell 块经会话级事件升级并收尾", () => {
  const shell = (subagent?: Record<string, unknown>) =>
    ({
      type: "tool_use",
      toolUseId: "tu-ctl",
      toolName: "Bash",
      toolInput: { run_in_background: true },
      subagent,
    }) as unknown as chat_svc.ChatBlock;

  const run = (status: string) => ({
    id: "agrctl:9",
    index: 0,
    agent: "reviewer",
    task: "审查",
    status,
  });

  it("upgrades a persisted shell block that has no overlay yet, in place", () => {
    const messages = [msg(1, [shell(undefined)])];
    const out = mergeSubagentMetaInMessages(messages, "tu-ctl", {
      origin: "agrctl",
      kind: "local_bash",
      subagentType: "reviewer",
      taskDescription: "审查",
      status: "running",
      runs: [run("running")],
    } as chat_svc.ChatBlockSubagent);
    expect(out[0].blocks).toHaveLength(1);
    const sa = out[0].blocks[0].subagent!;
    expect(sa.origin).toBe("agrctl");
    expect(sa.runs).toEqual([run("running")]);
  });

  it("finishes the persisted card in place, keeps the background kind and takes the backend's run snapshot", () => {
    const upgraded = mergeSubagentMetaInMessages(
      [msg(1, [shell(undefined)])],
      "tu-ctl",
      {
        origin: "agrctl",
        kind: "local_bash",
        subagentType: "reviewer",
        taskDescription: "审查",
        status: "running",
        runs: [run("running")],
      } as chat_svc.ChatBlockSubagent,
    );
    const out = mergeSubagentMetaInMessages(upgraded, "tu-ctl", {
      origin: "agrctl",
      status: "completed",
      finalText: "LGTM",
      runs: [run("completed")],
    } as chat_svc.ChatBlockSubagent);
    const sa = out[0].blocks[0].subagent!;
    expect(sa).toMatchObject({
      kind: "local_bash",
      status: "completed",
      finalText: "LGTM",
    });
    expect(sa.runs).toEqual([run("completed")]);
  });

  it("does not touch a non-agrctl shell block that has no overlay", () => {
    const messages = [msg(1, [shell(undefined)])];
    const out = mergeSubagentMetaInMessages(messages, "tu-ctl", {
      status: "completed",
    } as chat_svc.ChatBlockSubagent);
    expect(out).toBe(messages);
  });
});
