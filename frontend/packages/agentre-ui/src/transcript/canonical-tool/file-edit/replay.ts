import type { DiffHunk, DiffLine, FileEditPatch, FileWriteDTO } from "../types";

/**
 * 本会话对同一个文件的一次工具调用（按调用先后排列）。两类调用的语义**不同**
 * （spec「本次会话」·「点开一行」）：
 *
 *   - `file.edit` 是**增量**：`hunks` 自带改动前后的内容，直接参与重放。
 *   - `file.write` 是**绝对状态**：该步之后文件内容正好等于 `content`。
 */
export type ReplayCall =
  | { kind: "file.edit"; patch: FileEditPatch }
  | { kind: "file.write"; write: FileWriteDTO };

/**
 * 重放合不成一个连续 diff 的四种原因。每一种在界面上都要说出**为什么**没能合并
 * （spec：失败与降级都必须可见，「空 diff」不是可接受的降级）。
 *
 *   - `writeOverEdits`   增量改动之后又来一次全量写入：写入前的内容无从得知。
 *   - `anchorNotFound`   某次改动的原文在重放出的内容里找不到（文件在工具之外被改过）。
 *   - `ambiguousAnchor`  原文在已知内容里出现多处，改的是哪一处无从判断。
 *   - `truncatedMidway`  被截断的调用后面还有改动：缺的那一段让后续改动对不上。
 */
export type ReplayFailureReason =
  | "writeOverEdits"
  | "anchorNotFound"
  | "ambiguousAnchor"
  | "truncatedMidway";

/** 降级呈现时的一段：一次调用**原样**列出，不与前后合并。 */
export type ReplaySegment = {
  /** 调用在本会话中的序号（0 起），界面上按「第 N 次改动」显示。 */
  index: number;
  hunks: DiffHunk[];
  plus: number;
  minus: number;
  /** 该次调用被产出方截断：这一段改动不完整。 */
  truncated: boolean;
  /** 该段是一次全量写入，未与写入前的内容比较。 */
  wholeFileWrite: boolean;
};

export type ReplayResult =
  | {
      ok: true;
      hunks: DiffHunk[];
      plus: number;
      minus: number;
      /**
       * 首个操作是全量写入：整篇按新增呈现，**未与写入前的内容比较**
       * （spec 决策 14）。调用方必须把这句标注画出来。
       */
      wholeFileWrite: boolean;
      /** 被产出方截断的调用序号（升序）；非空 = 这份 diff 不完整。 */
      truncatedCalls: number[];
    }
  | {
      ok: false;
      reason: ReplayFailureReason;
      segments: ReplaySegment[];
      truncatedCalls: number[];
    };

/**
 * replayPatches 把同一个文件在本会话中的每一次工具调用**按调用先后重放成一个
 * 连续 diff**（spec 决策 4）：呈现「AI 动手之前」到「AI 最后一次动完」的整体差异。
 *
 * ### 为什么靠内容锚定而不是行号
 *
 * claudecode 的 Edit / MultiEdit 与 pi 的 edit 只给 `old_string` / `new_string`，
 * 产出方无从知道它们在原文里的位置，于是 hunk 的行号**统一从 1 起算**
 * （internal/pkg/diff/edit.go 的 FromClaudeCodeEdit）。按行号拼接因此不只是
 * 「后一次的行号基于前一次改完的内容」会错位——它压根就没有真行号可用。
 * 所以这里用**内容**定位：一次改动的原文（hunk 的 context + 删除行）若能在此前
 * 重放出的内容里找到，就地替换，两次改动合并成一处；找不到则说明它动的是文件里
 * 我们还没见过的另一段，作为独立的一段追加。codex 的 unified diff 带真行号，
 * 同一套内容锚定照样成立（context 行就是锚）。
 *
 * ### 未知的部分不需要知道
 *
 * 原始文件的全文永远拿不到（工具只给改动片段）。但**没被改过的行不进 diff**，
 * 所以只需要把「改过的那几段」记住：每段是一对 (改前, 改后)，段与段之间是多少
 * 未知的未改动行并不影响结果。
 */
