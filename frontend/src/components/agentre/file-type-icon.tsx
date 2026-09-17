import * as React from "react";
import { Icon as IconifyIconCmp } from "@iconify/react";
import type { IconifyIcon } from "@iconify/types";
import iconBinary from "@iconify-icons/tabler/binary";
import iconCsharp from "@iconify-icons/tabler/brand-c-sharp";
import iconCpp from "@iconify-icons/tabler/brand-cpp";
import iconCss from "@iconify-icons/tabler/brand-css3";
import iconDocker from "@iconify-icons/tabler/brand-docker";
import iconGit from "@iconify-icons/tabler/brand-git";
import iconGo from "@iconify-icons/tabler/brand-golang";
import iconHtml from "@iconify-icons/tabler/brand-html5";
import iconJavaScript from "@iconify-icons/tabler/brand-javascript";
import iconKotlin from "@iconify-icons/tabler/brand-kotlin";
import iconNpm from "@iconify-icons/tabler/brand-npm";
import iconPhp from "@iconify-icons/tabler/brand-php";
import iconPython from "@iconify-icons/tabler/brand-python";
import iconReact from "@iconify-icons/tabler/brand-react";
import iconRust from "@iconify-icons/tabler/brand-rust";
import iconSass from "@iconify-icons/tabler/brand-sass";
import iconSwift from "@iconify-icons/tabler/brand-swift";
import iconTypeScript from "@iconify-icons/tabler/brand-typescript";
import iconJava from "@iconify-icons/tabler/coffee";
import iconCert from "@iconify-icons/tabler/file-certificate";
import iconCode from "@iconify-icons/tabler/file-code";
import iconDatabase from "@iconify-icons/tabler/file-database";
import iconText from "@iconify-icons/tabler/file-text";
import iconCsv from "@iconify-icons/tabler/file-type-csv";
import iconWord from "@iconify-icons/tabler/file-type-docx";
import iconPdf from "@iconify-icons/tabler/file-type-pdf";
import iconPowerPoint from "@iconify-icons/tabler/file-type-ppt";
import iconSql from "@iconify-icons/tabler/file-type-sql";
import iconSvg from "@iconify-icons/tabler/file-type-svg";
import iconExcel from "@iconify-icons/tabler/file-type-xls";
import iconXml from "@iconify-icons/tabler/file-type-xml";
import iconFont from "@iconify-icons/tabler/file-typography";
import iconUnknown from "@iconify-icons/tabler/file-unknown";
import iconArchive from "@iconify-icons/tabler/file-zip";
import iconJson from "@iconify-icons/tabler/json";
import iconKey from "@iconify-icons/tabler/key";
import iconLock from "@iconify-icons/tabler/lock";
import iconMarkdown from "@iconify-icons/tabler/markdown";
import iconAudio from "@iconify-icons/tabler/music";
import iconImage from "@iconify-icons/tabler/photo";
import iconConfig from "@iconify-icons/tabler/settings";
import iconTerminal from "@iconify-icons/tabler/terminal";
import iconVideo from "@iconify-icons/tabler/video";

import { monacoLanguageForPath } from "@/lib/file-preview/monaco-language";
import { cn } from "@agentre-hub/agentre-ui";

/** 文件身份色调——语义色 token（`file-<tone>`），与代理调色板/状态色无关。 */
export type FileTypeTone =
  | "blue"
  | "yellow"
  | "cyan"
  | "purple"
  | "orange"
  | "green"
  | "red"
  | "neutral";

/**
 * 一条路径的分类结果：`id` 是稳定身份（语言 / 生态 / 格式 / 未知），`tone` 是
 * 该身份映射到的语义色调。字形在组件内部按 `id` 查找，颜色与字形双重编码身份。
 */
export interface FileTypeMeta {
  id: string;
  tone: FileTypeTone;
}

interface FileTypeEntry {
  tone: FileTypeTone;
  icon: IconifyIcon;
}

