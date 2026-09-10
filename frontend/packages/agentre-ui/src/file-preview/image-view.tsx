import { cn } from "../lib/utils";

export type ImagePreviewProps = {
  /** base64 编码的图片正文（readFile 应答里的 content）。 */
  content: string;
  /**
   * MIME 类型（readFile 应答里的 contentType）。那侧按扩展名给，字段可缺省；
   * 缺省时退到 application/octet-stream —— 拼不出类型的 data URL 本来也渲染不出
   * 图，但至少不会把字面量 "undefined" 写进 src。
   */
  contentType?: string;
  /** 读屏替代文本：通常是文件名。 */
  alt: string;
  className?: string;
};

/**
 * 图片档：readFile 的应答自带 base64 正文与 MIME，直接拼成 data URL 渲染，不经
 * 网络、不落盘（桌面离线 + 控制台经中继都只有这一条正文路径）。
 *
 * 底衬棋盘格：透明 png 落在纯色背景上分不清「透明」与「白」。
 */
export function ImagePreview({
  content,
  contentType,
  alt,
  className,
}: ImagePreviewProps) {
  return (
    <div
      className={cn(
        "flex min-h-0 flex-1 items-center justify-center overflow-auto p-4",
        className,
      )}
      style={{
        backgroundImage:
          "repeating-conic-gradient(var(--muted) 0% 25%, transparent 0% 50%)",
        backgroundSize: "16px 16px",
      }}
    >
      <img
        src={`data:${contentType || "application/octet-stream"};base64,${content}`}
        alt={alt}
        className="max-h-full max-w-full rounded-md object-contain shadow-sm"
      />
    </div>
  );
}