export function replayPatches(calls: ReplayCall[]): ReplayResult {
  const truncatedCalls = calls.flatMap((call, index) =>
    isTruncated(call) ? [index] : [],
  );
  // 截断的调用后面还有改动：被砍掉的那些行既没进重放、也无从推断，后续改动
  // 落在哪里因此不再可信。不静默合并（spec），直接降级并指出是哪一次被截断。
  if (truncatedCalls.some((index) => index < calls.length - 1)) {
    return degrade("truncatedMidway", calls, truncatedCalls);
  }

  // 两种互斥的重放状态：
  //   - absolute ≠ null —— 首个操作是全量写入，此后**全文已知**，改动就地施加；
  //   - 否则 —— 只知道被改过的若干段（regions），段外的内容一律未知。
  let absolute: string[] | null = null;
  let regions: Region[] = [];
  let wholeFileWrite = false;

  for (const call of calls) {
    if (call.kind === "file.write") {
      // 全量写入不携带写入前的内容。它若不是首个操作，前面那些增量改动的
      // 「改前」与这一篇之间无从对齐，合不出一个连续 diff（spec 决策 14）。
      if (absolute === null && regions.length > 0) {
        return degrade("writeOverEdits", calls, truncatedCalls);
      }
      absolute = splitLines(call.write.content);
      wholeFileWrite = true;
      continue;
    }
    const replaceAll = call.patch.replaceAll === true;
    for (const hunk of call.patch.hunks ?? []) {
      const before = sideOf(hunk, "before");
      const after = sideOf(hunk, "after");
      if (before.length === 0 && after.length === 0) continue;
      if (absolute !== null) {
        const applied = applyToContent(absolute, before, after, replaceAll);
        if (!applied.ok) return degrade(applied.reason, calls, truncatedCalls);
        absolute = applied.lines;
        continue;
      }
      const applied = applyToRegions(regions, before, after, hunk, replaceAll);
      if (!applied.ok) return degrade(applied.reason, calls, truncatedCalls);
      regions = applied.regions;
    }
  }

  if (absolute !== null) {
    // 写入前的内容无从得知，整篇按新增（标注由 wholeFileWrite 带出去）。
    const hunks = absolute.length === 0 ? [] : [allAddedHunk(absolute)];
    return {
      ok: true,
      hunks,
      plus: absolute.length,
      minus: 0,
      wholeFileWrite: true,
      truncatedCalls,
    };
  }

  const hunks = regions
    .map(regionHunk)
    .filter((hunk): hunk is DiffHunk => hunk !== null);
  return {
    ok: true,
    hunks,
    plus: countOp(hunks, "+"),
    minus: countOp(hunks, "-"),
    wholeFileWrite,
    truncatedCalls,
  };
}

/** 一段被改过的区间：改前是什么、现在是什么。段外的内容未知且未被改动。 */
type Region = {
  oldLines: string[];
  newLines: string[];
  oldStart: number;
  newStart: number;
  header?: string;
};

type Applied =
  | { ok: true; lines: string[] }
  | { ok: false; reason: ReplayFailureReason };

type AppliedRegions =
  | { ok: true; regions: Region[] }
  | { ok: false; reason: ReplayFailureReason };

/**
 * applyToContent 在**全文已知**的内容里定位 `before` 并换成 `after`。
 * 找不到锚点就是失败：全文既然已知，对不上只能是文件在工具之外被改过。
 */
function applyToContent(
  lines: string[],
  before: string[],
  after: string[],
  replaceAll: boolean,
): Applied {
  if (before.length === 0) {
    // 无原文可锚（old_string 为空）：只有在文件本身也是空的时候才知道该写到哪。
    if (lines.length === 0) return { ok: true, lines: [...after] };
    return { ok: false, reason: "anchorNotFound" };
  }
  const at = findAll(lines, before);
  if (at.length === 0) return { ok: false, reason: "anchorNotFound" };
  // 多处匹配时改的是哪一处无从判断——除非这次调用本来就是 replace_all，
  // 那时「全部替换」正是工具自己的语义。
  if (at.length > 1 && !replaceAll) {
    return { ok: false, reason: "ambiguousAnchor" };
  }
  return { ok: true, lines: spliceAt(lines, at, before.length, after) };
}

function spliceAt(
  lines: string[],
  at: number[],
  width: number,
  after: string[],
): string[] {
  const next = [...lines];
  // 从后往前替换，前面那些起点才不会被位移。
  for (const start of [...at].reverse()) {
    next.splice(start, width, ...after);
  }
  return next;
}

