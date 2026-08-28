// tier.ts — 一步工具调用落在哪一档视觉权重的纯判据。按顺序取第一个命中:
// ① canonical.kind(后端已算好)② input shape(与 raw/summary.ts 同源探测)
// ③ 都不匹配落中性层。**不依赖工具名硬集合** —— 那是 backend-specific 知识泄漏
// (raw/summary.ts 开头的规矩,本文件沿用)。
//
// 中性层是关键分支:认不出来的工具不假装它是只读,不能给它读层的轻量级视觉权重。

import type { TranscriptBlock } from "../dto";

import type { CanonicalDTO, CanonicalKind } from "./types";

export type Tier = "out" | "write" | "read" | "neutral";

const OUT_KINDS = new Set<CanonicalKind>([
  "agent.spawn",
  "user.ask",
  "plan.approve_request",
  "tool.permission",
]);

const WRITE_KINDS = new Set<CanonicalKind>(["file.write", "file.edit"]);

const COMMAND_KEYS = ["command", "cmd"];
const WRITE_SHAPE_KEYS = ["content", "edits", "old_string"];
// 定位语义字段与 raw/summary.ts 的 PATH_KEYS / PATTERN_KEYS 同源(判据②要求
// 「与 summary.ts 的探测同源」)。只认 path 会漏掉 claudecode 的 Read
// ({file_path, offset, limit}) —— 最常见的只读探查会被当成认不出来的工具落进
// 中性层,组头把它算进「其它」而不是「查阅」。写入语义字段先判,所以 Write 的
// {file_path, content} 仍是写层。
const READ_SHAPE_KEYS = [
  "path",
  "file_path",
  "file",
  "filename",
  "pattern",
  "query",
  "url",
];

export function tier(block: TranscriptBlock): Tier {
  // block.canonical 的生成类型(wailsjs view.CanonicalDTO)把 kind 宽化成 string;
  // 这里借道本地 CanonicalDTO 拿回字面量联合类型,与其余 canonical-tool 卡片同一手法
  // (如 file-write/card.tsx:28、agent-spawn/card.tsx:51)。
  const kind = (block as { canonical?: CanonicalDTO }).canonical?.kind;
  if (kind) {
    if (OUT_KINDS.has(kind)) return "out";
    if (WRITE_KINDS.has(kind)) return "write";
  }

  const input = block.toolInput as Record<string, unknown> | undefined;
  if (input) {
    if (hasAnyKey(input, COMMAND_KEYS)) return "write";
    if (hasAnyKey(input, WRITE_SHAPE_KEYS)) return "write";
    if (hasAnyKey(input, READ_SHAPE_KEYS)) return "read";
  }

  return "neutral";
}

function hasAnyKey(input: Record<string, unknown>, keys: string[]): boolean {
  return keys.some((k) => input[k] != null);
}

// ToolCategory 比 Tier 细一档 —— 活动块组头要把写层再分「改文件 / 写入 / 命令」
// (组头固定顺序 思考 / 查阅 / 改 N 个文件 / 写入 / 命令 / 其它 / N 失败)。
// 判据顺序与 tier 完全一致:canonical.kind → input shape → 中性兜底,同样不查
// 工具名表。出组项(tier === "out")不进活动块,调用方先用 tier 过滤;真传进来
// 也只会落 "other",不会被谎报成读层。
export type ToolCategory = "read" | "edit" | "write" | "command" | "other";

export function toolCategory(block: TranscriptBlock): ToolCategory {
  // 档位仍由 tier 独家判定 —— 这里只在写层内部再分岔,不另起一套判据。
  const t = tier(block);
  if (t === "out") return "other";

  const kind = (block as { canonical?: CanonicalDTO }).canonical?.kind;
  if (kind === "file.edit") return "edit";
  if (kind === "file.write") return "write";

  const input = block.toolInput as Record<string, unknown> | undefined;
  if (t === "write" && input && hasAnyKey(input, COMMAND_KEYS))
    return "command";
  if (t === "write") return "write";
  return t === "read" ? "read" : "other";
}

// displayName 把 `mcp__<server>__<tool>` 形态拆成 "server · tool";其余原样返回。
export function displayName(toolName: string): string {
  const prefix = "mcp__";
  if (!toolName.startsWith(prefix)) return toolName;
  const rest = toolName.slice(prefix.length);
  const sep = rest.indexOf("__");
  if (sep === -1) return toolName;
  const server = rest.slice(0, sep);
  const tool = rest.slice(sep + 2);
  if (!server || !tool) return toolName;
  return `${server} · ${tool}`;
}
