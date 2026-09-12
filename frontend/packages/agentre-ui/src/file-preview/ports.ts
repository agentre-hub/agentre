import type { ReadFileResult } from "../transcript/ports";

/**
 * 预览面板的**取数端口** —— 面板与宿主之间唯一的副作用边界。
 *
 * 与 `transcript/ports.ts` 同一套契约：形状对齐桌面端 Wails 绑定的应答，宿主零
 * 转换即可接上（agentre-server 那侧把它们映射成自己的 relay 请求）；失败一律
 * reject，面板只把它冒泡进自己的错误态，不吞异常、不做 toast。
 *
 * 端口只收 `path`（会话级 relPath）：**读哪个会话的哪个工作根**是宿主的身份问题
 * （桌面端是 sessionId + 已认领的工作根，控制台是它自己的那套坐标），由宿主在
 * 闭包里带着。面板需要知道的只是「这个身份变了没有」，那由 `sourceKey` 回答。
 */
export interface FilePreviewPorts {
  /** 读工作区文件。`binary` / `tooLarge` 是视图标志，不是错误。 */
  readFile(path: string): Promise<ReadFileResult>;
  /** 读同一文件在 git HEAD 的版本（对比档左列）。 */
  gitFileContent(path: string): Promise<GitFileContentResult>;
}

/** gitFileContent 的应答：非 git 仓库 → notARepo；未跟踪 / 不在 HEAD → 空基线。 */
export interface GitFileContentResult {
  content: string;
  notARepo?: boolean;
  hasHead?: boolean;
}

export type { ReadFileResult };

/**
 * 面板会区别对待的两类失败。
 *
 * - `offline`：对端离线 / 中继断开。**唯一**给重试按钮的失败态 —— 那台机器过会儿
 *   可能就回来了。
 * - `notFound`：文件已不存在。终态：转录里的路径来自当时那次工具调用，之后被删掉
 *   是正常情况，重试一万次也还是不存在。
 *
 * 归类留在宿主：错误码通道两端形状不同（桌面端的 Wails 边界只过一个已本地化的
 * Error() 字符串，控制台那侧有中继的失败分类），包内不去猜字符串。宿主 reject
 * 一个带 `kind` 的错误即可，例如
 * `Object.assign(new Error(msg), { kind: "offline" } satisfies FilePreviewFailure)`。
 * 不带 `kind` 的失败按「未归类」处理：如实显示它自己的文案 + 重试。
 */
export type FilePreviewFailureKind = "offline" | "notFound";

/** 宿主给失败归类时贴的那个标记。 */
export interface FilePreviewFailure {
  kind: FilePreviewFailureKind;
}

const FAILURE_KINDS: ReadonlySet<string> = new Set<FilePreviewFailureKind>([
  "offline",
  "notFound",
]);

/** 读 reject 值上的 `kind` 标记；没有 / 不认识的一律当未归类。 */
export function filePreviewFailureKind(
  err: unknown,
): FilePreviewFailureKind | null {
  const kind = (err as { kind?: unknown } | null | undefined)?.kind;
  return typeof kind === "string" && FAILURE_KINDS.has(kind)
    ? (kind as FilePreviewFailureKind)
    : null;
}

/**
 * 取失败自带的文案原样呈现 —— 两端的后端都已经把失败本地化好了（桌面端是
 * cago 的 i18n 错误码，控制台是中继的失败分类），前端不再二次归类。拿不到文案
 * 时返回空串，由调用方回落到自己那句通用提示。
 */
export function filePreviewFailureText(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === "string") return err;
  return "";
}
