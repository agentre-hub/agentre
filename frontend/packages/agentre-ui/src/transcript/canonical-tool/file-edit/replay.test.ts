import { describe, expect, it } from "vitest";

import { replayPatches } from "./replay";
import type { ReplayCall } from "./replay";
import type { DiffHunk, FileEditPatch } from "../types";

/**
 * 用紧凑写法造一个 hunk：每行首字符是 op（" " / "+" / "-"），其余是正文。
 * 行号照 producer 的算法从 oldStart / newStart 起算（internal/pkg/diff）。
 */
function hunk(spec: string[], oldStart = 1, newStart = 1): DiffHunk {
  let oldNo = oldStart;
  let newNo = newStart;
  let oldLines = 0;
  let newLines = 0;
  const lines = spec.map((raw) => {
    const op = raw[0] as " " | "+" | "-";
    const text = raw.slice(1);
    if (op === "+") {
      newLines += 1;
      return { op, new: newNo++, text };
    }
    if (op === "-") {
      oldLines += 1;
      return { op, old: oldNo++, text };
    }
    oldLines += 1;
    newLines += 1;
    return { op, old: oldNo++, new: newNo++, text };
  });
  return { oldStart, oldLines, newStart, newLines, lines };
}

function edit(
  hunks: DiffHunk[],
  extra: Partial<FileEditPatch> = {},
): ReplayCall {
  const plus = hunks.reduce(
    (n, h) => n + h.lines.filter((l) => l.op === "+").length,
    0,
  );
  const minus = hunks.reduce(
    (n, h) => n + h.lines.filter((l) => l.op === "-").length,
    0,
  );
  return {
    kind: "file.edit",
    patch: {
      path: "app/x.ts",
      kind: "modified",
      hunks,
      plus,
      minus,
      ...extra,
    },
  };
}

function write(content: string, truncated = false): ReplayCall {
  const lines =
    content === "" ? 0 : content.replace(/\n$/, "").split("\n").length;
  return {
    kind: "file.write",
    write: {
      path: "app/x.ts",
      content,
      lines,
      bytes: content.length,
      truncated,
    },
  };
}

/** 把重放结果里的 hunk 摊平回紧凑写法，断言才读得懂。 */
function flatten(hunks: DiffHunk[]): string[][] {
  return hunks.map((h) => h.lines.map((l) => `${l.op}${l.text}`));
}

