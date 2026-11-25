import { describe, expect, it } from "vitest";

import type { chat_svc } from "../../../../../wailsjs/go/models";

import { cwdBeforeWindow } from "./use-transcript-rows";

// spec「AI 当时所在目录」：文字块没有同轮之前的工具块时用这一轮开始时的 AI 当前位置，
// 重新加载后不变。转录只渲染正文取全的那一段（以及压缩折叠之后的那一段）；那之前 AI
// 在哪，从窗口外只带派生视图块的消息、以及被折叠掉的那部分算出来。

function msg(
  id: number,
  blocks: Partial<chat_svc.ChatBlock>[],
  extra: Partial<chat_svc.ChatMessage> = {},
): chat_svc.ChatMessage {
  return {
    id,
    role: "assistant",
    blocks,
    ...extra,
  } as unknown as chat_svc.ChatMessage;
}

describe("cwdBeforeWindow", () => {
  it("Given an unloaded prefix whose derived tool call entered a worktree, Then the window starts there", () => {
    const unloaded = msg(
      1,
      [
        {
          type: "tool_use",
          toolUseId: "t1",
          cwd: "/repo",
          resultCwd: "/repo-wt",
        },
      ],
      { blocksLoaded: false },
    );
    const loaded = [
      msg(2, [{ type: "text", text: "next" }], { role: "user" }),
      msg(3, [{ type: "text", text: "src/a.ts" }]),
    ];

    expect(cwdBeforeWindow([unloaded, ...loaded], loaded)).toBe("/repo-wt");
  });

  it("Given the display starts mid-message after a compact boundary, Then the trimmed earlier blocks count too", () => {
    const full = msg(1, [
      { type: "tool_use", toolUseId: "t1", cwd: "/repo-wt" },
      { type: "compact_boundary" },
      { type: "text", text: "after" },
    ]);
    const trimmed = { ...full, blocks: full.blocks.slice(1) };

    expect(cwdBeforeWindow([full], [trimmed as chat_svc.ChatMessage])).toBe(
      "/repo-wt",
    );
  });

  it("Given everything is displayed, Then there is no earlier location", () => {
    const all = [msg(1, [{ type: "tool_use", toolUseId: "t1", cwd: "/a" }])];
    expect(cwdBeforeWindow(all, all)).toBeUndefined();
  });
});
