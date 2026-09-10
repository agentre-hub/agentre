// 「运行记录」页签:历史执行列表。

import type { TFunction } from "i18next";
import { XCircle } from "lucide-react";
import { Badge } from "@agentre-hub/agentre-ui";

import { cn } from "@/lib/utils";

import { formatRelativeTime, type HookEventItem } from "../hooks-page-model";

export function RunLogTab({
  events,
  selectedEventId,
  onSelectEvent,
  t,
}: {
  events: HookEventItem[];
  selectedEventId: number | null;
  onSelectEvent: (id: number) => void;
  t: TFunction;
}) {
  const selected = events.find((e) => e.id === selectedEventId) ?? null;
  if (events.length === 0) {
    return (
      <p className="px-2 py-8 text-center text-sm text-muted-foreground">
        {t("hooks.log.empty")}
      </p>
    );
  }
  return (
    <div className="flex gap-4">
      <div className="flex w-80 shrink-0 flex-col gap-1">
        {events.map((ev) => (
          <button
            key={ev.id}
            type="button"
            onClick={() => onSelectEvent(ev.id)}
            aria-current={ev.id === selectedEventId ? "true" : undefined}
            className={cn(
              "flex flex-col gap-1 rounded-md border px-3 py-2.5 text-left transition-colors",
              ev.id === selectedEventId
                ? "border-primary bg-primary/5"
                : "border-border hover:bg-muted/50",
            )}
          >
            <span className="flex min-w-0 items-center gap-1.5 text-xs font-medium">
              {ev.kind === "failure" ? (
                <XCircle
                  className="h-3 w-3 shrink-0 text-status-error"
                  aria-hidden
                />
              ) : null}
              <span
                className={cn(
                  "truncate",
                  ev.kind === "failure"
                    ? "text-status-error"
                    : "text-foreground",
                )}
              >
                {ev.title}
              </span>
            </span>
            <span className="font-mono text-3xs text-muted-foreground">
              {t("hooks.log.receivedAt", {
                time: formatRelativeTime(ev.receivedAt, t),
              })}
            </span>
          </button>
        ))}
      </div>
      <div className="min-w-0 flex-1">
        {selected ? (
          <div className="flex flex-col gap-3">
            <div className="flex flex-col gap-1">
              <span className="flex items-center gap-2">
                {selected.kind === "failure" ? (
                  <Badge variant="destructive" className="shrink-0">
                    {t("hooks.log.failureBadge")}
                  </Badge>
                ) : null}
                <span
                  className={cn(
                    "text-sm font-semibold",
                    selected.kind === "failure"
                      ? "text-status-error"
                      : "text-foreground",
                  )}
                >
                  {selected.title}
                </span>
              </span>
              {selected.dedupeKey ? (
                <span
                  data-selectable-text="true"
                  className="font-mono text-3xs text-muted-foreground"
                >
                  {t("hooks.log.dedupeKey")}: {selected.dedupeKey}
                </span>
              ) : null}
            </div>
            <div className="flex flex-col gap-1">
              <span className="font-mono text-3xs text-muted-foreground">
                {t("hooks.log.payload")}
              </span>
              <pre
                data-selectable-text="true"
                className="overflow-x-auto rounded-md border border-border bg-code-surface p-3 font-mono text-2xs leading-relaxed text-code-muted-foreground"
              >
                {selected.payloadJson}
              </pre>
            </div>
          </div>
        ) : (
          <p className="px-2 py-8 text-center text-sm text-muted-foreground">
            {t("hooks.log.selectPrompt")}
          </p>
        )}
      </div>
    </div>
  );
}

// ── Page ─────────────────────────────────────────────────────────────────────