function applyToRegions(
  regions: Region[],
  before: string[],
  after: string[],
  hunk: DiffHunk,
  replaceAll: boolean,
): AppliedRegions {
  if (before.length > 0) {
    const hits = regions
      .map((region, index) => ({ index, at: findAll(region.newLines, before) }))
      .filter((hit) => hit.at.length > 0);
    const total = hits.reduce((n, hit) => n + hit.at.length, 0);
    if (total > 1 && !replaceAll) {
      return { ok: false, reason: "ambiguousAnchor" };
    }
    if (hits.length > 0) {
      const next = [...regions];
      for (const hit of hits) {
        next[hit.index] = {
          ...next[hit.index],
          newLines: spliceAt(
            next[hit.index].newLines,
            hit.at,
            before.length,
            after,
          ),
        };
      }
      return { ok: true, regions: next };
    }
  }
  // 这次改动落在我们还没见过的一段上：作为独立的一段追加，位置按调用顺序
  // （行号不可信，见 replayPatches 的说明，不据此重排）。
  return {
    ok: true,
    regions: [
      ...regions,
      {
        oldLines: before,
        newLines: after,
        oldStart: hunk.oldStart,
        newStart: hunk.newStart,
        header: hunk.header,
      },
    ],
  };
}

/**
 * 取 hunk 的一侧：`"before"` 是改动前的原文（context + 删除行），
 * `"after"` 是改动后的样子（context + 新增行）。重放靠这两侧做内容锚定。
 */
function sideOf(hunk: DiffHunk, side: "before" | "after"): string[] {
  const drop = side === "before" ? "+" : "-";
  return (hunk.lines ?? [])
    .filter((line) => line.op !== drop)
    .map((line) => line.text);
}

/** findAll 返回 needle 在 hay 里所有不重叠出现的起点（升序）。 */
function findAll(hay: string[], needle: string[]): number[] {
  if (needle.length === 0 || needle.length > hay.length) return [];
  const out: number[] = [];
  for (let i = 0; i + needle.length <= hay.length; i++) {
    let hit = true;
    for (let j = 0; j < needle.length; j++) {
      if (hay[i + j] !== needle[j]) {
        hit = false;
        break;
      }
    }
    if (hit) {
      out.push(i);
      i += needle.length - 1;
    }
  }
  return out;
}

function regionHunk(region: Region): DiffHunk | null {
  const lines = diffLines(
    region.oldLines,
    region.newLines,
    region.oldStart,
    region.newStart,
  );
  if (!lines.some((line) => line.op !== " ")) return null;
  return {
    oldStart: region.oldStart,
    oldLines: region.oldLines.length,
    newStart: region.newStart,
    newLines: region.newLines.length,
    header: region.header,
    lines,
  };
}

function allAddedHunk(lines: string[]): DiffHunk {
  return {
    oldStart: 0,
    oldLines: 0,
    newStart: 1,
    newLines: lines.length,
    lines: lines.map((text, index) => ({
      op: "+" as const,
      new: index + 1,
      text,
    })),
  };
}

function countOp(hunks: DiffHunk[], op: "+" | "-"): number {
  return hunks.reduce(
    (total, hunk) => total + hunk.lines.filter((l) => l.op === op).length,
    0,
  );
}

function isTruncated(call: ReplayCall): boolean {
  return call.kind === "file.edit"
    ? call.patch.truncated === true
    : call.write.truncated === true;
}

function degrade(
  reason: ReplayFailureReason,
  calls: ReplayCall[],
  truncatedCalls: number[],
): ReplayResult {
  return {
    ok: false,
    reason,
    truncatedCalls,
    segments: calls.map((call, index) => {
      const truncated = isTruncated(call);
      if (call.kind === "file.edit") {
        const hunks = call.patch.hunks ?? [];
        return {
          index,
          hunks,
          plus: call.patch.plus ?? countOp(hunks, "+"),
          minus: call.patch.minus ?? countOp(hunks, "-"),
          truncated,
          wholeFileWrite: false,
        };
      }
      const lines = splitLines(call.write.content);
      return {
        index,
        hunks: lines.length === 0 ? [] : [allAddedHunk(lines)],
        plus: lines.length,
        minus: 0,
        truncated,
        wholeFileWrite: true,
      };
    }),
  };
}

