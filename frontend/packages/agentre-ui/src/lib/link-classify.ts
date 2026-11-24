/**
 * 会话的一个工作根（spec 2026-11-24-chat-right-toolwindows「文件链接」）。`primary`
 * 标出会话主根；`name` 是悬浮卡上的根名标签，缺省时用路径最后一段。
 */
export type LinkRoot = { path: string; name?: string; primary?: boolean };

/**
 * `cwd`：相对路径补全用的目录（「AI 当时所在目录」）。`roots`：宿主给出的已认领
 * 工作根；给了它，「内部」就改判为「落在某个根之内」，归属包含它的最深的根，
 * `relPath` 相对那个根。不给时与只传 cwd 字符串完全相同（今天的行为）。
 */
export type ClassifyLinkOptions = {
  cwd?: string;
  roots?: readonly LinkRoot[];
};

export type LinkClass =
  | { kind: "url"; url: string }
  | {
      kind: "local-internal";
      fullPath: string;
      pathKind: LocalPathKind;
      relPath: string;
      /** 只在调用方给了 `roots` 时出现：这条路径归属的工作根。 */
      root?: LinkRoot;
      line?: number;
      endLine?: number;
      col?: number;
    }
  | {
      kind: "local-external";
      fullPath: string;
      pathKind: LocalPathKind;
      line?: number;
      endLine?: number;
      col?: number;
    }
  | { kind: "unknown"; href: string };

export type LocalPathKind = "file" | "folder";

const URL_PREFIX = /^(https?:|mailto:|tel:)/i;
const WWW_PREFIX = /^www\./i;
const ABS_POSIX = /^\//;
const ABS_WINDOWS = /^[A-Za-z]:[\\/]/;
// 家目录锚定形式。只认 "~" 与 "~/…"（"~alice/…" 指的是别人的家目录，前端无从
// 解析，仍按老规则处理）。
export const HOME_ANCHORED = /^~(?:$|[\\/])/;
const SCHEME = /^[A-Za-z][A-Za-z0-9+.-]*:/;
// 行号后缀:":42" / ":42:7" / ":311-330"。范围与列号互斥——":311-330:5" 不是
// 任何工具写得出的形式,认了只会让文法更松。整段后缀一律从路径上剥掉,否则会被
// 当成文件名的一部分拼进 cwd,打开时必然 ENOENT。
const LINE_SUFFIX = /:(\d+)(?:-(\d+)|:(\d+))?$/;

function stripLineSuffix(p: string): {
  path: string;
  line?: number;
  endLine?: number;
  col?: number;
} {
  const m = LINE_SUFFIX.exec(p);
  if (!m) return { path: p };
  return {
    path: p.slice(0, m.index),
    line: parseInt(m[1], 10),
    endLine: m[2] !== undefined ? parseInt(m[2], 10) : undefined,
    col: m[3] !== undefined ? parseInt(m[3], 10) : undefined,
  };
}

function decodeLocalPath(path: string): string {
  try {
    return decodeURIComponent(path);
  } catch {
    return path;
  }
}

export function isLocalFileURL(href: string): boolean {
  return /^file:\/\/(?:localhost\/|\/(?!\/))/i.test(href);
}

function fileURLToPath(href: string): string {
  // file:///Users/x/foo.go → /Users/x/foo.go
  // file://localhost/Users/x/foo.go → /Users/x/foo.go
  // file:///C:/Users/x/foo.go → C:/Users/x/foo.go
  let path = href
    .replace(/^file:\/\/localhost/i, "file://")
    .slice("file://".length);
  if (path.startsWith("/") && /^[A-Za-z]:/.test(path.slice(1))) {
    path = path.slice(1);
  }
  return decodeLocalPath(path);
}

function classifyLocalPathKind(
  fullPath: string,
  cwd?: string,
  relativeSource?: string,
): LocalPathKind {
  if (cwd && fullPath === cwd) return "folder";
  if (fullPath === "~") return "folder";
  if (/[\\/]$/.test(fullPath)) return "folder";
  if (relativeSource) {
    const base = relativeSource.split(/[\\/]/).pop() ?? "";
    if (base !== "" && !base.includes(".")) return "folder";
  }
  return "file";
}

function resolveRelativePath(path: string, cwd: string): string {
  const windows = ABS_WINDOWS.test(cwd);
  const separator = windows ? "\\" : "/";
  const normalizedCwd = cwd.replace(/[\\/]+/g, separator);
  const normalizedPath = path.replace(/[\\/]+/g, separator);
  const trailingSeparator = /[\\/]$/.test(path);

  let root = separator;
  let cwdRest: string;
  if (windows) {
    root = normalizedCwd.slice(0, 2);
    cwdRest = normalizedCwd.slice(2).replace(/^\\+/, "");
  } else {
    cwdRest = normalizedCwd.replace(/^\/+/, "");
  }

  const parts = cwdRest.split(separator).filter(Boolean);
  for (const part of normalizedPath.split(separator)) {
    if (part === "" || part === ".") continue;
    if (part === "..") {
      parts.pop();
      continue;
    }
    parts.push(part);
  }

  const joined = parts.join(separator);
  const fullPath = windows
    ? `${root}${joined ? separator + joined : separator}`
    : `${root}${joined}`;
  return trailingSeparator && !fullPath.endsWith(separator)
    ? `${fullPath}${separator}`
    : fullPath;
}

