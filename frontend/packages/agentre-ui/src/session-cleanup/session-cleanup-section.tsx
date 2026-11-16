/**
 * 批量清理对话历史（规格 2026-11-12「批量清理」）。桌面端「数据 & 备份」与控制台
 * 「隐私」共用这一份：条件 → 预览计数 → 确认 → 执行 →（部分失败 → 重试剩下的）。
 * 宿主只经 `SessionCleanupPorts` 提供计数与删除，toast / 关标签页经 `onDone`。
 */
import * as React from "react";

import { useUiTranslation } from "../i18n";
import { Button } from "../ui/button";
import {
  DialogShell,
  DialogShellBody,
  DialogShellFooter,
  DialogShellHeader,
  DialogShellSubmit,
} from "../ui/dialog-shell";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../ui/select";
import {
  SegmentedControl,
  SegmentedControlItem,
} from "../ui/segmented-control";
import { Skeleton } from "../ui/skeleton";
import { cn } from "../lib/utils";
import {
  CLEANUP_ALL,
  type CleanupBefore,
  type CleanupCopyView,
  type CleanupExecuteView,
  type CleanupFilter,
  type CleanupPreviewView,
  type SessionCleanupPorts,
} from "./ports";

export interface SessionCleanupSectionProps {
  ports: SessionCleanupPorts;
  /** 文案变体：标题与说明不同，其余一致。 */
  variant?: "desktop" | "console";
  /** 手机形态：清理按钮占满整行。 */
  fullWidthAction?: boolean;
  className?: string;
}

type PreviewState =
  | { status: "counting" }
  | { status: "failed" }
  | { status: "ready"; view: CleanupPreviewView };

type DialogState =
  | { phase: "closed" }
  | {
      phase: "confirm" | "deleting" | "partial";
      /** 确认那一刻的计数；执行与列表都以它为准，不跟后续重新计数走。 */
      view: CleanupPreviewView;
      /** 还没删掉、下一次会送去执行的 id。 */
      remaining: string[];
      failed: CleanupExecuteView["failed"];
      error: boolean;
    };

const BEFORE_OPTIONS: CleanupBefore[] = ["30d", "90d", "180d", "all"];
const DEFAULT_FILTER: CleanupFilter = {
  before: "90d",
  agentId: CLEANUP_ALL,
  projectId: CLEANUP_ALL,
};

const EMPTY_RESULT: CleanupExecuteView = {
  deleted: 0,
  failed: [],
  pending: [],
};

/** 把一次执行结果并进累计：重试要把先前删掉的与离线待办一起交给宿主。 */
function accumulate(
  acc: CleanupExecuteView,
  next: CleanupExecuteView,
): CleanupExecuteView {
  const pending = new Map<string, number>();
  for (const p of [...acc.pending, ...next.pending]) {
    pending.set(p.label, (pending.get(p.label) ?? 0) + p.count);
  }
  return {
    deleted: acc.deleted + next.deleted,
    failed: next.failed,
    pending: [...pending].map(([label, count]) => ({ label, count })),
  };
}

