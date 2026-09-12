/**
 * 会话文件预览的可预览性判定与「路径 → 会话级 relPath」换算（spec「预览面板与入口」）。
 *
 * 「变动」模式的行路径可能来自工具调用的绝对路径：只有解析后落在会话 cwd 内的
 * 文件才出预览按钮（越出 cwd 的一律不出）；相对路径原样当 relPath。「目录 / Git」
 * 模式的路径本就相对 cwd，直接可用。任何不在扩展名 allowlist 内的文件不出预览按钮。
 */ export type PreviewKind = "markdown" | "code" | "image";

/** markdown 档只收 .md / .markdown；.mdx 含 JSX、GFM 渲染会碎，明确不入档。 */
const MARKDOWN_EXTENSIONS = new Set([".md", ".markdown"]);

const IMAGE_EXTENSIONS = new Set([
  ".png",
  ".jpg",
  ".jpeg",
  ".gif",
  ".webp",
  ".avif",
  ".bmp",
  ".ico",
  ".svg",
]);

// 代码 / 文本 allowlist —— 与 lib/file-preview/monaco-language 的语言表对齐
// （能高亮就给预览按钮），另加常见纯文本格式；markdown / 图片 / .mdx 不在这里。
const CODE_TEXT_EXTENSIONS = new Set([
  // web 前端 / 标记
  ".js",
  ".jsx",
  ".mjs",
  ".cjs",
  ".ts",
  ".tsx",
  ".mts",
  ".cts",
  ".json",
  ".jsonc",
  ".html",
  ".htm",
  ".xml",
  ".css",
  ".scss",
  ".sass",
  ".less",
  // 文本格式 / 配置
  ".yaml",
  ".yml",
  ".toml",
  ".ini",
  ".cfg",
  ".conf",
  ".txt",
  ".log",
  ".csv",
  // shell / 脚本
  ".sh",
  ".bash",
  ".zsh",
  ".py",
  ".pyw",
  ".rb",
  ".php",
  ".lua",
  ".r",
  ".pl",
  ".ps1",
  ".psd1",
  ".psm1",
  ".bat",
  ".cmd",
  // 系统 / 编译型
  ".c",
  ".h",
  ".cpp",
  ".cc",
  ".cxx",
  ".hpp",
  ".hh",
  ".cs",
  ".java",
  ".go",
  ".rs",
  ".swift",
  ".kt",
  ".kts",
  ".dart",
  ".scala",
  ".sc",
  ".clj",
  ".cljs",
  ".cljc",
  ".groovy",
  ".gradle",
  ".sql",
]);

function extensionOf(path: string): string {
  const base = path.split("/").pop() ?? "";
  const slash = base.lastIndexOf("\\");
  const name = slash >= 0 ? base.slice(slash + 1) : base;
  const dot = name.lastIndexOf(".");
  if (dot <= 0) return "";
  return name.slice(dot).toLowerCase();
}

/** 按扩展名判定文件的可预览类型；不在 allowlist 内返回 null。 */
export function previewKind(path: string): PreviewKind | null {
  const ext = extensionOf(path);
  if (MARKDOWN_EXTENSIONS.has(ext)) return "markdown";
  if (IMAGE_EXTENSIONS.has(ext)) return "image";
  if (CODE_TEXT_EXTENSIONS.has(ext)) return "code";
  return null;
}

const ABS_POSIX = /^\//;
const ABS_WINDOWS = /^[A-Za-z]:[\\/]/;

/**
 * 把文件面板一条路径换算成相对会话 cwd 的路径。返回 null 表示换算不出来（会话
 * 没有 cwd，或绝对路径落在 cwd 之外）。相对路径（目录 / Git 模式）原样通过。
 *
 * 结果恒用 "/" 分隔：这个字符串就是预览标签的身份（chat-sidebar-store 的
 * `FilePreviewTab.path`），而三个模式给进来的形状并不一致——「目录」模式自己按
 * "/" 拼、「Git」模式的路径直接来自后端（恒 "/"），只有「变动」模式的行路径来自
 * 工具调用，Windows 会话上是 "\" 分隔。不在这一处归一，同一个文件从两个模式点开
 * 会开出两个标签。后端两端都用 `filepath.FromSlash` 还原本地分隔符
 * （internal/pkg/workspacefs/listdir.go 的 resolveRelPath），"/" 是安全的。
 */
export function toRelPath(path: string, cwd: string): string | null {
  if (cwd === "") return null;
  if (!ABS_POSIX.test(path) && !ABS_WINDOWS.test(path)) {
    return toSlash(path);
  }
  const sep = cwd.includes("\\") ? "\\" : "/";
  const prefix = cwd.endsWith("/") || cwd.endsWith("\\") ? cwd : `${cwd}${sep}`;
  if (path === cwd || !path.startsWith(prefix)) return null;
  return toSlash(path.slice(prefix.length));
}

/** 会话级 relPath 的规范分隔符是 "/"（Windows 会话的 "\" 在这里归一）。 */
function toSlash(relPath: string): string {
  return relPath.replace(/\\/g, "/");
}

/**
 * 把文件面板一条路径解析成可交给后端 ReadFile/GitFileContent 的会话级 relPath。
 * 返回 null 表示该文件不可预览（扩展名不在 allowlist，绝对路径越出 cwd，或会话
 * 根本没有 cwd）——这样的行不响应单击，菜单里也不出预览两项。
 *
 * 无 cwd(自由会话 / 远端未配路径)时后端必然回 WorkspaceFsNoCwd，预览随行消失
 * （spec「预览面板与入口」）——目录 / Git 模式本就不渲染行，这里覆盖「变动」
 * 模式里消息派生出的相对路径行。
 */
export function resolvePreviewRelPath(
  path: string,
  cwd: string,
): string | null {
  if (previewKind(path) === null) return null;
  return toRelPath(path, cwd);
}
