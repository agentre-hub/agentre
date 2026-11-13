// parse.ts —— 「这条 shell 调用是 agent 的一次记忆写入」的判据与结果行解析。
//
// 结果行是 agrctl 稳定输出的第 1 行（internal/cli/ctlcmd/memory.go 文件头写明「对话里的
// 记忆卡片据此识别」）：
//
//   memory m<id> <created|already exists|updated|archived> (<层>)
//
// <层> 是 global，或 department <路径> / agent <名字> / project <路径>。超预算时还有第 2 行
// `over budget: ...`，卡片不用它。
import type { TranscriptBlock } from "../../dto";
import { unwrapShellCommand } from "../shell-command";
import { commandResultOf, isFailedCommandResult } from "../command-result";

export type MemoryWriteAction = "created" | "updated" | "archived" | "exists";

export type MemoryWriteLayer =
  | { kind: "global" }
  | { kind: "agent" }
  | { kind: "department"; name: string }
  | { kind: "project"; name: string };

export type MemoryWrite = {
  id: number;
  action: MemoryWriteAction;
  layer: MemoryWriteLayer;
};

// 命令的开头必须是 agrctl（行首或 && ; | 之后，可带目录前缀），免得 echo "agrctl create memory"
// 之类误判。codex 会套一层 `zsh -lc "…"`，判据前先用 unwrapShellCommand 剥掉。
const WRITE_COMMAND =
  /(?:^|[;&|(]\s*|&&\s*)(?:[^\s;&|()'"]*\/)?agrctl\s+(?:create|update|delete)\s+memor(?:y|ies)\b/m;

export function isMemoryWriteCall(block: TranscriptBlock): boolean {
  const command = (block.toolInput as { command?: unknown } | undefined)
    ?.command;
  return (
    typeof command === "string" &&
    WRITE_COMMAND.test(unwrapShellCommand(command))
  );
}

const RESULT_LINE =
  /^memory m(\d+) (created|already exists|updated|archived) \((.+)\)\s*$/;

const ACTIONS: Record<string, MemoryWriteAction> = {
  created: "created",
  updated: "updated",
  archived: "archived",
  "already exists": "exists",
};

function parseLayer(text: string): MemoryWriteLayer | null {
  if (text === "global") return { kind: "global" };
  const space = text.indexOf(" ");
  const kind = space < 0 ? text : text.slice(0, space);
  const name = space < 0 ? "" : text.slice(space + 1).trim();
  if (kind === "agent") return { kind: "agent" };
  if ((kind === "department" || kind === "project") && name) {
    return { kind, name };
  }
  return null;
}

/** 没有结果、命令失败或结果行对不上时返回 null（调用方退回通用呈现）。 */
export function parseMemoryWrite(
  _toolBlock: TranscriptBlock,
  resultBlock?: TranscriptBlock,
): MemoryWrite | null {
  if (!resultBlock || resultBlock.isError) return null;
  const command = commandResultOf(resultBlock);
  if (isFailedCommandResult(command)) return null;
  const output = command ? command.output : (resultBlock.text ?? "");
  const first = output.split("\n", 1)[0] ?? "";
  const match = RESULT_LINE.exec(first);
  if (!match) return null;
  const layer = parseLayer(match[3]);
  if (!layer) return null;
  return { id: Number(match[1]), action: ACTIONS[match[2]], layer };
}
