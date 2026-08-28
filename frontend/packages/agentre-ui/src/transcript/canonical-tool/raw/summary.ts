// summary.ts — 通用工具的兜底摘要。canonical 卡识别的工具(Write/Edit/AskUserQuestion 等)
// 走专用卡;不进 canonical 集合的(Bash/Read/MCP 等)走 RawToolCard,用本文件的 input shape
// 探测出一个简短摘要。**不依赖工具名硬集合** —— 那是 backend-specific 知识泄漏。

export type SummarizeOptions = { cwd?: string };

const COMMAND_KEYS = ["command", "cmd"];
const PATH_KEYS = ["path", "file_path", "file", "filename"];
const PATTERN_KEYS = ["pattern", "query"];

export function summarizeRawTool(
  _toolName: string,
  input?: Record<string, unknown>,
  opts: SummarizeOptions = {},
): string {
  if (!input) return "";
  const cmd = pickString(input, COMMAND_KEYS);
  if (cmd) return formatCommandExecutionCommand(cmd);
  const path = pickString(input, PATH_KEYS);
  if (path) return relativizePath(path, opts.cwd);
  const pattern = pickString(input, PATTERN_KEYS);
  if (pattern) return pattern;
  const entries = Object.entries(input).filter(
    ([, v]) => v != null && v !== "",
  );
  if (!entries.length) return "";
  const [key, value] = entries[0];
  return `${key}=${stringifyValue(value)}`;
}

export function formatCommandExecutionCommand(command: string): string {
  const normalized = command.replace(/\s+/g, " ").trim();
  const shellWrapped = normalized.match(
    /^(?:\/[^\s]+\/)?(?:zsh|bash|sh)\s+-lc\s+(['"])([\s\S]*)\1$/,
  );
  if (!shellWrapped) return normalized;
  const [, quote, inner] = shellWrapped;
  return inner.replace(new RegExp(`\\\\${quote}`, "g"), quote).trim();
}

function pickString(input: Record<string, unknown>, keys: string[]): string {
  for (const k of keys) {
    const v = input[k];
    if (typeof v === "string" && v.length > 0) return v;
  }
  return "";
}

function stringifyValue(value: unknown): string {
  if (value == null) return "";
  if (typeof value === "string") return value.replace(/\s+/g, " ").trim();
  if (typeof value === "number" || typeof value === "boolean")
    return String(value);
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

// relativizePath 导出给活动行的行首标签复用 —— 摘要里的路径与这里的写法必须一致,
// 不然同一个文件在工具卡与活动行里显示成两种路径。
export function relativizePath(path: string, cwd?: string): string {
  if (!cwd) return path;
  const cwdTrimmed = cwd.replace(/\/+$/, "");
  if (path === cwdTrimmed) return "./";
  if (path.startsWith(cwdTrimmed + "/")) {
    return "./" + path.slice(cwdTrimmed.length + 1);
  }
  return path;
}
