import * as React from "react";
import { useTranslation } from "react-i18next";
import { ChevronDown, ChevronRight, TriangleAlert } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { relativeTime } from "../remote-devices/format";
import {
  entityLabel,
  formatPayload,
  lostChangeTitle,
  reasonLabel,
} from "./lost-change-utils";
import type { LostChangeView, RestoreOutcome } from "./use-sync-status";

type LostChangeRowProps = {
  row: LostChangeView;
  expanded: boolean;
  onToggleExpand: () => void;
  onRestore: (id: number) => Promise<RestoreOutcome>;
  onRecreate: (id: number) => Promise<void>;
  onDiscard: (id: number) => Promise<void>;
  /** 调用方(SyncPanel)持有的当前时间,每分钟刷新一次;渲染期间不直接调用
   * `Date.now()`(react-hooks/purity)。 */
  now: number;
};

type Busy = "restore" | "recreate" | "discard" | null;

/**
 * server 直写(浏览器在控制台上建 / 改 / 删组织架构)的行,来源设备记 `0`
 * (规格 2026-08-18「server 端的组织管理面」决策 21);后端把它记成哨兵 `server`,
 * 而这个字段落在本地库里,本次改动之前写下的行仍是 `"0"`。两种写法说的是同一件事。
 *
 * **必须显式认出它们**:`"0"` 在 JavaScript 里是真值,不认就照常走进
 * 「来自「{{device}}」」那一支,把覆盖方说成一台并不存在的「设备 0」。
 */
const SERVER_ORIGIN_DEVICES = new Set(["server", "0"]);

/**
 * 「没能同步的改动」列表的一行(R5)。默认给「恢复为我的版本」;点击恢复后
 * server 按 R4a 判定目标已被删除时,`onRestore` 的返回值带 `TargetDeleted`,
 * 这一行**就地**切换成 R5a 的「已被删除」态 —— 标题加删除线、行尾角标、
 * 按钮换成「按这份内容新建」+「丢弃这条记录」。这是恢复被拒之后的界面反应,
 * 不是提前探测:列表本身不携带「目标是否已被删除」这个信息。
 */
