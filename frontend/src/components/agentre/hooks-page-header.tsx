import type { TFunction } from "i18next";
import {
  Loader2,
  MoreHorizontal,
  Play,
  Power,
  PowerOff,
  Trash2,
} from "lucide-react";

import {
  Badge,
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@agentre-hub/agentre-ui";

import {
  formatRelativeTime,
  hookStatus,
  type Draft,
  type HookItem,
  type interpMeta,
} from "./hooks-page-model";

/** 详情区顶栏：这一条 hook 的身份、上次运行摘要，以及运行 / 启停 / 删除三个动作。 */
export function HookDetailHeader({
  draft,
  headerMeta,
  selectedHook,
  selectedId,
  running,
  busy,
  onRun,
  onToggle,
  onDelete,
  t,
}: {
  draft: Draft;
  headerMeta: ReturnType<typeof interpMeta> | null;
  selectedHook: HookItem | null;
  selectedId: number | null;
  running: boolean;
  busy: boolean;
  onRun: () => void;
  onToggle: (hook: HookItem) => void;
  onDelete: (hook: HookItem) => void;
  t: TFunction;
}) {
  return (
    <div className="flex flex-col gap-2.5 border-b border-border px-7 py-4">
      <div className="flex items-center gap-3.5">
        {headerMeta ? (
          <span
            className="flex h-11 w-11 shrink-0 items-center justify-center rounded-lg text-agent-foreground"
            style={{
              backgroundColor: `var(--color-${headerMeta.color})`,
            }}
          >
            <headerMeta.icon className="h-[18px] w-[18px]" />
          </span>
        ) : null}
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <div className="flex items-center gap-2">
            <span className="truncate text-lg font-semibold text-foreground">
              {draft.name}
            </span>
            <span className="text-muted-foreground">·</span>
            <span className="text-aux font-medium text-muted-foreground">
              {t("hooks.header.kindLabel", {
                interp: t(`hooks.interp.${draft.interpreter}`),
              })}
            </span>
            {selectedHook ? (
              <Badge variant="secondary" className="font-mono text-3xs">
                {t(`hooks.status.${hookStatus(selectedHook)}`)}
              </Badge>
            ) : null}
          </div>
          <div className="flex items-center gap-2 font-mono text-3xs text-muted-foreground">
            <span>{draft.scheduleExpr}</span>
            {selectedHook ? (
              <>
                <span>·</span>
                <span>
                  {selectedHook.lastRunAt
                    ? t("hooks.header.lastRun", {
                        time: formatRelativeTime(selectedHook.lastRunAt, t),
                      })
                    : t("hooks.header.neverRun")}
                </span>
                <span>·</span>
                <span>
                  {t("hooks.header.totalEvents", {
                    count: selectedHook.totalCount,
                  })}
                </span>
              </>
            ) : null}
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button
            type="button"
            size="sm"
            onClick={onRun}
            disabled={running || selectedId == null}
          >
            {running ? (
              <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
            ) : (
              <Play className="mr-1.5 h-3.5 w-3.5" />
            )}
            {t("hooks.header.run")}
          </Button>
          {selectedHook ? (
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => onToggle(selectedHook)}
              disabled={busy}
            >
              {selectedHook.enabled ? (
                <PowerOff className="mr-1.5 h-3.5 w-3.5" />
              ) : (
                <Power className="mr-1.5 h-3.5 w-3.5" />
              )}
              {selectedHook.enabled
                ? t("hooks.header.disable")
                : t("hooks.header.enable")}
            </Button>
          ) : null}
          {selectedHook ? (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  className="h-8 w-8"
                  aria-label={t("hooks.header.more")}
                >
                  <MoreHorizontal className="h-4 w-4" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem
                  className="text-status-error"
                  onClick={() => onDelete(selectedHook)}
                >
                  <Trash2 className="mr-2 h-3.5 w-3.5" />
                  {t("hooks.header.delete")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          ) : null}
        </div>
      </div>
    </div>
  );
}
