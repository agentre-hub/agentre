import { SquareTerminal, X, ChevronRight, ChevronDown } from "lucide-react";

import { useUiTranslation } from "../../i18n";
import { formatDuration } from "../../lib/format-duration";
import { shouldIgnoreClickForSelection } from "../../lib/copyable-text";
import { Button } from "../../ui/button";
import type { LocalCommandStatus } from "../dto";
import { TranscriptPill } from "../transcript-card";

import { useLocalCommand, useLocalCommandsAccess } from "./access";
import { isLocalCommandCollapsed } from "./collapsed";
import { OutputTerminal } from "./output-terminal";

// Status → visual style map (DRY — one place for all status styles).
const STATUS_CONFIG: Record<
  LocalCommandStatus,
  { dot: string; pill: string; labelKey: string }
> = {
  running: {
    dot: "bg-status-waiting",
    pill: "bg-status-waiting-bg text-status-waiting",
    labelKey: "localCommand.status.running",
  },
  done: {
    dot: "bg-status-running",
    pill: "bg-status-running-bg text-status-running",
    labelKey: "localCommand.status.done",
  },
  failed: {
    dot: "bg-destructive",
    pill: "bg-destructive/15 text-destructive",
    labelKey: "localCommand.status.failed",
  },
  stopped: {
    dot: "bg-muted-foreground",
    pill: "bg-muted text-muted-foreground",
    labelKey: "localCommand.status.stopped",
  },
};

export function LocalCommandCard({
  entryId,
  onOpenInTerminal,
  onStop,
}: {
  entryId: string;
  onOpenInTerminal: (id: string) => void;
  onStop?: (id: string) => void | Promise<void>;
}) {
  const { t } = useUiTranslation();
  const commands = useLocalCommandsAccess();
  const entry = useLocalCommand(entryId);

  if (!entry) return null;

  const cfg = STATUS_CONFIG[entry.status];
  const isRunning = entry.status === "running";
  const showExitCode =
    entry.status !== "running" && entry.exitCode !== undefined;
  const collapsed = isLocalCommandCollapsed(entry);
  const duration =
    entry.finishedAt !== undefined
      ? formatDuration(entry.finishedAt - entry.createdAt)
      : null;

  const statusPill = (
    <TranscriptPill className={cfg.pill}>
      <span className={`h-1.5 w-1.5 rounded-full ${cfg.dot}`} />
      {t(cfg.labelKey)}
      {showExitCode && (
        <>
          <span className="opacity-50">·</span>
          {t("localCommand.exitCode", { code: entry.exitCode })}
        </>
      )}
    </TranscriptPill>
  );

  const dismissBtn = (
    <button
      type="button"
      aria-label={t("localCommand.dismiss")}
      title={t("localCommand.dismiss")}
      className="-mr-1 inline-flex size-6 shrink-0 cursor-pointer items-center justify-center rounded-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
      onClick={(e) => {
        e.stopPropagation();
        commands.remove(entryId);
      }}
    >
      <X className="size-3.5" aria-hidden="true" />
    </button>
  );

  // ── Collapsed: one-line summary (command + status + exit + duration). ──
  if (collapsed) {
    const toggle = () => commands.toggleExpanded(entryId);
    return (
      <div
        role="button"
        tabIndex={0}
        aria-label={t("localCommand.expand")}
        onClick={(e) => {
          if (shouldIgnoreClickForSelection(e)) return;
          toggle();
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            toggle();
          }
        }}
        className="flex cursor-pointer items-center gap-2 rounded-lg border border-border bg-card px-3.5 py-2.5 text-foreground transition-colors hover:bg-accent/40 w-full max-w-measure"
      >
        <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        <SquareTerminal className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        <span
          data-selectable-text="true"
          className="min-w-0 flex-1 truncate font-mono text-aux font-semibold text-foreground"
        >
          {entry.command}
        </span>
        {duration && (
          <span className="shrink-0 text-meta tabular-nums text-muted-foreground">
            {duration}
          </span>
        )}
        {statusPill}
        {dismissBtn}
      </div>
    );
  }

  // ── Expanded: full header + output terminal. ──
  return (
    <div className="w-full max-w-measure rounded-lg border border-border bg-card text-foreground">
      {/* Header */}
      <div className="flex items-center gap-2 border-b border-border px-3.5 py-2.5">
        <SquareTerminal className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />

        {/* "本地命令" chip */}
        <span className="rounded-sm border border-border bg-muted px-1.5 py-0.5 text-meta font-semibold text-muted-foreground">
          {t("localCommand.localChip")}
        </span>

        {/* Command */}
        <span
          data-selectable-text="true"
          className="font-mono text-aux font-semibold text-foreground"
        >
          {entry.command}
        </span>

        <div className="flex-1" />

        {/* Not shared with AI marker */}
        <span className="text-meta text-muted-foreground/70">
          {t("localCommand.notSharedWithAI")}
        </span>

        {duration && (
          <span className="text-meta tabular-nums text-muted-foreground">
            {duration}
          </span>
        )}

        {/* Status pill */}
        {statusPill}

        {/* Collapse — only once finished (running stays open to stream). */}
        {!isRunning && (
          <button
            type="button"
            aria-label={t("localCommand.collapse")}
            title={t("localCommand.collapse")}
            className="-mr-1 inline-flex size-6 shrink-0 cursor-pointer items-center justify-center rounded-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            onClick={() => commands.toggleExpanded(entryId)}
          >
            <ChevronDown className="size-3.5" aria-hidden="true" />
          </button>
        )}

        {/* Dismiss — only once finished; running cards must be stopped first. */}
        {!isRunning && dismissBtn}
      </div>

      {/* Output area — read-only xterm;ANSI/OSC 交给 xterm 解释,不剥转义。 */}
      <OutputTerminal terminalId={entry.id} />

      {/* Actions — only while running */}
      {isRunning && (
        <div className="flex items-center gap-2 border-t border-border px-3.5 py-2.5">
          <div className="flex-1" />
          {onStop ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => void onStop(entryId)}
            >
              {t("localCommand.stop")}
            </Button>
          ) : null}
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={() => onOpenInTerminal(entryId)}
          >
            {t("localCommand.openInTerminal")}
          </Button>
        </div>
      )}
    </div>
  );
}