/**
 * splitLines 与产出方的 `splitLinesNoTrailingEmpty` 同一口径
 * （internal/pkg/diff/edit.go）：末尾换行不算一行空行，`\r\n` 归一成 `\n`。
 * 两边不一致，全量写入的内容就对不上增量改动里的同一行文本。
 */
function splitLines(content: string): string[] {
  if (content === "") return [];
  const parts = content.split("\n");
  if (parts[parts.length - 1] === "") parts.pop();
  return parts.map((line) => (line.endsWith("\r") ? line.slice(0, -1) : line));
}

// LCS 表的规模上限。超过就退回「整段替换」：合并出来的段落理论上可以随调用次数
// 增长，一个 O(m·n) 的表不能没有天花板。
const LCS_CELL_LIMIT = 1_000_000;

/**
 * diffLines 是重放结果的行级 diff，与产出方的 LCS 实现同构
 * （internal/pkg/diff/edit.go 的 diffLines）：同一处改动在「单次调用直接渲染」
 * 与「多次调用重放合并」两条路径下形状一致。行号按段自己的起点重算——合并之后
 * 沿用某一次调用的行号就是错的。
 */
function diffLines(
  oldLines: string[],
  newLines: string[],
  oldStart: number,
  newStart: number,
): DiffLine[] {
  // 相同的首尾直接算 context：既省掉大表，也让合并后的段落不会因为 LCS 的另一种
  // 等价对齐而抖动。
  let head = 0;
  while (
    head < oldLines.length &&
    head < newLines.length &&
    oldLines[head] === newLines[head]
  ) {
    head++;
  }
  let tail = 0;
  while (
    tail < oldLines.length - head &&
    tail < newLines.length - head &&
    oldLines[oldLines.length - 1 - tail] ===
      newLines[newLines.length - 1 - tail]
  ) {
    tail++;
  }
  const midOld = oldLines.slice(head, oldLines.length - tail);
  const midNew = newLines.slice(head, newLines.length - tail);

  const middle =
    midOld.length * midNew.length > LCS_CELL_LIMIT
      ? [
          ...midOld.map((text) => ({ op: "-" as const, text })),
          ...midNew.map((text) => ({ op: "+" as const, text })),
        ]
      : lcsDiff(midOld, midNew);

  const ops: { op: " " | "+" | "-"; text: string }[] = [
    ...oldLines.slice(0, head).map((text) => ({ op: " " as const, text })),
    ...middle,
    ...oldLines
      .slice(oldLines.length - tail)
      .map((text) => ({ op: " " as const, text })),
  ];

  let oldNo = oldStart;
  let newNo = newStart;
  return ops.map(({ op, text }) => {
    if (op === "+") return { op, new: newNo++, text };
    if (op === "-") return { op, old: oldNo++, text };
    return { op, old: oldNo++, new: newNo++, text };
  });
}

function lcsDiff(
  oldLines: string[],
  newLines: string[],
): { op: " " | "+" | "-"; text: string }[] {
  const m = oldLines.length;
  const n = newLines.length;
  if (m === 0 && n === 0) return [];
  const lcs: number[][] = Array.from({ length: m + 1 }, () =>
    new Array<number>(n + 1).fill(0),
  );
  for (let i = 1; i <= m; i++) {
    for (let j = 1; j <= n; j++) {
      lcs[i][j] =
        oldLines[i - 1] === newLines[j - 1]
          ? lcs[i - 1][j - 1] + 1
          : Math.max(lcs[i - 1][j], lcs[i][j - 1]);
    }
  }
  const reversed: { op: " " | "+" | "-"; text: string }[] = [];
  let i = m;
  let j = n;
  while (i > 0 || j > 0) {
    if (i > 0 && j > 0 && oldLines[i - 1] === newLines[j - 1]) {
      reversed.push({ op: " ", text: oldLines[i - 1] });
      i--;
      j--;
      continue;
    }
    if (j > 0 && (i === 0 || lcs[i][j - 1] >= lcs[i - 1][j])) {
      reversed.push({ op: "+", text: newLines[j - 1] });
      j--;
      continue;
    }
    reversed.push({ op: "-", text: oldLines[i - 1] });
    i--;
  }
  return reversed.reverse();
}