/** 身份目录：`id` → 色调 + 静态导入的 Tabler 图标数据。 */
const CATALOG_ENTRIES = {
  // 编程语言 / Web
  go: { tone: "cyan", icon: iconGo },
  python: { tone: "yellow", icon: iconPython },
  typescript: { tone: "blue", icon: iconTypeScript },
  javascript: { tone: "yellow", icon: iconJavaScript },
  "react-ts": { tone: "blue", icon: iconReact },
  "react-js": { tone: "yellow", icon: iconReact },
  rust: { tone: "orange", icon: iconRust },
  java: { tone: "orange", icon: iconJava },
  kotlin: { tone: "purple", icon: iconKotlin },
  cpp: { tone: "blue", icon: iconCpp },
  csharp: { tone: "purple", icon: iconCsharp },
  shell: { tone: "green", icon: iconTerminal },
  html: { tone: "orange", icon: iconHtml },
  css: { tone: "blue", icon: iconCss },
  sass: { tone: "purple", icon: iconSass },
  php: { tone: "purple", icon: iconPhp },
  swift: { tone: "orange", icon: iconSwift },
  sql: { tone: "blue", icon: iconSql },
  markdown: { tone: "blue", icon: iconMarkdown },
  json: { tone: "yellow", icon: iconJson },
  yaml: { tone: "red", icon: iconCode },
  toml: { tone: "neutral", icon: iconCode },
  xml: { tone: "orange", icon: iconXml },
  text: { tone: "neutral", icon: iconText },
  config: { tone: "blue", icon: iconConfig },

  // 项目入口 / 生态
  dockerfile: { tone: "blue", icon: iconDocker },
  makefile: { tone: "green", icon: iconTerminal },
  cmake: { tone: "green", icon: iconTerminal },
  npm: { tone: "red", icon: iconNpm },
  git: { tone: "orange", icon: iconGit },
  ruby: { tone: "red", icon: iconCode },

  // 文档 / 表格
  pdf: { tone: "red", icon: iconPdf },
  word: { tone: "blue", icon: iconWord },
  excel: { tone: "green", icon: iconExcel },
  powerpoint: { tone: "orange", icon: iconPowerPoint },
  csv: { tone: "green", icon: iconCsv },

  // 媒体 / 字体
  image: { tone: "purple", icon: iconImage },
  svg: { tone: "orange", icon: iconSvg },
  audio: { tone: "purple", icon: iconAudio },
  video: { tone: "red", icon: iconVideo },
  font: { tone: "green", icon: iconFont },

  // 归档 / 二进制
  archive: { tone: "orange", icon: iconArchive },
  binary: { tone: "red", icon: iconBinary },

  // 数据库 / 密钥 / 锁文件
  database: { tone: "green", icon: iconDatabase },
  key: { tone: "yellow", icon: iconKey },
  cert: { tone: "yellow", icon: iconCert },
  lock: { tone: "neutral", icon: iconLock },

  // 语言识别回退（Monaco 语言表高频语言，无专用 Tabler 品牌字形）
  lua: { tone: "blue", icon: iconCode },
  r: { tone: "blue", icon: iconCode },
  perl: { tone: "blue", icon: iconCode },
  powershell: { tone: "blue", icon: iconCode },
  dart: { tone: "cyan", icon: iconCode },
  scala: { tone: "red", icon: iconCode },
  clojure: { tone: "green", icon: iconCode },
  groovy: { tone: "yellow", icon: iconCode },

  // 回退
  unknown: { tone: "neutral", icon: iconUnknown },
} as const satisfies Record<string, FileTypeEntry>;

/** 身份 id 只能是目录中已注册的键，防止 `CATALOG[id]` 指向未定义条目。 */
type FileTypeId = keyof typeof CATALOG_ENTRIES;

/** 运行时按 `id` 查找的目录（`string` 索引便于 `FileTypeMeta.id` 直接取值）。 */
const CATALOG: Record<string, FileTypeEntry> = CATALOG_ENTRIES;

/** 完整或特定文件名（basename 小写），优先于后缀匹配。 */
const EXACT_FILENAME: Record<string, FileTypeId> = {
  dockerfile: "dockerfile",
  makefile: "makefile",
  "cmakelists.txt": "cmake",
  "package.json": "npm",
  "package-lock.json": "npm",
  "pnpm-lock.yaml": "npm",
  "yarn.lock": "npm",
  "go.mod": "go",
  "go.sum": "go",
  "cargo.toml": "rust",
  "cargo.lock": "rust",
  "requirements.txt": "python",
  "pyproject.toml": "python",
  gemfile: "ruby",
  ".gitignore": "git",
  ".gitattributes": "git",
  ".editorconfig": "config",
  ".npmrc": "npm",
  ".prettierrc": "config",
  ".eslintrc": "config",
};

