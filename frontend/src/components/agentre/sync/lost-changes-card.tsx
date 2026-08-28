import * as React from "react";
import { useTranslation } from "react-i18next";

import { Badge } from "@agentre-hub/agentre-ui";

import { LostChangeRow } from "./lost-change-row";
import type { LostChangeView, RestoreOutcome } from "./use-sync-status";

type LostChangesCardProps = {
  lostChanges: LostChangeView[];
  onRestore: (id: number) => Promise<RestoreOutcome>;
  onRecreate: (id: number) => Promise<void>;
  onDiscard: (id: number) => Promise<void>;
  now: number;
};

/**
 * 设置 → 同步的第二张卡:「没能同步的改动」(R5)。三种失效原因(被覆盖 /
 * 引用丢失 / 超时未上传)共用这一份列表;空态是常态,如实说「所有改动都
 * 同步成功了」而不是隐藏这张卡——用户需要能确认「现在没有需要我处理的」。
 */
export function LostChangesCard({
  lostChanges,
  onRestore,
  onRecreate,
  onDiscard,
  now,
}: LostChangesCardProps) {
  const { t } = useTranslation();
  const [expandedIds, setExpandedIds] = React.useState<Set<number>>(
    () => new Set(),
  );

  const toggle = (id: number) => {
    setExpandedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  };

  return (
    <section
      data-slot="lost-changes-card"
      className="overflow-hidden rounded-lg border border-border bg-card"
    >
      <div className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3">
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <h2 className="text-sm font-semibold">
            {t("sync.lostChanges.title")}
          </h2>
          <p className="text-xs leading-relaxed text-muted-foreground">
            {t("sync.lostChanges.description")}
          </p>
        </div>
        {lostChanges.length > 0 ? (
          <Badge
            variant="secondary"
            className="rounded-sm px-1.5 py-0.5 text-2xs font-medium"
          >
            {t("sync.lostChanges.count", { count: lostChanges.length })}
          </Badge>
        ) : null}
      </div>

      {lostChanges.length === 0 ? (
        <div className="p-4">
          <p className="text-sm text-muted-foreground">
            {t("sync.lostChanges.empty")}
          </p>
        </div>
      ) : (
        lostChanges.map((row) => (
          <LostChangeRow
            key={row.id}
            row={row}
            expanded={expandedIds.has(row.id)}
            onToggleExpand={() => toggle(row.id)}
            onRestore={onRestore}
            onRecreate={onRecreate}
            onDiscard={onDiscard}
            now={now}
          />
        ))
      )}
    </section>
  );
}
