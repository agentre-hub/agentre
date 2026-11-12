import * as React from "react";

import { useUiTranslation } from "../../../i18n";
import { MemoryEditForm } from "../../../memory/memory-edit-form";
import type { MemoryEntryView } from "../../../memory/types";
import { Button } from "../../../ui/button";
import { cn } from "../../../lib/utils";
import { useTranscriptPorts } from "../../ports-context";
import type { MemoryCardWriteAction, MemoryTranscriptPorts } from "../../ports";
import { TranscriptCard } from "../../transcript-card";
import { useTranscriptBooleanState } from "../../transcript-ui-state";
import type { CanonicalCardProps } from "../props";
import { RawToolCard } from "../raw/card";

import {
  parseMemoryWrite,
  type MemoryWrite,
  type MemoryWriteLayer,
} from "./parse";

// MemoryWriteCard：agent 的一次记忆写入（agrctl create / update / delete memory）在对话流里
// 的样子。宿主没接记忆端口、或命令还没有可识别的结果行时，退回通用的 RawToolCard。
export const MemoryWriteCard: React.FC<CanonicalCardProps> = (props) => {
  const { memory } = useTranscriptPorts();
  const write = parseMemoryWrite(props.toolBlock, props.resultBlock);
  if (!memory || !write) return <RawToolCard {...props} />;
  return (
    <MemoryCard
      write={write}
      memory={memory}
      sessionId={props.sessionId ?? 0}
      uiStateKey={props.uiStateKey}
    />
  );
};

type Refusal = { kind: "changed" } | { kind: "failed"; reason?: string };

function MemoryCard({
  write,
  memory,
  sessionId,
  uiStateKey,
}: {
  write: MemoryWrite;
  memory: MemoryTranscriptPorts;
  sessionId: number;
  uiStateKey?: string;
}) {
  const { t } = useUiTranslation();
  // undefined = 还在读；null = 条目已不存在。
  const [entry, setEntry] = React.useState<MemoryEntryView | null | undefined>(
    undefined,
  );
  const [undone, setUndone] = useTranscriptBooleanState(uiStateKey, false);
  const [editing, setEditing] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [refusal, setRefusal] = React.useState<Refusal | null>(null);
  // 撤销之后条目可能没了，卡片仍要显示撤销前读到的那句摘要。
  const [lastSummary, setLastSummary] = React.useState<string | undefined>();

  const load = React.useCallback(async () => {
    try {
      const next = await memory.get(write.id);
      if (next) setLastSummary(next.summary);
      setEntry(next);
    } catch {
      setEntry(null);
    }
  }, [memory, write.id]);

  React.useEffect(() => {
    void load();
  }, [load]);

  const exists = write.action === "exists";
  // 新增的写入，条目已经没了：就是被撤销（或被删）了。
  const isUndone = undone || (write.action === "created" && entry === null);
  const gone = entry === null && !isUndone;
  const summary = entry?.summary ?? lastSummary;
  const action = write.action;

  const undo = async () => {
    if (action === "exists" || busy) return;
    setBusy(true);
    setRefusal(null);
    let result;
    try {
      result = await memory.undoWrite({
        id: write.id,
        sessionId,
        action: action satisfies MemoryCardWriteAction,
      });
    } catch {
      result = { status: "failed" as const };
    }
    setBusy(false);
    switch (result.status) {
      case "undone":
        setUndone(true);
        void load();
        break;
      case "gone":
        setUndone(action === "created");
        void load();
        break;
      case "changed":
        setRefusal({ kind: "changed" });
        break;
      default:
        setRefusal({ kind: "failed", reason: result.reason });
    }
  };

  const layer = layerLabel(write.layer, t);
  const label = isUndone
    ? t("memoryWrite.label.undone")
    : actionLabel(action, t);
  const dimmed = isUndone || exists;

  if (editing && entry) {
    return (
      <div data-testid="memory-write-card" className="w-full max-w-measure">
        <MemoryEditForm
          summary={entry.summary}
          detail={entry.detail}
          onCancel={() => setEditing(false)}
          onSave={async (draft) => {
            const result = await memory.save(write.id, draft);
            if (result.ok) {
              setEditing(false);
              await load();
            }
            return result;
          }}
        />
      </div>
    );
  }

  const canAct = !isUndone && !exists && !gone && entry != null;
  const note = gone
    ? t("memoryWrite.gone")
    : refusal?.kind === "changed"
      ? t("memoryWrite.undoChanged")
      : refusal?.kind === "failed"
        ? refusal.reason
          ? t("memoryWrite.undoFailedWithReason", { reason: refusal.reason })
          : t("memoryWrite.undoFailed")
        : null;

  return (
    <TranscriptCard
      data-testid="memory-write-card"
      data-state={isUndone ? "undone" : exists ? "exists" : action}
      className="flex items-center gap-2 px-3.5 py-2.5"
    >
      <span
        className={cn(
          "shrink-0 text-xs font-medium",
          dimmed ? "text-muted-foreground" : "text-foreground",
        )}
      >
        {label}
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-2">
          {summary ? (
            <span
              className={cn(
                "min-w-0 truncate text-sm",
                dimmed ? "text-muted-foreground" : "text-foreground",
                isUndone && "line-through",
              )}
            >
              {summary}
            </span>
          ) : null}
          {layer ? (
            <span className="shrink-0 rounded-full bg-muted px-2 py-0.5 text-2xs text-muted-foreground">
              {layer}
            </span>
          ) : null}
        </div>
        {note ? (
          <p
            role={refusal ? "alert" : undefined}
            className={cn(
              "mt-0.5 text-xs",
              refusal ? "text-destructive-text" : "text-muted-foreground",
            )}
          >
            {note}
          </p>
        ) : null}
      </div>
      {canAct ? (
        <div className="flex shrink-0 items-center gap-1">
          {action === "created" || action === "updated" ? (
            <Button
              type="button"
              variant="ghost"
              size="xs"
              onClick={() => setEditing(true)}
            >
              {t("memoryWrite.edit")}
            </Button>
          ) : null}
          <Button
            type="button"
            variant="outline"
            size="xs"
            disabled={busy}
            onClick={() => void undo()}
          >
            {t("memoryWrite.undo")}
          </Button>
        </div>
      ) : null}
    </TranscriptCard>
  );
}

type Translate = (key: string, options?: Record<string, unknown>) => string;

function layerLabel(layer: MemoryWriteLayer, t: Translate): string | null {
  switch (layer.kind) {
    case "global":
      return t("memoryWrite.layer.global");
    case "department":
      return t("memoryWrite.layer.department", { name: layer.name });
    case "project":
      return t("memoryWrite.layer.project", { name: layer.name });
    default:
      return null;
  }
}

function actionLabel(action: MemoryWrite["action"], t: Translate): string {
  switch (action) {
    case "created":
      return t("memoryWrite.label.created");
    case "updated":
      return t("memoryWrite.label.updated");
    case "archived":
      return t("memoryWrite.label.archived");
    default:
      return t("memoryWrite.label.exists");
  }
}
