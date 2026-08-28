/**
 * 「发现模型」读不到清单时那一块。
 *
 * 三件东西同屏：**说清是什么失败**（归类后的标题 + 一段解释）、**两条出路**
 * （去改连接 / 重试）、**原始响应**（折叠，默认收起）。原始串留着但不占主位——
 * 它是给排查用的证据，不是给用户读的说明。
 */
import {
  ChevronDown,
  ChevronUp,
  Loader2,
  Pencil,
  RefreshCw,
  TriangleAlert,
} from "lucide-react";

import { useUiTranslation as useTranslation } from "../../i18n";
import { Button } from "../../ui/button";

import type { FailureKind } from "./discover-failure";

export interface DiscoverErrorPanelProps {
  /** 上游原样返回的错误文本，只进折叠区。 */
  rawError: string;
  failure: { kind: FailureKind; code?: string };
  loading: boolean;
  showRaw: boolean;
  onToggleRaw(): void;
  onEditConnection(): void;
  onRetry(): void;
}

export function DiscoverErrorPanel({
  rawError,
  failure,
  loading,
  showRaw,
  onToggleRaw,
  onEditConnection,
  onRetry,
}: DiscoverErrorPanelProps) {
  const { t } = useTranslation();

  return (
    <div
      role="alert"
      className="flex flex-col items-start gap-2 rounded-md border border-status-error/40 bg-destructive-soft px-3 py-2.5 text-status-error"
    >
      <div className="flex items-start gap-2 text-xs">
        <TriangleAlert className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
        {/* 标题一句话说清是什么失败，解释单独一段 */}
        <div className="flex min-w-0 flex-col gap-0.5">
          <span className="font-semibold">
            {t(`llmProviders.discover.errorTitle.${failure.kind}`, {
              code: failure.code,
            })}
          </span>
          <span className="text-2xs leading-relaxed">
            {t(`llmProviders.discover.error.${failure.kind}`, {
              code: failure.code,
            })}
          </span>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-7 gap-1.5 text-2xs"
          onClick={onEditConnection}
        >
          <Pencil className="size-3" aria-hidden="true" />
          {t("llmProviders.discover.goEditConnection")}
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-7 gap-1.5 text-2xs"
          onClick={onRetry}
          disabled={loading}
        >
          {loading ? (
            <Loader2 className="size-3 animate-spin" aria-hidden="true" />
          ) : (
            <RefreshCw className="size-3" aria-hidden="true" />
          )}
          {t("llmProviders.discover.retry")}
        </Button>
      </div>
      <button
        type="button"
        className="flex items-center gap-1 text-2xs text-muted-foreground underline-offset-2 hover:underline"
        onClick={onToggleRaw}
        aria-expanded={showRaw}
      >
        {showRaw ? (
          <ChevronUp className="size-3" aria-hidden="true" />
        ) : (
          <ChevronDown className="size-3" aria-hidden="true" />
        )}
        {t("llmProviders.discover.viewRawResponse")}
      </button>
      {showRaw ? (
        <pre className="max-h-40 w-full overflow-auto whitespace-pre-wrap break-words rounded border border-border bg-secondary px-2.5 py-2 font-mono text-2xs text-foreground">
          {rawError}
        </pre>
      ) : null}
    </div>
  );
}
