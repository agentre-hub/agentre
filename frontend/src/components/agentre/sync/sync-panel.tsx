import * as React from "react";
import { useTranslation } from "react-i18next";

import { LostChangesCard } from "./lost-changes-card";
import { SyncStatusCard } from "./sync-status-card";
import { useSyncStatus } from "./use-sync-status";

// 相对时间(「2 分钟前」)每分钟刷新一次;渲染期间不直接调用 `Date.now()`
// (react-hooks/purity),状态卡与列表行共享同一份 `now`。
const NOW_REFRESH_INTERVAL_MS = 60_000;

function useNow(intervalMs: number): number {
  const [now, setNow] = React.useState(() => Date.now());
  React.useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), intervalMs);
    return () => window.clearInterval(id);
  }, [intervalMs]);
  return now;
}

/**
 * 设置 → 同步(整页新增):同步状态卡 + 「没能同步的改动」卡。未登录时
 * `status` 为 `null`,这里直接不渲染任何内容——调用方(SettingsPage)据同一份
 * `status` 已经让「同步」这个导航项整个不出现(R12),这里只是同一判断的
 * 兜底,不重复展示「请先登录」之类的说明。
 */
export function SyncPanel() {
  const { t } = useTranslation();
  const { status, lostChanges, loading, retryNow, restore, recreate, discard } =
    useSyncStatus();
  const now = useNow(NOW_REFRESH_INTERVAL_MS);

  if (loading || !status || !status.enabled) return null;

  return (
    <div data-slot="sync-panel" className="flex flex-col gap-6">
      <div className="flex max-w-3xl flex-col gap-1.5">
        <h1 className="text-2xl font-semibold tracking-normal">
          {t("sync.title")}
        </h1>
        <p className="text-sm leading-relaxed text-muted-foreground">
          {t("sync.description")}
        </p>
      </div>
      <SyncStatusCard status={status} onRetry={retryNow} now={now} />
      <LostChangesCard
        lostChanges={lostChanges}
        onRestore={restore}
        onRecreate={recreate}
        onDiscard={discard}
        now={now}
      />
    </div>
  );
}
