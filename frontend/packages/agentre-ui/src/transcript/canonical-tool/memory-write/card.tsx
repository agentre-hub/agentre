import * as React from "react";
import { Folder, Globe, Pencil, Undo2, Users } from "lucide-react";

import { useUiTranslation } from "../../../i18n";
import { MemoryEditForm } from "../../../memory/memory-edit-form";
import { MetaIcon } from "../../../memory/meta-icon";
import type { MemoryEntryView } from "../../../memory/types";
import { Button } from "../../../ui/button";
import { StatusMark, type StatusMarkState } from "../../../ui/status-mark";
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
  // undefined = 还在读或读不出来；null = 条目已不存在（端口明确答了 null）。
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
      // 读失败不等于条目没了：不据此判成已撤销 / 已不存在，只是不给操作。
      setEntry(undefined);
    }
  }, [memory, write.id]);

  React.useEffect(() => {
    void load();
  }, [load]);

  const exists = write.action === "exists";
  // 卡片重新挂载（重开会话）后也要认出已撤销：新增的条目已经没了；修改、归档的条目最近
  // 一次变更是用户的恢复上一版 / 恢复（与卡片上的撤销是同一个动作，决策 9）。
  const isUndone =
    undone ||
    (write.action === "created" && entry === null) ||
    (entry != null && undoneByUser(write.action, entry));
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

  const mark = isUndone ? "memoryUndone" : actionMark(action);
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
      <StatusMark state={mark} />
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
          <LayerBadge layer={write.layer} />
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
              size="icon-xs"
              aria-label={t("memoryWrite.edit")}
              title={t("memoryWrite.edit")}
              onClick={() => setEditing(true)}
            >
              <Pencil aria-hidden="true" />
            </Button>
          ) : null}
          <Button
            type="button"
            variant="outline"
            size="icon-xs"
            aria-label={t("memoryWrite.undo")}
            title={t("memoryWrite.undo")}
            disabled={busy}
            onClick={() => void undo()}
          >
            <Undo2 aria-hidden="true" />
          </Button>
        </div>
      ) : null}
    </TranscriptCard>
  );
}

/** 条目现在的样子说明这次修改 / 归档已被用户撤回。 */
function undoneByUser(
  action: MemoryWrite["action"],
  entry: MemoryEntryView,
): boolean {
  const last = entry.lastChange;
  if (last?.actor !== "user") return false;
  switch (action) {
    case "updated":
      return last.action === "restored";
    case "archived":
      return !entry.archived && last.action === "unarchived";
    default:
      return false;
  }
}

/** 所在层级：全局只有地球图标，部门 / 项目是图标加名字；图标的名字是被替换掉的「全局 / 部门 / 项目」。 */
function LayerBadge({ layer }: { layer: MemoryWriteLayer }) {
  const { t } = useUiTranslation();
  let word: string;
  let Icon: typeof Globe;
  let name: string | undefined;
  switch (layer.kind) {
    case "global":
      word = t("memoryWrite.layer.global");
      Icon = Globe;
      break;
    case "department":
      word = t("memoryWrite.layer.department");
      Icon = Users;
      name = layer.name;
      break;
    case "project":
      word = t("memoryWrite.layer.project");
      Icon = Folder;
      name = layer.name;
      break;
    default:
      return null;
  }
  return (
    <span className="inline-flex shrink-0 items-center gap-1 rounded-full bg-muted px-2 py-0.5 text-2xs text-muted-foreground">
      <MetaIcon icon={Icon} name={word} />
      {name ? <span>{name}</span> : null}
    </span>
  );
}

function actionMark(action: MemoryWrite["action"]): StatusMarkState {
  switch (action) {
    case "created":
      return "memoryCreated";
    case "updated":
      return "memoryUpdated";
    case "archived":
      return "memoryArchived";
    default:
      return "memoryExists";
  }
}
