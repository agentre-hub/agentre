import { describe, expect, it } from "vitest";

import type { TranscriptBlock, TranscriptMessage } from "./dto";
import {
  applyLiveTranscriptRows,
  buildRenderItems,
  buildSettledTranscriptRows,
  buildTranscriptRows,
  type TranscriptRow,
} from "./transcript-rows";

/*
 * 行模型给每一项标上「AI 当时所在目录」（spec 2026-11-24-chat-right-toolwindows）：
 * 工具项用自己的记录，正文用同一轮里它之前最近一个工具块的记录，再没有就是这一轮
 * 开始时的位置；全都没有时不标，链接回落到会话工作目录。
 */

function tool(id: string, cwd?: string, resultCwd = cwd): TranscriptBlock[] {
  return [
    {
      type: "tool_use",
      toolUseId: id,
      toolName: "Bash",
      ...(cwd ? { cwd } : {}),
    },
    {
      type: "tool_result",
      toolUseId: id,
      text: "ok",
      ...(resultCwd ? { cwd: resultCwd } : {}),
    },
  ];
}

function msg(
  id: number,
  blocks: TranscriptBlock[],
  role = "assistant",
): TranscriptMessage {
  return {
    id,
    sessionId: 1,
    role,
    blocks,
    model: "",
    promptTokens: 0,
    completionTokens: 0,
    cachedTokens: 0,
    cacheCreationTokens: 0,
    reasoningTokens: 0,
    durationMs: 0,
    createtime: id,
  } as unknown as TranscriptMessage;
}

function cwdOf(row: TranscriptRow | undefined): string | undefined {
  const item = row?.item as { cwd?: string } | undefined;
  return item?.cwd;
}

function textRows(rows: TranscriptRow[], messageId: number) {
  return rows.filter(
    (r) => r.messageId === messageId && r.item.type === "text",
  );
}

describe("buildRenderItems 的所在目录", () => {
  it("Given a tool call whose call and result report different directories, When built, Then the tool item keeps the call's and later text takes the result's", () => {
    const items = buildRenderItems({
      messageId: 1,
      blocks: [
        ...tool("t1", "/repo", "/repo-wt"),
        { type: "text", text: "now here" },
      ],
    });
    const toolStep = items.flatMap((i) =>
      i.type === "activity" ? i.steps : [i],
    )[0] as { cwd?: string };

    expect(toolStep.cwd).toBe("/repo");
    expect((items.at(-1) as { cwd?: string }).cwd).toBe("/repo-wt");
  });

  it("Given only the result reports a directory, When built, Then the tool item uses it", () => {
    const items = buildRenderItems({
      messageId: 1,
      blocks: tool("t1", undefined, "/repo-wt"),
    });
    const step = items.flatMap((i) =>
      i.type === "activity" ? i.steps : [i],
    )[0] as { cwd?: string };

    expect(step.cwd).toBe("/repo-wt");
  });

  it("Given no record anywhere, When built, Then no item carries a directory", () => {
    const items = buildRenderItems({
      messageId: 1,
      blocks: [...tool("t1"), { type: "text", text: "x" }],
    });

    expect(JSON.stringify(items)).not.toContain('"cwd"');
  });
});

describe("跨消息的这一轮开始时位置", () => {
  it("Given an earlier message moved the AI, When a later turn starts with text, Then that text takes the earlier location", () => {
    const { rows } = buildTranscriptRows({
      displayMessages: [
        msg(1, tool("t1", "/repo-wt")),
        msg(2, [{ type: "text", text: "go on" }], "user"),
        msg(3, [{ type: "text", text: "still there" }]),
      ],
      autonomousIds: new Set(),
    });

    expect(cwdOf(textRows(rows, 3)[0])).toBe("/repo-wt");
    expect(cwdOf(textRows(rows, 2)[0])).toBeUndefined();
  });

  it("Given a cached later message, When an earlier message changes where the AI ended, Then the cached rows are rebuilt with the new start", () => {
    const cache = new WeakMap<TranscriptMessage, TranscriptRow[]>();
    const later = msg(3, [{ type: "text", text: "still there" }]);
    const first = buildTranscriptRows({
      displayMessages: [msg(1, tool("t1", "/repo-wt")), later],
      autonomousIds: new Set(),
      cache,
    });
    const second = buildTranscriptRows({
      displayMessages: [msg(1, tool("t1", "/repo-wt2")), later],
      autonomousIds: new Set(),
      cache,
    });

    expect(cwdOf(textRows(first.rows, 3)[0])).toBe("/repo-wt");
    expect(cwdOf(textRows(second.rows, 3)[0])).toBe("/repo-wt2");
  });

  it("Given a live turn after a settled worktree turn, When live text streams in, Then it starts where the settled turn ended and follows live tool calls", () => {
    const messages = [msg(1, tool("t1", "/repo-wt")), msg(2, [])];
    const settled = buildSettledTranscriptRows({
      displayMessages: messages,
      autonomousIds: new Set(),
    });
    const before = applyLiveTranscriptRows(settled, {
      displayMessages: messages,
      autonomousIds: new Set(),
      liveByMessageId: new Map([[2, { liveTail: "streaming" }]]),
    });
    const after = applyLiveTranscriptRows(settled, {
      displayMessages: messages,
      autonomousIds: new Set(),
      liveByMessageId: new Map([
        [2, { liveBlocks: tool("t2", "/repo-wt2"), liveTail: "streaming" }],
      ]),
    });

    expect(cwdOf(textRows(before.rows, 2)[0])).toBe("/repo-wt");
    expect(cwdOf(textRows(after.rows, 2)[0])).toBe("/repo-wt2");
  });
});
