import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { ReplayedFileDiff } from "./replay-view";
import type { ReplayResult } from "./replay";
import type { DiffHunk } from "../types";

const oneHunk: DiffHunk[] = [
  {
    oldStart: 1,
    oldLines: 2,
    newStart: 1,
    newLines: 2,
    lines: [
      { op: " ", old: 1, new: 1, text: "keep" },
      { op: "-", old: 2, text: "before" },
      { op: "+", new: 2, text: "after" },
    ],
  },
];

function ok(extra: Partial<Extract<ReplayResult, { ok: true }>> = {}) {
  return {
    ok: true as const,
    hunks: oneHunk,
    plus: 1,
    minus: 1,
    wholeFileWrite: false,
    truncatedCalls: [],
    ...extra,
  };
}

describe("ReplayedFileDiff", () => {
  // Given 重放成功，When 渲染，Then 画出重放出来的连续 diff，不出降级说明。
  it("renders the replayed diff lines", () => {
    render(<ReplayedFileDiff path="app/x.ts" result={ok()} />);

    expect(screen.getByText("before")).toBeDefined();
    expect(screen.getByText("after")).toBeDefined();
    expect(screen.queryByText(/call order/i)).toBeNull();
  });

  // Given 首个操作是全量写入（spec 决策 14），When 渲染，
  // Then 显式标注它没有与写入前的内容比较 —— 不能让它看起来像一次逐行对比。
  it("labels a leading whole-file write", () => {
    render(
      <ReplayedFileDiff
        path="app/x.ts"
        result={ok({ wholeFileWrite: true })}
      />,
    );

    expect(
      screen.getByText(/not compared against the content before the write/i),
    ).toBeDefined();
  });

  // Given 有调用被产出方截断，When 渲染，Then 告知这份 diff 不完整（不静默）。
  it("warns that a truncated call makes the diff incomplete", () => {
    render(
      <ReplayedFileDiff path="app/x.ts" result={ok({ truncatedCalls: [1] })} />,
    );

    expect(screen.getByText(/incomplete/i)).toBeDefined();
  });

  // Given 重放出来净变更为空，When 渲染，Then 说清楚是「互相抵消」，
  // 而不是留一片什么都没有的空白（spec：「空 diff」不是可接受的降级）。
  it("explains an empty net change instead of showing a blank diff", () => {
    render(
      <ReplayedFileDiff
        path="app/x.ts"
        result={ok({ hunks: [], plus: 0, minus: 0 })}
      />,
    );

    expect(screen.getByText(/cancel each other out/i)).toBeDefined();
  });

  // Given 重放失败，When 渲染，Then 说明为什么没能合并，并按调用顺序分段列出
  // 每一次改动（每段自带它的标注）。
  it("explains the failure and lists every call in order", () => {
    const failed: ReplayResult = {
      ok: false,
      reason: "writeOverEdits",
      truncatedCalls: [0],
      segments: [
        {
          index: 0,
          hunks: oneHunk,
          plus: 1,
          minus: 1,
          truncated: true,
          wholeFileWrite: false,
        },
        {
          index: 1,
          hunks: [
            {
              oldStart: 0,
              oldLines: 0,
              newStart: 1,
              newLines: 1,
              lines: [{ op: "+", new: 1, text: "written" }],
            },
          ],
          plus: 1,
          minus: 0,
          truncated: false,
          wholeFileWrite: true,
        },
      ],
    };

    render(<ReplayedFileDiff path="app/x.ts" result={failed} />);

    expect(
      screen.getByText(/whole-file write landed after incremental edits/i),
    ).toBeDefined();
    expect(screen.getByText("Change 1")).toBeDefined();
    expect(screen.getByText("Change 2")).toBeDefined();
    expect(screen.getByText("written")).toBeDefined();
    // 被截断的那一段自己带标注。
    expect(screen.getByText(/this change is incomplete/i)).toBeDefined();
  });
});