function internalRelativePath(fullPath: string, cwd: string): string | null {
  const windows = ABS_WINDOWS.test(cwd);
  const normalizedFull = fullPath.replace(/\\/g, "/");
  const normalizedCwd = cwd.replace(/\\/g, "/").replace(/\/$/, "");
  const comparableFull = windows
    ? normalizedFull.toLowerCase()
    : normalizedFull;
  const comparableCwd = windows ? normalizedCwd.toLowerCase() : normalizedCwd;
  if (comparableFull === comparableCwd) return "";
  if (!comparableFull.startsWith(`${comparableCwd}/`)) return null;
  return normalizedFull.slice(normalizedCwd.length + 1);
}

/**
 * 包含 `fullPath` 的最深的根，以及路径相对它的部分。没有根包含它时为 null。
 */
function owningRoot(
  fullPath: string,
  roots: readonly LinkRoot[],
): { root: LinkRoot; relPath: string } | null {
  let best: { root: LinkRoot; relPath: string } | null = null;
  for (const root of roots) {
    if (!root.path) continue;
    const relPath = internalRelativePath(fullPath, root.path);
    if (relPath === null) continue;
    if (best === null || relPath.length < best.relPath.length) {
      best = { root, relPath };
    }
  }
  return best;
}

/**
 * 这条路径归谁：给了根就归最深的根，没给就看在不在 cwd 之内（今天的口径）。
 * 家目录形式无法与 cwd / 根比较（它们是展开后的绝对路径），一律当作外部目标。
 */
function ownerOf(
  fullPath: string,
  cwd: string | undefined,
  roots: readonly LinkRoot[] | undefined,
): { root?: LinkRoot; relPath: string } | null {
  if (HOME_ANCHORED.test(fullPath)) return null;
  if (roots) return owningRoot(fullPath, roots);
  if (!cwd) return null;
  const relPath = internalRelativePath(fullPath, cwd);
  return relPath === null ? null : { relPath };
}

export function classifyLink(
  href: string | undefined,
  cwdOrOptions?: string | ClassifyLinkOptions,
): LinkClass {
  const { cwd, roots } =
    typeof cwdOrOptions === "object"
      ? cwdOrOptions
      : { cwd: cwdOrOptions, roots: undefined };
  if (!href) return { kind: "unknown", href: "" };

  if (URL_PREFIX.test(href)) return { kind: "url", url: href };
  if (WWW_PREFIX.test(href)) return { kind: "url", url: `http://${href}` };

  let rawPath: string;
  let relativeSource: string | undefined;
  if (isLocalFileURL(href)) {
    rawPath = fileURLToPath(href);
  } else if (
    ABS_POSIX.test(href) ||
    ABS_WINDOWS.test(href) ||
    HOME_ANCHORED.test(href)
  ) {
    // 家目录形式与绝对路径同一档：不拼到 cwd 上（那会造出
    // "<cwd>/~/Code/foo.ts" 这种不存在的路径），原样保留 "~/…" 展示，
    // 展开成真实家目录由宿主的 openPath 负责（前端拿不到家目录）。
    rawPath = decodeLocalPath(href);
  } else if (cwd && !SCHEME.test(href) && !href.startsWith("#")) {
    const relative = stripLineSuffix(decodeLocalPath(href));
    relativeSource = relative.path;
    rawPath = resolveRelativePath(relative.path, cwd);
    if (relative.line !== undefined) rawPath += `:${relative.line}`;
    if (relative.endLine !== undefined) rawPath += `-${relative.endLine}`;
    if (relative.col !== undefined) rawPath += `:${relative.col}`;
  } else {
    return { kind: "unknown", href };
  }

  const { path: fullPath, line, endLine, col } = stripLineSuffix(rawPath);

  const owner = ownerOf(fullPath, cwd, roots);
  const pathKind =
    owner?.root && owner.relPath === ""
      ? "folder"
      : classifyLocalPathKind(fullPath, cwd, relativeSource);
  if (owner !== null) {
    return {
      kind: "local-internal",
      fullPath,
      pathKind,
      relPath: owner.relPath,
      ...(owner.root ? { root: owner.root } : {}),
      ...(line !== undefined ? { line } : {}),
      ...(endLine !== undefined ? { endLine } : {}),
      ...(col !== undefined ? { col } : {}),
    };
  }

  return {
    kind: "local-external",
    fullPath,
    pathKind,
    ...(line !== undefined ? { line } : {}),
    ...(endLine !== undefined ? { endLine } : {}),
    ...(col !== undefined ? { col } : {}),
  };
}
