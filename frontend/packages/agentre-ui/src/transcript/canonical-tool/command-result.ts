// command-result.ts —— 命令类工具结果的唯一解析层。
//
// command_execution 形态的工具(codex 的 command_execution、claudecode 的 Bash、
// piagent 的 bash)把结果写成 JSON {exitCode, output, status},其余工具的
// tool_result 是纯文本。**靠 result shape 判定,不靠 toolName** —— 那是
// backend-specific 知识泄漏(raw/summary.ts 立下的规矩)。
//
// 这份解析同时被三处消费:RawToolCard 的错误染色 / 活动行的行内事实
// (activity-block/facts.ts)/ 组头的失败计数(transcript-rows 的 isFailedStep)。
// 各写一份必然漂移:失败判据一旦只留在其中一处,另外两处就会把一条 exit 1 的命令
// 当成功渲染。

import type { TranscriptBlock } from "../dto";

export type CommandResult = {
  exitCode?: number;
  output: string;
  status?: string;
};

export function parseCommandResult(text?: string): CommandResult | null {
  if (typeof text !== "string") return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return null;
  }
  if (!parsed || typeof parsed !== "object") return null;
  const data = parsed as Record<string, unknown>;
  if (!("output" in data) && !("exitCode" in data) && !("status" in data)) {
    return null;
  }
  return {
    exitCode: typeof data.exitCode === "number" ? data.exitCode : undefined,
    output: stringifyOutput(data.output),
    status: typeof data.status === "string" ? data.status : undefined,
  };
}

// isFailedCommandResult —— 「这条命令失败了没有」。**不能只看 tool_result.isError**:
// isError 跟的是 item 自身是否出错(codex translator 的 IsError = ev.Tool.Err != nil,
// 而 Tool.Err 只在 item.Error 存在或 item.Status == failed 时非空),一条正常跑完
// 但退出码非零的命令 isError 是 false,失败信号只在这段 JSON 里。
export function isFailedCommandResult(result: CommandResult | null): boolean {
  if (!result) return false;
  if (typeof result.exitCode === "number" && result.exitCode !== 0) return true;
  return (
    result.status === "failed" ||
    result.status === "error" ||
    result.status === "interrupted"
  );
}

// commandResultOf 按结果块缓存解析结果:同一个块会被组头汇总(流式里每 chunk 一次)、
// 活动行事实与 RawToolCard 各要一次,而解析要把整段结果(可能几 MB)JSON.parse 一遍。
// 块是不可变的(store 更新一律换新对象),旧条目随块一起被 GC。
const cache = new WeakMap<TranscriptBlock, CommandResult | null>();

export function commandResultOf(block?: TranscriptBlock): CommandResult | null {
  if (!block) return null;
  const hit = cache.get(block);
  if (hit !== undefined) return hit;
  const parsed = parseCommandResult(block.text);
  cache.set(block, parsed);
  return parsed;
}

function stringifyOutput(output: unknown): string {
  if (output == null) return "";
  if (typeof output === "string") return output;
  try {
    return JSON.stringify(output, null, 2);
  } catch {
    return String(output);
  }
}
