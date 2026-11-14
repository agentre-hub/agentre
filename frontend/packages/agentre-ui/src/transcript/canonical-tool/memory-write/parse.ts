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
import {
  programName,
  splitShellCommands,
  unwrapShellCommand,
} from "../shell-command";
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
  /** 触发这次写入的命令里 --summary 的值；条目已删（新增后被撤销）时卡片靠它继续显示摘要。 */
  summary?: string;
};

// 一条简单命令的程序是 agrctl（可带目录前缀、Windows 上是 agrctl.exe，前面可有 FOO=1 这样的
// 环境变量赋值），后面紧跟写动词与 memory。按 shell 的引号读：引号里、注释里的
// "; agrctl create memory" 不是调用，引起来或转义空格的路径（macOS 上 agrctl 装在
// ~/Library/Application Support/… 下）是。codex 套的 `zsh -lc "…"` 先由 unwrapShellCommand 剥掉。
const AGRCTL = new Set(["agrctl", "agrctl.exe"]);
const WRITE_VERBS = new Set(["create", "update", "delete"]);
const MEMORY_KINDS = new Set(["memory", "memories"]); // agrctl 认名字不分大小写
const ENV_ASSIGNMENT = /^[A-Za-z_][A-Za-z0-9_]*=/;

function isMemoryWriteWords(words: string[]): boolean {
  const start = words.findIndex((word) => !ENV_ASSIGNMENT.test(word));
  if (start < 0) return false;
  const [program, verb, kind] = words.slice(start, start + 3);
  return (
    AGRCTL.has(programName(program)) &&
    WRITE_VERBS.has(verb) &&
    kind !== undefined &&
    MEMORY_KINDS.has(kind.toLowerCase())
  );
}

/**
 * 命令里第一条记忆写入的 --summary（结果行也是第一条写入的）；没有则 undefined。
 * 与 agrctl 读 flag 一致（ctlcmd/args.go parseArgs）：前面几个 "-" 都行，值跟在后面或 "=" 之后。
 */
function summaryOfFirstWrite(block: TranscriptBlock): string | undefined {
  const command = (block.toolInput as { command?: unknown } | undefined)
    ?.command;
  if (typeof command !== "string") return undefined;
  const commands = splitShellCommands(unwrapShellCommand(command));
  const words = commands?.find(isMemoryWriteWords);
  if (!words) return undefined;
  for (let i = 0; i < words.length; i++) {
    if (!words[i].startsWith("-")) continue;
    const flag = words[i].replace(/^-+/, "");
    if (flag === "summary") return words[i + 1];
    if (flag.startsWith("summary=")) return flag.slice("summary=".length);
  }
  return undefined;
}

export function isMemoryWriteCall(block: TranscriptBlock): boolean {
  const command = (block.toolInput as { command?: unknown } | undefined)
    ?.command;
  if (typeof command !== "string" || !command.includes("agrctl")) return false;
  const commands = splitShellCommands(unwrapShellCommand(command));
  return commands !== null && commands.some(isMemoryWriteWords);
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
  toolBlock: TranscriptBlock,
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
  const summary = summaryOfFirstWrite(toolBlock);
  return {
    id: Number(match[1]),
    action: ACTIONS[match[2]],
    layer,
    ...(summary ? { summary } : {}),
  };
}
