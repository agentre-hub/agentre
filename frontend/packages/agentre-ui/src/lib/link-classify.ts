export type LinkClass =
  | { kind: "url"; url: string }
  | {
      kind: "local-internal";
      fullPath: string;
      pathKind: LocalPathKind;
      relPath: string;
      line?: number;
      col?: number;
    }
  | {
      kind: "local-external";
      fullPath: string;
      pathKind: LocalPathKind;
      line?: number;
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
const LINE_SUFFIX = /:(\d+)(?::(\d+))?$/;

function stripLineSuffix(p: string): {
  path: string;
  line?: number;
  col?: number;
} {
  const m = LINE_SUFFIX.exec(p);
  if (!m) return { path: p };
  return {
    path: p.slice(0, m.index),
    line: parseInt(m[1], 10),
    col: m[2] !== undefined ? parseInt(m[2], 10) : undefined,
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

export function classifyLink(
  href: string | undefined,
  cwd?: string,
): LinkClass {
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
    if (relative.col !== undefined) rawPath += `:${relative.col}`;
  } else {
    return { kind: "unknown", href };
  }

  const { path: fullPath, line, col } = stripLineSuffix(rawPath);
  const pathKind = classifyLocalPathKind(fullPath, cwd, relativeSource);

  // 家目录形式无法与 cwd 比较（cwd 是展开后的绝对路径），一律当作 cwd 外目标。
  const relPath =
    cwd && !HOME_ANCHORED.test(fullPath)
      ? internalRelativePath(fullPath, cwd)
      : null;
  if (relPath !== null) {
    return {
      kind: "local-internal",
      fullPath,
      pathKind,
      relPath,
      ...(line !== undefined ? { line } : {}),
      ...(col !== undefined ? { col } : {}),
    };
  }

  return {
    kind: "local-external",
    fullPath,
    pathKind,
    ...(line !== undefined ? { line } : {}),
    ...(col !== undefined ? { col } : {}),
  };
}