export function LostChangeRow({
  row,
  expanded,
  onToggleExpand,
  onRestore,
  onRecreate,
  onDiscard,
  now,
}: LostChangeRowProps) {
  const { t } = useTranslation();
  const [deleted, setDeleted] = React.useState(false);
  const [busy, setBusy] = React.useState<Busy>(null);

  const title = lostChangeTitle(row, t);
  const time = relativeTime(row.OccurredAt, now, t);
  const reason = reasonLabel(row.Reason, t);

  const originDevice = SERVER_ORIGIN_DEVICES.has(row.OriginDevice)
    ? t("sync.lostChanges.originServer")
    : row.OriginDevice;

  const subtitleParts = [time, reason];
  if (row.Reason === "overwritten" && originDevice) {
    subtitleParts.push(
      t("sync.lostChanges.overwrittenFrom", { device: originDevice }),
    );
  } else if (row.Reason === "discarded") {
    subtitleParts.push(t("sync.lostChanges.discardedDetail"));
  } else if (row.Reason === "rejected") {
    subtitleParts.push(t("sync.lostChanges.rejectedDetail"));
  }

  const handleRestore = async () => {
    setBusy("restore");
    try {
      const outcome = await onRestore(row.ID);
      if (outcome.TargetDeleted) {
        setDeleted(true);
      } else {
        toast.success(t("sync.lostChanges.restoreSucceeded"));
      }
    } catch (e) {
      toast.error(t("sync.lostChanges.restoreFailed"), {
        description: String(e),
      });
    } finally {
      setBusy(null);
    }
  };

  const handleRecreate = async () => {
    setBusy("recreate");
    try {
      await onRecreate(row.ID);
      toast.success(t("sync.lostChanges.recreateSucceeded"));
    } catch (e) {
      toast.error(t("sync.lostChanges.recreateFailed"), {
        description: String(e),
      });
    } finally {
      setBusy(null);
    }
  };

  const handleDiscard = async () => {
    setBusy("discard");
    try {
      await onDiscard(row.ID);
    } catch (e) {
      toast.error(t("sync.lostChanges.discardFailed"), {
        description: String(e),
      });
    } finally {
      setBusy(null);
    }
  };

  return (
    <div data-testid="lost-change-row" className="border-t border-border">
      <button
        type="button"
        onClick={onToggleExpand}
        aria-expanded={expanded}
        aria-label={
          expanded
            ? t("sync.lostChanges.toggleCollapse")
            : t("sync.lostChanges.toggleExpand")
        }
        className="flex w-full items-center gap-3 px-4 py-3 text-left hover:bg-accent/40"
      >
        {expanded ? (
          <ChevronDown
            className="size-3.5 shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
        ) : (
          <ChevronRight
            className="size-3.5 shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
        )}
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span
            className={cn(
              "truncate text-sm font-medium",
              deleted && "text-muted-foreground line-through",
            )}
          >
            {title}
          </span>
          <p className="text-xs text-muted-foreground">
            {subtitleParts.join(" · ")}
          </p>
        </div>
        {deleted ? (
          <Badge
            variant="outline"
            className="shrink-0 rounded-sm border-border px-1.5 py-0.5 text-2xs font-medium text-muted-foreground"
          >
            {t("sync.lostChanges.deletedBadge")}
          </Badge>
        ) : null}
      </button>

      {expanded ? (
        <div className="flex flex-col gap-3 border-t border-border bg-muted p-4">
          {deleted ? (
            <div className="flex gap-2 rounded-md border border-destructive bg-destructive-soft p-3">
              <TriangleAlert
                className="mt-0.5 size-4 shrink-0 text-destructive"
                aria-hidden="true"
              />
              <div className="flex min-w-0 flex-col gap-0.5">
                <span className="text-xs font-semibold">
                  {originDevice
                    ? t("sync.lostChanges.deletedTitle", {
                        entity: entityLabel(row.EntityType, t),
                        device: originDevice,
                      })
                    : t("sync.lostChanges.deletedTitleUnknownDevice", {
                        entity: entityLabel(row.EntityType, t),
                      })}
                </span>
                <p className="text-2xs leading-relaxed text-muted-foreground">
                  {t("sync.lostChanges.deletedDescription")}
                </p>
              </div>
            </div>
          ) : (
            <div className="flex flex-col gap-1.5">
              <span className="text-2xs font-medium text-muted-foreground">
                {t("sync.lostChanges.content")}
              </span>
              <pre
                data-selectable-text="true"
                className="overflow-x-auto rounded-md border border-border bg-code-surface p-2 font-mono text-xs whitespace-pre-wrap break-words text-code-foreground"
              >
                {formatPayload(row.PayloadJSON)}
              </pre>
            </div>
          )}
          <div className="flex flex-wrap items-center gap-2">
            {deleted ? (
              <>
                <Button
                  type="button"
                  size="sm"
                  className="h-7 text-2xs"
                  onClick={() => void handleRecreate()}
                  disabled={busy !== null}
                >
                  {busy === "recreate"
                    ? t("sync.lostChanges.recreating")
                    : t("sync.lostChanges.recreate")}
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="h-7 text-2xs text-muted-foreground"
                  onClick={() => void handleDiscard()}
                  disabled={busy !== null}
                >
                  {busy === "discard"
                    ? t("sync.lostChanges.discarding")
                    : t("sync.lostChanges.discard")}
                </Button>
              </>
            ) : (
              <>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="h-7 text-2xs"
                  onClick={() => void handleRestore()}
                  disabled={busy !== null}
                >
                  {busy === "restore"
                    ? t("sync.lostChanges.restoring")
                    : t("sync.lostChanges.restore")}
                </Button>
                <span className="text-2xs text-muted-foreground">
                  {t("sync.lostChanges.restoreHint")}
                </span>
              </>
            )}
          </div>
        </div>
      ) : null}
    </div>
  );
}