/** 普通扩展名（最后一个点之后，小写）→ 身份。 */
const EXTENSION: Record<string, FileTypeId> = {
  go: "go",
  py: "python",
  pyw: "python",
  ts: "typescript",
  mts: "typescript",
  cts: "typescript",
  js: "javascript",
  mjs: "javascript",
  cjs: "javascript",
  tsx: "react-ts",
  jsx: "react-js",
  rs: "rust",
  java: "java",
  kt: "kotlin",
  kts: "kotlin",
  c: "cpp",
  h: "cpp",
  cpp: "cpp",
  cc: "cpp",
  cxx: "cpp",
  hpp: "cpp",
  hh: "cpp",
  cs: "csharp",
  sh: "shell",
  bash: "shell",
  zsh: "shell",
  bat: "shell",
  cmd: "shell",
  ps1: "shell",
  html: "html",
  htm: "html",
  css: "css",
  less: "css",
  scss: "sass",
  sass: "sass",
  php: "php",
  swift: "swift",
  sql: "sql",
  md: "markdown",
  mdx: "markdown",
  markdown: "markdown",
  json: "json",
  jsonc: "json",
  yaml: "yaml",
  yml: "yaml",
  toml: "toml",
  xml: "xml",
  svg: "svg",
  ini: "config",
  cfg: "config",
  conf: "config",
  txt: "text",
  log: "text",
  text: "text",
  pdf: "pdf",
  doc: "word",
  docx: "word",
  xls: "excel",
  xlsx: "excel",
  ppt: "powerpoint",
  pptx: "powerpoint",
  csv: "csv",
  png: "image",
  jpg: "image",
  jpeg: "image",
  gif: "image",
  bmp: "image",
  webp: "image",
  ico: "image",
  tiff: "image",
  avif: "image",
  mp3: "audio",
  wav: "audio",
  ogg: "audio",
  flac: "audio",
  aac: "audio",
  m4a: "audio",
  mp4: "video",
  mov: "video",
  avi: "video",
  mkv: "video",
  webm: "video",
  ttf: "font",
  otf: "font",
  woff: "font",
  woff2: "font",
  eot: "font",
  zip: "archive",
  "7z": "archive",
  rar: "archive",
  tar: "archive",
  gz: "archive",
  tgz: "archive",
  bz2: "archive",
  xz: "archive",
  exe: "binary",
  bin: "binary",
  dll: "binary",
  so: "binary",
  dylib: "binary",
  wasm: "binary",
  o: "binary",
  obj: "binary",
  a: "binary",
  jar: "binary",
  sqlite: "database",
  sqlite3: "database",
  db: "database",
  key: "key",
  pem: "key",
  p12: "key",
  pfx: "key",
  pub: "key",
  priv: "key",
  crt: "cert",
  cer: "cert",
  der: "cert",
  lock: "lock",
};

/** Monaco 语言 id（语言识别回退层）→ 身份；只覆盖 EXTENSION 未登记的扩展名。 */
const LANGUAGE_ID: Record<string, FileTypeId> = {
  ruby: "ruby",
  lua: "lua",
  r: "r",
  perl: "perl",
  powershell: "powershell",
  dart: "dart",
  scala: "scala",
  clojure: "clojure",
  groovy: "groovy",
};

/** basename 同时认 POSIX `/` 与 Windows `\` 分隔符。 */
function basenameOf(path: string): string {
  const parts = path.split(/[\\/]/);
  return parts[parts.length - 1] ?? path;
}

/**
 * 匹配优先级：完整文件名 → 复合文件名/后缀 → 普通扩展名 → 语言识别回退 → 未知回退。
 * 全程不区分大小写；不读取文件内容、不访问文件系统。
 */
function identify(path: string): FileTypeId {
  const basename = basenameOf(path).toLowerCase();
  if (basename === "") return "unknown";

  const exact = EXACT_FILENAME[basename];
  if (exact) return exact;

  // 复合形式：`.env` / `.env.*`（环境配置）、`*.dockerfile` / `Dockerfile.*`
  // （Docker 身份）。`.tar.gz` / `.d.ts` 的最后一个后缀已落在 `gz` / `ts` 上，
  // 无需单独分派。
  if (basename === ".env" || basename.startsWith(".env.")) return "config";
  if (basename.endsWith(".dockerfile") || basename.startsWith("dockerfile.")) {
    return "dockerfile";
  }

  const dot = basename.lastIndexOf(".");
  if (dot > 0) {
    const id = EXTENSION[basename.slice(dot + 1)];
    if (id) return id;
  }
  // 语言识别回退：扩展名不在本目录时，复用 Monaco 的语言映射兜底（如 rb/lua/r/
  // pl/dart/scala/clojure/groovy 等），再经语言 id → 身份的小映射归一。
  const language = monacoLanguageForPath(basename);
  const fallback = LANGUAGE_ID[language];
  if (fallback) return fallback;
  return "unknown";
}

/** 把一条路径稳定分类为文件身份；未知/空路径/无法识别 dotfile 回退到中性 `unknown`。 */
export function classifyFileType(path: string): FileTypeMeta {
  const id = identify(path);
  return { id, tone: CATALOG[id].tone };
}

const fileToneClassNames: Record<FileTypeTone, string> = {
  blue: "text-file-blue",
  yellow: "text-file-yellow",
  cyan: "text-file-cyan",
  purple: "text-file-purple",
  orange: "text-file-orange",
  green: "text-file-green",
  red: "text-file-red",
  neutral: "text-file-neutral",
};

type FileTypeIconProps = {
  path: string;
  className?: string;
  testId?: string;
};

/**
 * 统一的文件身份图标：17px 透明对齐槽位 + 身份色调 token 着色的 Tabler Logo/glyph。
 * 纯装饰（`aria-hidden`），文件名 / Git 状态 / 可操作性由既有语义继续表达。
 */
export function FileTypeIcon({
  path,
  className,
  testId,
}: FileTypeIconProps): React.ReactElement {
  const meta = classifyFileType(path);

  return (
    <span
      data-testid={testId}
      data-file-type={meta.id}
      aria-hidden="true"
      className={cn(
        "inline-flex size-[17px] shrink-0 items-center justify-center",
        fileToneClassNames[meta.tone],
        className,
      )}
    >
      <IconifyIconCmp
        icon={CATALOG[meta.id].icon}
        className="size-[17px]"
        aria-hidden="true"
      />
    </span>
  );
}
