import { describe, expect, it } from "vitest";

import { collectReplayCalls } from "./replay-calls";
import type { CanonicalDTO, TranscriptMessage } from "../../dto";

function message(canonicals: CanonicalDTO[]): TranscriptMessage {
  return {
    blocks: canonicals.map((canonical) => ({ type: "tool_use", canonical })),
  } as unknown as TranscriptMessage;
}

function fileEdit(path: string, plus = 1): CanonicalDTO {
  return {
    kind: "file.edit",
    fileEdit: {
      files: [
        {
          path,
          kind: "modified",
          hunks: [
            {
              oldStart: 1,
              oldLines: 1,
              newStart: 1,
              newLines: 1,
              lines: [{ op: "+", new: 1, text: `line for ${path}` }],
            },
          ],
          plus,
          minus: 0,
        },
      ],
    },
  };
}

function fileWrite(path: string): CanonicalDTO {
  return {
    kind: "file.write",
    fileWrite: { path, content: "hello\n", lines: 1, bytes: 6 },
  };
}

describe("collectReplayCalls", () => {
  // Given 同一个文件被绝对路径与相对路径两种写法改过，When 收集，
  // Then 两次都收进来（归属判定与「变更」行同一口径，否则点开的 diff 缺一半）。
  it("collects both absolute and cwd-relative spellings of the same file", () => {
    const calls = collectReplayCalls(
      [
        message([fileEdit("/repo/app/x.ts")]),
        message([fileEdit("app/x.ts", 2)]),
      ],
      "/repo",
      "app/x.ts",
    );

    expect(calls.length).toBe(2);
    expect(calls.map((c) => c.kind)).toEqual(["file.edit", "file.edit"]);
  });

  // Given 同一次调用里还有别的文件，When 收集，Then 只收目标文件那一份。
  it("ignores patches for other files in the same call", () => {
    const calls = collectReplayCalls(
      [
        message([
          {
            kind: "file.edit",
            fileEdit: {
              files: [
                ...(fileEdit("app/x.ts").fileEdit?.files ?? []),
                ...(fileEdit("app/y.ts").fileEdit?.files ?? []),
              ],
            },
          },
        ]),
      ],
      "/repo",
      "app/x.ts",
    );

    expect(calls.length).toBe(1);
    expect(calls[0].kind === "file.edit" && calls[0].patch.path).toBe(
      "app/x.ts",
    );
  });

  // Given 工作根之外的同名路径，When 收集，Then 不收（归属过滤恒定生效）。
  it("drops calls whose path resolves outside the work root", () => {
    const calls = collectReplayCalls(
      [message([fileEdit("/tmp/app/x.ts")])],
      "/repo",
      "app/x.ts",
    );

    expect(calls).toEqual([]);
  });

  // Given 全量写入，When 收集，Then 作为绝对状态调用收进来（带 content）。
  it("collects a whole-file write as an absolute-state call", () => {
    const calls = collectReplayCalls(
      [message([fileWrite("/repo/app/x.ts")])],
      "/repo",
      "app/x.ts",
    );

    expect(calls.length).toBe(1);
    expect(calls[0].kind === "file.write" && calls[0].write.content).toBe(
      "hello\n",
    );
  });

  // Given 消息里还有没有 canonical 的工具块，When 收集，Then 跳过它们
  // （四种状态与 diff 全部来自 canonical，历史块产不出改动内容）。
  it("skips blocks without a canonical payload", () => {
    const plain = {
      blocks: [{ type: "tool_use", toolName: "Edit" }],
    } as unknown as TranscriptMessage;

    expect(collectReplayCalls([plain], "/repo", "app/x.ts")).toEqual([]);
  });

  // Given 多次调用分布在多条消息里，When 收集，Then 顺序就是调用先后
  // （重放依赖这个顺序，乱序会把「后一次改的是前一次的产物」倒过来）。
  it("keeps the calls in message and block order", () => {
    const calls = collectReplayCalls(
      [
        message([fileWrite("app/x.ts")]),
        message([fileEdit("app/x.ts", 1), fileEdit("app/x.ts", 2)]),
      ],
      "/repo",
      "app/x.ts",
    );

    expect(calls.map((c) => c.kind)).toEqual([
      "file.write",
      "file.edit",
      "file.edit",
    ]);
    expect(calls[2].kind === "file.edit" && calls[2].patch.plus).toBe(2);
  });

  // Given 后端发来一个渲染层没见过的 diff op（宽边界的 `op` 是 string），
  // When 收集，Then 它被收窄成上下文行——既不记进加数也不记进减数，
  // 否则重放出的 ±N 会与「变更」那一行上的数字对不上。
  it("narrows an unknown diff op to a context line", () => {
    const odd: CanonicalDTO = {
      kind: "file.edit",
      fileEdit: {
        files: [
          {
            path: "app/x.ts",
            kind: "modified",
            hunks: [
              {
                oldStart: 1,
                oldLines: 1,
                newStart: 1,
                newLines: 1,
                lines: [{ op: "?", old: 1, new: 1, text: "mystery" }],
              },
            ],
            plus: 0,
            minus: 0,
          },
        ],
      },
    };

    const calls = collectReplayCalls([message([odd])], "/repo", "app/x.ts");

    expect(
      calls[0].kind === "file.edit" && calls[0].patch.hunks[0].lines,
    ).toEqual([{ op: " ", old: 1, new: 1, text: "mystery" }]);
  });

  // Given 会话没有工作根（cwd 为空），When 收集，Then 按工具给的原样路径匹配
  // （无从归属，也拼不出相对路径——与「变更」行的行为一致）。
  it("matches raw tool paths when the session has no work root", () => {
    const calls = collectReplayCalls(
      [message([fileEdit("app/x.ts")])],
      "",
      "app/x.ts",
    );

    expect(calls.length).toBe(1);
  });
});
