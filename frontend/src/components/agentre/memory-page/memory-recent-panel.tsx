import * as React from "react";
import { History } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  Button,
  formatMemoryDate,
  type MemoryWriteResult,
} from "@agentre-hub/agentre-ui";

import {
  MemoryRestorePrevious,
  MemoryUnarchive,
} from "../../../../wailsjs/go/app/App";
import type { memory_svc } from "../../../../wailsjs/go/models";

import { ownerKey, type MemoryDirectory } from "./directory";
import { runMemoryWrite } from "./memory-errors";

/** 变更动作（`memory_entity.ChangeAction`）→ 「谁做了什么」那句。 */
const ACTION_KEYS: Record<string, string> = {
  created: "memoryPage.recent.action.created",
  edited: "memoryPage.recent.action.edited",
  archived: "memoryPage.recent.action.archived",
  unarchived: "memoryPage.recent.action.unarchived",
  restored: "memoryPage.recent.action.restored",
  moved: "memoryPage.recent.action.moved",
};

/**
 * 「最近变更」：所有层近 14 天内被新增、修改或归档过的条目，按时间倒序（顺序由
 * 后端给）。每条写最近一次变更是谁做的、做了什么；有上一版的可恢复上一版，已归档的
 * 可恢复。彻底删除的条目本来就不在后端的结果里。
 */
export function MemoryRecentPanel({
  items,
  directory,
  onChanged,
}: {
  items: memory_svc.MemoryItem[];
  directory: MemoryDirectory;
  /** 写入成功后重拉（最近变更随左列目录一起加载）。 */
  onChanged: () => Promise<void>;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex min-h-0 flex-1 flex-col" data-slot="memory-recent">
      <header className="flex shrink-0 items-center gap-3 px-6 pb-3 pt-5">
        <History aria-hidden="true" className="size-4 text-muted-foreground" />
        <div className="flex min-w-0 flex-col">
          <h1 className="text-sm font-semibold text-foreground">
            {t("memoryPage.recent.title")}
          </h1>
          <span className="text-2xs text-muted-foreground">
            {t("memoryPage.recent.subtitle")}
          </span>
        </div>
      </header>
      <div
        className="min-h-0 flex-1 overflow-y-auto px-4 pb-6"
        data-selectable-text="true"
      >
        {items.length === 0 ? (
          <p className="pt-16 text-center text-xs text-muted-foreground">
            {t("memoryPage.recent.empty")}
          </p>
        ) : (
          <div className="flex flex-col gap-0.5">
            {items.map((item) => (
              <ChangeRow
                key={item.id}
                item={item}
                directory={directory}
                onChanged={onChanged}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function ChangeRow({
  item,
  directory,
  onChanged,
}: {
  item: memory_svc.MemoryItem;
  directory: MemoryDirectory;
  onChanged: () => Promise<void>;
}) {
  const { t } = useTranslation();
  const [failure, setFailure] = React.useState<MemoryWriteResult | null>(null);
  // 恢复上一版是对调：一次没回来之前再点一下会把它对调回去，所以在途时按钮不可点。
  const [busy, setBusy] = React.useState(false);
  const change = item.lastChange;
  const actor =
    change?.actor?.kind === "ai"
      ? (directory.byKey.get(
          ownerKey({ kind: "agent", id: change.actor.agentId }),
        )?.label ?? t("memoryPage.recent.unknownAgent"))
      : t("memoryPage.recent.you");
  const actionKey = ACTION_KEYS[change?.action ?? ""] ?? ACTION_KEYS.edited;
  const owner =
    directory.byKey.get(ownerKey(item.owner))?.label ??
    t("memoryPage.owner.unknown");
  const archived = item.state === "archived";

  const run = async (fn: () => Promise<unknown>) => {
    if (busy) return;
    setBusy(true);
    try {
      const result = await runMemoryWrite(fn);
      setFailure(result.ok ? null : result);
      if (result.ok) await onChanged();
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      data-testid={`memory-change-${item.id}`}
      className="flex items-start gap-2 rounded-lg px-2 py-1.5 hover:bg-accent/60 hover:on-accent"
    >
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="text-sm text-foreground">{item.summary}</span>
        <div className="flex flex-wrap items-center gap-x-1.5 text-2xs text-muted-foreground">
          <span>{t(actionKey, { actor })}</span>
          <span className="rounded-sm bg-button-fill px-1.5 py-px">
            {owner}
          </span>
          <span aria-hidden="true" className="text-decorative-foreground">
            ·
          </span>
          <span className="font-mono">{formatMemoryDate(change?.at ?? 0)}</span>
        </div>
        {failure && !failure.ok ? (
          <p role="alert" className="text-xs text-destructive-text">
            {failure.reason
              ? t("memoryPage.recent.failedWithReason", {
                  reason: failure.reason,
                })
              : t("memoryPage.recent.failed")}
          </p>
        ) : null}
      </div>
      {archived ? (
        <Button
          variant="ghost"
          size="xs"
          disabled={busy}
          onClick={() => void run(() => MemoryUnarchive(item.id))}
        >
          {t("memoryPage.recent.unarchive")}
        </Button>
      ) : null}
      {item.hasPrevious ? (
        <Button
          variant="ghost"
          size="xs"
          disabled={busy}
          onClick={() => void run(() => MemoryRestorePrevious(item.id))}
        >
          {t("memoryPage.recent.restorePrevious")}
        </Button>
      ) : null}
    </div>
  );
}
