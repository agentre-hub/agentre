import * as React from "react";
import { useTranslation } from "react-i18next";

import { Badge, Button } from "@agentre-hub/agentre-ui";

import { relativeTime } from "../remote-devices/format";
import type { SyncStatusView } from "./use-sync-status";

// server_svc.ErrServerUnreachable —— 网络层失败统一归一到这个哨兵串
// (internal/service/server_svc/httpclient.go),值得给一句更友好的文案；
// 别的错误原样显示 LastError,不吞掉细节。
const UNREACHABLE_ERROR = "server: unreachable";

type BadgeKind = "up-to-date" | "waiting" | "unreachable";

function badgeKindOf(status: SyncStatusView): BadgeKind {
  if (status.lastError) return "unreachable";
  if (status.pendingCount > 0) return "waiting";
  return "up-to-date";
}

type SyncStatusCardProps = {
  status: SyncStatusView;
  onRetry: () => Promise<void>;
  /** 调用方(SyncPanel)持有的当前时间,每分钟刷新一次;渲染期间不直接调用
   * `Date.now()`(react-hooks/purity)。 */
  now: number;
};

/**
 * 设置 → 同步的第一张卡:上次成功同步时间 / 待同步条目数 / 不可达原因。
 * 不新增「正在同步」这类解释性横幅——卡内容随状态切换,没有独立的进度提示。
 */
export function SyncStatusCard({ status, onRetry, now }: SyncStatusCardProps) {
  const { t } = useTranslation();
  const [retrying, setRetrying] = React.useState(false);
  const kind = badgeKindOf(status);

  const handleRetry = async () => {
    setRetrying(true);
    try {
      await onRetry();
    } finally {
      setRetrying(false);
    }
  };

  return (
    <section
      data-slot="sync-status-card"
      className="overflow-hidden rounded-lg border border-border bg-card"
    >
      <div className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3">
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <h2 className="text-sm font-semibold">{t("sync.status.title")}</h2>
          {kind === "up-to-date" ? (
            <p className="text-xs leading-relaxed text-muted-foreground">
              {t("sync.status.description")}
            </p>
          ) : null}
        </div>
        {kind === "up-to-date" ? (
          <Badge
            variant="secondary"
            className="gap-1 rounded-sm bg-status-running-bg px-1.5 py-0.5 text-2xs font-medium text-status-running"
          >
            <span
              className="size-1.5 rounded-full bg-status-running"
              aria-hidden="true"
            />
            {t("sync.status.badge.upToDate")}
          </Badge>
        ) : kind === "waiting" ? (
          <Badge
            variant="secondary"
            className="rounded-sm bg-status-waiting-bg px-1.5 py-0.5 text-2xs font-medium text-status-waiting"
          >
            {t("sync.status.badge.pending", { count: status.pendingCount })}
          </Badge>
        ) : (
          <Badge
            variant="secondary"
            className="rounded-sm bg-destructive-soft px-1.5 py-0.5 text-2xs font-medium text-destructive"
          >
            {t("sync.status.badge.unreachable")}
          </Badge>
        )}
      </div>

      {kind === "unreachable" ? (
        <div className="flex flex-col gap-2 p-4">
          <div className="flex flex-col gap-0.5">
            <span className="text-sm font-medium">
              {status.lastError === UNREACHABLE_ERROR
                ? t("sync.status.unreachableTitle")
                : t("sync.status.errorTitle")}
            </span>
            <p className="text-xs leading-relaxed text-muted-foreground">
              {status.lastError === UNREACHABLE_ERROR
                ? t("sync.status.unreachableDetail")
                : status.lastError}
            </p>
          </div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="mt-1 h-7 w-fit text-2xs"
            onClick={() => void handleRetry()}
            disabled={retrying}
          >
            {retrying ? t("sync.status.retrying") : t("sync.status.retry")}
          </Button>
        </div>
      ) : (
        <div className="flex flex-col gap-2 p-4">
          <div className="flex min-w-0 flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <span className="text-sm font-medium">
              {t("sync.status.lastSuccess")}
            </span>
            <span className="text-sm tabular-nums text-muted-foreground">
              {relativeTime(status.lastSuccessAt, now, t)}
            </span>
          </div>
          {kind === "up-to-date" ? (
            <div className="flex min-w-0 flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
              <span className="text-sm font-medium">
                {t("sync.status.pending")}
              </span>
              <span className="text-sm tabular-nums text-muted-foreground">
                {t("sync.status.pendingCount", {
                  count: status.pendingCount,
                })}
              </span>
            </div>
          ) : null}
        </div>
      )}
    </section>
  );
}