export function SessionCleanupSection({
  ports,
  variant = "desktop",
  fullWidthAction = false,
  className,
}: SessionCleanupSectionProps) {
  const { t, i18n } = useUiTranslation();
  const [filter, setFilter] = React.useState<CleanupFilter>(DEFAULT_FILTER);
  const [preview, setPreview] = React.useState<PreviewState>({
    status: "counting",
  });
  const [reload, setReload] = React.useState(0);
  const [dialog, setDialog] = React.useState<DialogState>({ phase: "closed" });
  const accRef = React.useRef<CleanupExecuteView>(EMPTY_RESULT);
  const portsRef = React.useRef(ports);
  React.useEffect(() => {
    portsRef.current = ports;
  });

  React.useEffect(() => {
    let stale = false;
    setPreview({ status: "counting" });
    portsRef.current.preview(filter).then(
      (view) => {
        if (!stale) setPreview({ status: "ready", view });
      },
      () => {
        if (!stale) setPreview({ status: "failed" });
      },
    );
    return () => {
      stale = true;
    };
  }, [filter, reload]);

  const ready = preview.status === "ready" ? preview.view : null;
  const canClean = ready !== null && ready.count > 0;

  const formatDate = (ms: number) =>
    new Intl.DateTimeFormat(i18n.language, { dateStyle: "medium" }).format(ms);

  const openConfirm = () => {
    if (!ready || ready.count === 0) return;
    accRef.current = EMPTY_RESULT;
    setDialog({
      phase: "confirm",
      view: ready,
      remaining: ready.ids,
      failed: [],
      error: false,
    });
  };

  const finish = () => {
    const result = accRef.current;
    accRef.current = EMPTY_RESULT;
    setDialog({ phase: "closed" });
    if (result.deleted > 0) portsRef.current.onDone?.(result);
    setReload((n) => n + 1);
  };

  const run = async () => {
    if (dialog.phase !== "confirm" && dialog.phase !== "partial") return;
    const { view, remaining } = dialog;
    setDialog({ phase: "deleting", view, remaining, failed: [], error: false });
    let result: CleanupExecuteView;
    try {
      result = await portsRef.current.execute(remaining);
    } catch {
      setDialog({
        phase: dialog.phase,
        view,
        remaining,
        failed: dialog.failed,
        error: true,
      });
      return;
    }
    accRef.current = accumulate(accRef.current, result);
    if (result.failed.length === 0) {
      finish();
      return;
    }
    setDialog({
      phase: "partial",
      view,
      remaining: result.failed.map((f) => f.id),
      failed: result.failed,
      error: false,
    });
  };

  const close = () => {
    if (dialog.phase === "partial") finish();
    else setDialog({ phase: "closed" });
  };

  const title =
    variant === "console"
      ? t("sessionCleanup.console.title")
      : t("sessionCleanup.desktop.title");
  const description =
    variant === "console"
      ? t("sessionCleanup.console.description")
      : t("sessionCleanup.desktop.description");

  const beforeLabel = (b: CleanupBefore) => {
    switch (b) {
      case "30d":
        return t("sessionCleanup.filter.before30d");
      case "90d":
        return t("sessionCleanup.filter.before90d");
      case "180d":
        return t("sessionCleanup.filter.before180d");
      default:
        return t("sessionCleanup.filter.beforeAll");
    }
  };

  return (
    <section
      data-slot="session-cleanup"
      aria-labelledby="session-cleanup-title"
      className={cn("flex flex-col gap-4", className)}
    >
      <header className="flex flex-col gap-1">
        <h3
          id="session-cleanup-title"
          className="text-sm font-semibold tracking-tight text-foreground"
        >
          {title}
        </h3>
        <p className="text-xs leading-relaxed text-muted-foreground">
          {description}
        </p>
      </header>

      <div className="flex flex-wrap items-end gap-x-4 gap-y-3">
        <div className="flex min-w-0 flex-col gap-1.5">
          <span
            id="session-cleanup-before"
            className="text-xs text-muted-foreground"
          >
            {t("sessionCleanup.filter.before")}
          </span>
          <SegmentedControl
            aria-labelledby="session-cleanup-before"
            className="flex-wrap"
            value={filter.before}
            onValueChange={(v) =>
              setFilter((f) => ({ ...f, before: v as CleanupBefore }))
            }
          >
            {BEFORE_OPTIONS.map((b) => (
              <SegmentedControlItem key={b} value={b}>
                {beforeLabel(b)}
              </SegmentedControlItem>
            ))}
          </SegmentedControl>
        </div>
        <div className="flex min-w-40 flex-1 flex-col gap-1.5 sm:max-w-56">
          <span
            id="session-cleanup-agent"
            className="text-xs text-muted-foreground"
          >
            {t("sessionCleanup.filter.agent")}
          </span>
          <Select
            value={filter.agentId}
            onValueChange={(v) => setFilter((f) => ({ ...f, agentId: v }))}
          >
            <SelectTrigger aria-labelledby="session-cleanup-agent">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={CLEANUP_ALL}>
                {t("sessionCleanup.filter.allAgents")}
              </SelectItem>
              {ports.agents.map((o) => (
                <SelectItem key={o.id} value={o.id}>
                  {o.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="flex min-w-40 flex-1 flex-col gap-1.5 sm:max-w-56">
          <span
            id="session-cleanup-project"
            className="text-xs text-muted-foreground"
          >
            {t("sessionCleanup.filter.project")}
          </span>
          <Select
            value={filter.projectId}
            onValueChange={(v) => setFilter((f) => ({ ...f, projectId: v }))}
          >
            <SelectTrigger aria-labelledby="session-cleanup-project">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={CLEANUP_ALL}>
                {t("sessionCleanup.filter.allProjects")}
              </SelectItem>
              {ports.projects.map((o) => (
                <SelectItem key={o.id} value={o.id}>
                  {o.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>

      <div
        role="status"
        aria-busy={preview.status === "counting"}
        className={cn(
          "flex flex-wrap items-center gap-x-4 gap-y-2",
          fullWidthAction && "flex-col items-stretch",
        )}
      >
        <div className="flex min-w-0 flex-1 flex-col gap-0.5 text-sm">
          {preview.status === "counting" ? (
            <>
              <Skeleton className="h-4 w-40" />
              <Skeleton className="mt-1 h-3 w-56" />
            </>
          ) : preview.status === "failed" ? (
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-destructive-text">
                {t("sessionCleanup.summary.failed")}
              </span>
              <Button
                variant="outline"
                size="sm"
                onClick={() => setReload((n) => n + 1)}
              >
                {t("sessionCleanup.summary.retry")}
              </Button>
            </div>
          ) : (
            <>
              <span className="text-foreground">
                {preview.view.count === 0
                  ? preview.view.cutoffDate === null
                    ? t("sessionCleanup.summary.emptyAll")
                    : t("sessionCleanup.summary.emptyBefore", {
                        date: formatDate(preview.view.cutoffDate),
                      })
                  : t("sessionCleanup.summary.willDelete", {
                      count: preview.view.count,
                    })}
              </span>
              {preview.view.retained > 0 ? (
                <span className="text-xs text-muted-foreground">
                  {t("sessionCleanup.summary.retained", {
                    count: preview.view.retained,
                  })}
                </span>
              ) : null}
            </>
          )}
        </div>
        <Button
          variant="outline"
          className={cn(fullWidthAction && "w-full")}
          disabled={!canClean}
          onClick={openConfirm}
        >
          {t("sessionCleanup.action.clean", { count: ready?.count ?? 0 })}
        </Button>
      </div>

      <DialogShell
        open={dialog.phase !== "closed"}
        onOpenChange={(open) => {
          if (!open && dialog.phase !== "deleting") close();
        }}
        size="sm"
        danger
        busy={dialog.phase === "deleting"}
      >
        {dialog.phase !== "closed" ? (
          <ConfirmBody
            dialog={dialog}
            onRun={() => void run()}
            onClose={close}
          />
        ) : null}
      </DialogShell>
    </section>
  );
}

function ConfirmBody({
  dialog,
  onRun,
  onClose,
}: {
  dialog: Exclude<DialogState, { phase: "closed" }>;
  onRun: () => void;
  onClose: () => void;
}) {
  const { t } = useUiTranslation();
  const partial = dialog.phase === "partial";
  const deleting = dialog.phase === "deleting";
  const n = dialog.remaining.length;

  // 同一原因合并成一行，避免几百条同样的话刷满确认框。
  const reasons = new Map<string, number>();
  for (const f of dialog.failed)
    reasons.set(f.reason, (reasons.get(f.reason) ?? 0) + 1);

  return (
    <>
      <DialogShellHeader
        danger
        title={
          partial
            ? t("sessionCleanup.partial.title", { count: n })
            : t("sessionCleanup.confirm.title", { count: dialog.view.count })
        }
        subtitle={partial ? undefined : t("sessionCleanup.confirm.body")}
      />
      <DialogShellBody>
        {partial ? (
          <ul className="flex flex-col gap-1.5 text-sm text-foreground">
            {[...reasons].map(([reason, count]) => (
              <li key={reason}>
                {t("sessionCleanup.partial.reason", { reason, count })}
              </li>
            ))}
          </ul>
        ) : (
          <ul className="flex flex-col gap-2">
            {dialog.view.copies.map((copy, i) => (
              <CopyRow key={`${copy.kind}-${copy.label}-${i}`} copy={copy} />
            ))}
          </ul>
        )}
      </DialogShellBody>
      <DialogShellFooter
        error={dialog.error ? t("sessionCleanup.confirm.error") : null}
      >
        <Button
          variant="ghost"
          disabled={deleting}
          onClick={onClose}
          type="button"
        >
          {partial
            ? t("sessionCleanup.partial.close")
            : t("sessionCleanup.confirm.cancel")}
        </Button>
        <DialogShellSubmit
          type="button"
          variant="destructive"
          busy={deleting}
          onClick={onRun}
        >
          {deleting
            ? t("sessionCleanup.confirm.deleting")
            : partial
              ? t("sessionCleanup.partial.retry", { count: n })
              : t("sessionCleanup.confirm.delete", { count: n })}
        </DialogShellSubmit>
      </DialogShellFooter>
    </>
  );
}

function CopyRow({ copy }: { copy: CleanupCopyView }) {
  const { t } = useUiTranslation();
  const name =
    copy.kind === "local"
      ? t("sessionCleanup.confirm.copyLocal")
      : copy.kind === "account"
        ? t("sessionCleanup.confirm.copyAccount")
        : copy.label;
  const status =
    copy.kind !== "machine"
      ? null
      : copy.online === false
        ? t("sessionCleanup.confirm.offline")
        : copy.lanDirect
          ? t("sessionCleanup.confirm.lanDirect")
          : copy.online
            ? t("sessionCleanup.confirm.online")
            : null;
  return (
    <li className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5 text-sm">
      <span className="flex min-w-0 flex-col">
        <span className="truncate text-foreground">{name}</span>
        {status ? (
          <span
            className={cn(
              "text-xs",
              copy.online === false
                ? "text-status-waiting-text"
                : "text-muted-foreground",
            )}
          >
            {status}
          </span>
        ) : null}
      </span>
      <span className="shrink-0 text-xs text-muted-foreground">
        {t("sessionCleanup.confirm.rowCount", { count: copy.count })}
      </span>
    </li>
  );
}
