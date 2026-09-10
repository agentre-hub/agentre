// 扩展名 / 文件名 → Monaco 内置语言 id。
// 映射只覆盖 monaco-editor 0.56 实际注册的语言（editor.main.js 随包注册的
// basic-languages + 核心 plaintext）；未知一律回退 plaintext（纯文本高亮）。
// 桌面离线 + //go:embed，语言集完全来自本地包，不引 CDN 语言资源。

const EXTENSION_LANGUAGE: Record<string, string> = {
  // web 前端 / 标记
  js: "javascript",
  jsx: "javascript",
  mjs: "javascript",
  cjs: "javascript",
  ts: "typescript",
  tsx: "typescript",
  mts: "typescript",
  cts: "typescript",
  json: "json",
  jsonc: "json",
  html: "html",
  htm: "html",
  xml: "xml",
  svg: "xml",
  css: "css",
  scss: "scss",
  sass: "scss",
  less: "less",
  // markdown / 文本格式
  md: "markdown",
  markdown: "markdown",
  mdx: "mdx",
  yaml: "yaml",
  yml: "yaml",
  toml: "ini",
  ini: "ini",
  cfg: "ini",
  conf: "ini",
  // shell / 脚本
  sh: "shell",
  bash: "shell",
  zsh: "shell",
  py: "python",
  pyw: "python",
  rb: "ruby",
  php: "php",
  lua: "lua",
  r: "r",
  pl: "perl",
  ps1: "powershell",
  psd1: "powershell",
  psm1: "powershell",
  bat: "bat",
  cmd: "bat",
  // 系统 / 编译型
  c: "c",
  h: "cpp",
  cpp: "cpp",
  cc: "cpp",
  cxx: "cpp",
  hpp: "cpp",
  hh: "cpp",
  cs: "csharp",
  java: "java",
  go: "go",
  rs: "rust",
  swift: "swift",
  kt: "kotlin",
  kts: "kotlin",
  dart: "dart",
  scala: "scala",
  sc: "scala",
  clj: "clojure",
  cljs: "clojure",
  cljc: "clojure",
  groovy: "groovy",
  gradle: "groovy",
  sql: "sql",
};

// 按完整文件名（basename 小写）识别，覆盖 Dockerfile 这类无扩展名入口。
const FILENAME_LANGUAGE: Record<string, string> = {
  dockerfile: "dockerfile",
};

// 由文件路径推断 Monaco 语言 id；未知扩展名 → "plaintext"。
export function monacoLanguageForPath(path: string): string {
  const basename = (path.split("/").pop() ?? "").toLowerCase();
  const byName = FILENAME_LANGUAGE[basename];
  if (byName) return byName;
  const dot = basename.lastIndexOf(".");
  if (dot > 0) {
    const language = EXTENSION_LANGUAGE[basename.slice(dot + 1)];
    if (language) return language;
  }
  return "plaintext";
}
