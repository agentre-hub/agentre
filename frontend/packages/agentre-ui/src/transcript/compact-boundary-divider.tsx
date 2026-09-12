import * as React from "react";
import { useUiTranslation } from "../i18n";

// CompactBoundaryDivider 渲染 transcript 内嵌的"上下文已压缩"分隔卡片。
// 文案:左右细线 + 中间 chip,chip 内含 trigger 标签、压缩前 token 数 (有则显示)、
// 触发时间 HH:mm。trigger 缺省时退化为"已压缩",preTokens=0 时不显示 token 数。
//
// 视觉风格对齐 shadcn token (border-border / text-muted-foreground / bg-muted),
// 与 queued-messages-bar.tsx 保持一致。
export function CompactBoundaryDivider({
  preTokens,
  trigger,
  at,
}: {
  preTokens?: number;
  trigger?: "auto" | "manual";
  at: number;
}): React.ReactElement {
  const { t } = useUiTranslation();
  const triggerLabel =
    trigger === "manual"
      ? t("compactBoundary.trigger.manual")
      : trigger === "auto"
        ? t("compactBoundary.trigger.auto")
        : t("compactBoundary.trigger.compressed");

  const tokenLabel =
    typeof preTokens === "number" && preTokens > 0
      ? t("compactBoundary.tokensBefore", {
          tokens: preTokens.toLocaleString(),
        })
      : null;

  const timeLabel = at > 0 ? formatHHMM(at) : null;

  return (
    <div
      className="flex w-full max-w-measure items-center gap-3 py-2"
      role="separator"
      aria-label={t("compactBoundary.aria")}
    >
      <div className="h-px flex-1 bg-border" />
      <div className="flex items-center gap-2 rounded-full border border-border bg-muted px-3 py-1 text-meta text-muted-foreground">
        <span className="font-medium">{t("compactBoundary.label")}</span>
        <span aria-hidden="true">·</span>
        <span>{triggerLabel}</span>
        {tokenLabel ? (
          <>
            <span aria-hidden="true">·</span>
            <span>{tokenLabel}</span>
          </>
        ) : null}
        {timeLabel ? (
          <>
            <span aria-hidden="true">·</span>
            <span>{timeLabel}</span>
          </>
        ) : null}
      </div>
      <div className="h-px flex-1 bg-border" />
    </div>
  );
}

function formatHHMM(ms: number): string {
  const d = new Date(ms);
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  return `${hh}:${mm}`;
}
