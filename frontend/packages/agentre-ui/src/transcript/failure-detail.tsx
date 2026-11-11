import { useUiTranslation } from "../i18n";

/**
 * 从一次失败里取机器原话：`Error.message`、裸字符串（Wails 的 reject 形状）、或带
 * `message` / `error` 等字段的对象。取不到返回空串——调用方据此决定要不要渲染详情
 * 或 `title`，这里不编造兜底文案。
 */
export function failureDetailText(err: unknown): string {
  if (err instanceof Error) return err.message.trim();
  if (typeof err === "string") return err.trim();
  if (err && typeof err === "object") {
    for (const key of ["message", "error", "detail", "details", "reason"]) {
      const value = (err as Record<string, unknown>)[key];
      if (typeof value === "string" && value.trim()) return value.trim();
      if (value instanceof Error && value.message.trim()) {
        return value.message.trim();
      }
    }
  }
  return "";
}

/**
 * 失败的机器原话：默认折叠的「详情」，展开后等宽、可选中复制。正文那句本地化事实
 * 由调用方自己写在它上面；没有原文就不渲染。
 */
export function FailureDetail({
  detail,
  className,
}: {
  detail?: string;
  className?: string;
}) {
  const { t } = useUiTranslation();
  if (!detail?.trim()) return null;
  return (
    <details className={className}>
      <summary className="cursor-pointer">{t("common.details")}</summary>
      <pre
        data-selectable-text="true"
        className="mt-1 font-mono text-2xs break-all whitespace-pre-wrap select-text"
      >
        {detail}
      </pre>
    </details>
  );
}
