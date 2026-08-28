import { resolveToolPathInRoot } from "../../../lib/work-root-path";
import type { DiffHunk as WideDiffHunk, TranscriptMessage } from "../../dto";
import type { DiffHunk, DiffOp, FileChangeKind, FileEditPatch } from "../types";

import type { ReplayCall } from "./replay";

const EDIT_KINDS: ReadonlySet<string> = new Set([
  "created",
  "modified",
  "deleted",
]);

const DIFF_OPS: ReadonlySet<string> = new Set([" ", "+", "-"]);

/**
 * narrowHunks 是宽边界（`dto.ts` 逐字对齐 Wails 生成类型，`op` 是 `string`）到
 * 渲染层窄视图（`op` 是 `" " | "+" | "-"`）的那一次收窄，见 types.ts 顶部的说明。
 *
 * 后端多发一个没见过的 op 时按**上下文行**处理：它既不该记进加数也不该记进减数，
 * 静默当成新增会让重放出的 `±N` 与行上的数字对不上。
 */
function narrowHunks(hunks: WideDiffHunk[] | undefined): DiffHunk[] {
  return (hunks ?? []).map((hunk) => ({
    ...hunk,
    lines: (hunk.lines ?? []).map((line) => ({
      ...line,
      op: (DIFF_OPS.has(line.op) ? line.op : " ") as DiffOp,
    })),
  }));
}

/**
 * collectReplayCalls 从会话消息里挑出**某一个文件**的每一次 `file.edit` /
 * `file.write` 调用，按调用先后交给 `replayPatches`。
 *
 * 归属判定与「变更」页列行时同一口径（把工具给的路径解析一次，落在工作根子树
 * 之外的一律不算），否则点开的 diff 会比那一行少掉几次调用：同一个文件被绝对
 * 与相对两种写法改过时，行只有一条，diff 也必须两次都算进去。
 *
 * 只读消息里的 canonical 块、不读 git —— AI 中途提交、事后 rebase 或 amend 都
 * 不影响结果（spec「与『有没有提交』无关」）。
 */
export function collectReplayCalls(
  messages: TranscriptMessage[],
  root: string,
  relPath: string,
): ReplayCall[] {
  const calls: ReplayCall[] = [];
  for (const message of messages) {
    for (const block of message.blocks ?? []) {
      const canonical = block.canonical;
      if (!canonical) continue;
      if (canonical.kind === "file.edit") {
        for (const file of canonical.fileEdit?.files ?? []) {
          if (resolveToolPathInRoot(file.path, root) !== relPath) continue;
          calls.push({
            kind: "file.edit",
            patch: {
              path: file.path,
              // 后端多发一个没见过的 kind 时按「已修改」兜底，与行的状态同源。
              kind: (EDIT_KINDS.has(file.kind)
                ? file.kind
                : "modified") as FileChangeKind,
              hunks: narrowHunks(file.hunks),
              plus: file.plus ?? 0,
              minus: file.minus ?? 0,
              truncated: file.truncated,
              replaceAll: file.replaceAll,
            } satisfies FileEditPatch,
          });
        }
        continue;
      }
      const write = canonical.fileWrite;
      if (canonical.kind !== "file.write" || !write) continue;
      if (resolveToolPathInRoot(write.path, root) !== relPath) continue;
      calls.push({ kind: "file.write", write });
    }
  }
  return calls;
}