describe("replayPatches", () => {
  // Given 一次 file.edit，When 重放，Then 原样得到那次改动。
  it("replays a single edit into the same diff", () => {
    const result = replayPatches([edit([hunk([" a", "-b", "+B", " c"])])]);

    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(flatten(result.hunks)).toEqual([[" a", "-b", "+B", " c"]]);
    expect(result.plus).toBe(1);
    expect(result.minus).toBe(1);
    expect(result.wholeFileWrite).toBe(false);
    expect(result.truncatedCalls).toEqual([]);
  });

  // Given 两次改到文件的不同位置，When 重放，Then 得到一个 diff 的两段 hunk，
  // 按调用顺序排列（互不相干的改动不该被合并，也不该丢）。
  it("keeps two independent edits as two hunks of one diff", () => {
    const result = replayPatches([
      edit([hunk([" a", "-b", "+B", " c"])]),
      edit([hunk([" x", "-y", "+Y", " z"])]),
    ]);

    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(flatten(result.hunks)).toEqual([
      [" a", "-b", "+B", " c"],
      [" x", "-y", "+Y", " z"],
    ]);
  });

  // 本任务最核心的一条（spec 决策 4）：后一次改的是前一次刚写进去的内容。
  // Given AI 先把 b 改成 B、再把 B 改成 C，When 重放，
  // Then 只剩 b → C 这一条净变更，中间态 B 不出现（拒绝首尾拼接）。
  it("collapses an edit that rewrites what an earlier edit produced", () => {
    const result = replayPatches([
      edit([hunk([" a", "-b", "+B", " c"])]),
      edit([hunk([" a", "-B", "+C", " c"])]),
    ]);

    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(flatten(result.hunks)).toEqual([[" a", "-b", "+C", " c"]]);
    const texts = result.hunks.flatMap((h) => h.lines.map((l) => l.text));
    expect(texts).not.toContain("B");
  });

  // Given 单次 MultiEdit 的第二个 hunk 基于第一个 hunk 改完的内容，
  // When 重放，Then 同样合并（一次调用内部的顺序语义与跨调用一致）。
  it("applies the hunks of one multi-hunk call in order", () => {
    const result = replayPatches([
      edit([hunk([" a", "-b", "+B", " c"]), hunk([" a", "-B", "+C", " c"])]),
    ]);

    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(flatten(result.hunks)).toEqual([[" a", "-b", "+C", " c"]]);
  });

  // Given 先加一行、后又删掉同一行，When 重放，Then 净变更为空 —— 这是真实结果，
  // 不是降级（调用方据此显式说明"互相抵消"，而不是画一个空 diff 当成有内容）。
  it("reports an empty net change when later edits undo earlier ones", () => {
    const result = replayPatches([
      edit([hunk([" a", "+b", " c"])]),
      edit([hunk([" a", "-b", " c"])]),
    ]);

    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(result.hunks).toEqual([]);
    expect(result.plus).toBe(0);
    expect(result.minus).toBe(0);
  });

  // Given 首个操作是全量写入（spec 决策 14），When 重放，
  // Then 整篇按新增，并标注这是一次全量写入。
  it("renders a leading whole-file write as an all-added diff", () => {
    const result = replayPatches([write("one\ntwo\n")]);

    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(flatten(result.hunks)).toEqual([["+one", "+two"]]);
    expect(result.wholeFileWrite).toBe(true);
    expect(result.plus).toBe(2);
    expect(result.minus).toBe(0);
  });

  // Given 全量写入之后又有增量改动，When 重放，Then 绝对状态被改动更新，
  // 整篇仍按新增（写入前的内容依旧无从得知）。
  it("applies later edits on top of a leading whole-file write", () => {
    const result = replayPatches([
      write("one\ntwo\n"),
      edit([hunk([" one", "-two", "+TWO"])]),
    ]);

    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(flatten(result.hunks)).toEqual([["+one", "+TWO"]]);
    expect(result.wholeFileWrite).toBe(true);
  });

  // Given 全量写入之前已经有过增量改动，When 重放，
  // Then 无法合成一个连续 diff（写入前的内容无从得知）：按调用顺序分段降级。
  it("degrades when a whole-file write lands after incremental edits", () => {
    const result = replayPatches([
      edit([hunk([" a", "-b", "+B", " c"])]),
      write("brand\nnew\n"),
    ]);

    expect(result.ok).toBe(false);
    if (result.ok) return;
    expect(result.reason).toBe("writeOverEdits");
    expect(result.segments.map((s) => s.index)).toEqual([0, 1]);
    expect(flatten(result.segments[0].hunks)).toEqual([
      [" a", "-b", "+B", " c"],
    ]);
    expect(flatten(result.segments[1].hunks)).toEqual([["+brand", "+new"]]);
    expect(result.segments[1].wholeFileWrite).toBe(true);
  });

  // Given 绝对状态已知，而某次改动的原文在其中找不到（文件在工具之外被改过），
  // When 重放，Then 降级而不是假装改成功了。
  it("degrades when an edit anchor is absent from the known content", () => {
    const result = replayPatches([
      write("one\ntwo\n"),
      edit([hunk(["-nine", "+NINE"])]),
    ]);

    expect(result.ok).toBe(false);
    if (result.ok) return;
    expect(result.reason).toBe("anchorNotFound");
    expect(result.segments.map((s) => s.index)).toEqual([0, 1]);
  });

  // Given 原文在已知内容里出现多处且不是 replace_all，When 重放，
  // Then 改的是哪一处无从判断：降级，不猜。
  it("degrades when an edit anchor matches more than one place", () => {
    const result = replayPatches([
      write("dup\nmid\ndup\n"),
      edit([hunk(["-dup", "+DUP"])]),
    ]);

    expect(result.ok).toBe(false);
    if (result.ok) return;
    expect(result.reason).toBe("ambiguousAnchor");
  });

  // Given 同样多处匹配但调用带 replaceAll（工具语义就是全替换），
  // When 重放，Then 全部替换且不降级。
  it("replaces every occurrence when the call is a replace-all edit", () => {
    const result = replayPatches([
      write("dup\nmid\ndup\n"),
      edit([hunk(["-dup", "+DUP"])], { replaceAll: true }),
    ]);

    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(flatten(result.hunks)).toEqual([["+DUP", "+mid", "+DUP"]]);
  });

  // Given 最后一次调用被产出方截断，When 重放，
  // Then 仍给出 diff，但明确告知哪一次改动不完整（不静默）。
  it("reports a truncated last call while still replaying it", () => {
    const result = replayPatches([
      edit([hunk([" a", "-b", "+B"])], { truncated: true }),
    ]);

    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(result.truncatedCalls).toEqual([0]);
    expect(result.hunks.length).toBe(1);
  });

  // Given 被截断的调用后面还有别的改动，When 重放，
  // Then 缺段使后续改动无从对上：降级为按调用顺序分段，并指出被截断的是哪一次。
  it("degrades when a truncated call is followed by further changes", () => {
    const result = replayPatches([
      edit([hunk([" a", "-b", "+B"])], { truncated: true }),
      edit([hunk([" x", "-y", "+Y"])]),
    ]);

    expect(result.ok).toBe(false);
    if (result.ok) return;
    expect(result.reason).toBe("truncatedMidway");
    expect(result.truncatedCalls).toEqual([0]);
    expect(result.segments.map((s) => s.truncated)).toEqual([true, false]);
    expect(result.segments.map((s) => s.index)).toEqual([0, 1]);
  });

  // Given 一段合并出来的 hunk，When 读它的行号，Then 与 oldStart / newStart 自洽
  // （合并后行号必须重算，否则渲染出来的 diff 行号是上一次调用的）。
  it("renumbers the merged hunk from its own start lines", () => {
    const result = replayPatches([
      edit([hunk([" a", "-b", "+B", " c"], 10, 20)]),
      edit([hunk([" a", "-B", "+C1", "+C2", " c"], 10, 20)]),
    ]);

    expect(result.ok).toBe(true);
    if (!result.ok) return;
    const [h] = result.hunks;
    expect(h.oldStart).toBe(10);
    expect(h.newStart).toBe(20);
    expect(h.oldLines).toBe(3);
    expect(h.newLines).toBe(4);
    expect(h.lines.map((l) => [l.op, l.old, l.new])).toEqual([
      [" ", 10, 20],
      ["-", 11, undefined],
      ["+", undefined, 21],
      ["+", undefined, 22],
      [" ", 12, 23],
    ]);
  });

  // Given 没有任何调用，When 重放，Then 是空结果而不是失败。
  it("returns an empty replay for no calls at all", () => {
    const result = replayPatches([]);

    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(result.hunks).toEqual([]);
    expect(result.wholeFileWrite).toBe(false);
  });
});
