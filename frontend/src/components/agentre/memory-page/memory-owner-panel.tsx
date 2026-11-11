import * as React from "react";
import { Brain } from "lucide-react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import {
  Button,
  Input,
  MemoryBudgetMeter,
  MemoryEntryGroups,
  Skeleton,
  Switch,
  type MemoryMoveTarget,
  type MemoryRowAction,
} from "@agentre-hub/agentre-ui";

import {
  MemoryArchive,
  MemoryCreate,
  MemoryDelete,
  MemoryMove,
  MemoryRestorePrevious,
  MemorySetPinned,
  MemoryUnarchive,
  MemoryUpdate,
  SetAgentMemoryEnabled,
} from "../../../../wailsjs/go/app/App";
import { app } from "../../../../wailsjs/go/models";

import {
  ownerKey,
  type MemoryDirectory,
  type MemoryDirectoryEntry,
} from "./directory";
import { memoryWriteFailure, runMemoryWrite } from "./memory-errors";
import { OwnerMark } from "./owner-mark";
import { toEntryView, type CreatorContext } from "./to-entry-view";
import { useOwnerMemory } from "./use-owner-memory";

/** 「移到…」的候选：除了条目当前所在层以外的所有层，按左列的分组与顺序。 */
function moveTargets(
  directory: MemoryDirectory,
  currentKey: string,
  groupLabels: { departments: string; agents: string; projects: string },
): MemoryMoveTarget[] {
  const targets: MemoryMoveTarget[] = [];
  const push = (entries: MemoryDirectoryEntry[], group?: string) => {
    for (const entry of entries) {
      if (entry.key === currentKey) continue;
      targets.push({
        key: entry.key,
        label: entry.label,
        group,
        depth: entry.depth,
      });
    }
  };
  push([directory.global]);
  push(directory.departments, groupLabels.departments);
  push(directory.agents, groupLabels.agents);
  push(directory.projects, groupLabels.projects);
  return targets;
}

export function MemoryOwnerPanel({
  entry,
  directory,
  creator,
  refreshSignal,
  onChanged,
  onOpenOwner,
}: {
  entry: MemoryDirectoryEntry;
  directory: MemoryDirectory;
  creator: CreatorContext;
  /** 同步落地 / 本机写入的计数器：变了就后台重拉。 */
  refreshSignal: number;
  /** 本页写入成功后：让左列的计数与最近变更跟上。 */
  onChanged: () => void;
  onOpenOwner: (entry: MemoryDirectoryEntry) => void;
}) {
  const { t } = useTranslation();
  const owner = entry.owner;
  const { state, reload, retry } = useOwnerMemory(owner);
  // 正在编辑哪一条归页面持有：后台重拉只换数据，不关掉编辑中的行。
  const [editingId, setEditingId] = React.useState<number | null>(null);
  const [enabledOverride, setEnabledOverride] = React.useState<boolean | null>(
    null,
  );

  const firstSignal = React.useRef(refreshSignal);
  React.useEffect(() => {
    if (refreshSignal === firstSignal.current) return;
    void reload();
  }, [refreshSignal, reload]);

  const afterWrite = React.useCallback(async () => {
    onChanged();
    await reload();
  }, [onChanged, reload]);

  const write = async (fn: () => Promise<unknown>) => {
    const result = await runMemoryWrite(fn);
    if (result.ok) await afterWrite();
    return result;
  };

  const onAction = (id: number, action: MemoryRowAction) => {
    switch (action.type) {
      case "pin":
        return write(() => MemorySetPinned(id, action.pinned));
      case "archive":
        return write(() => MemoryArchive(id));
      case "unarchive":
        return write(() => MemoryUnarchive(id));
      case "restorePrevious":
        return write(() => MemoryRestorePrevious(id));
      case "move": {
        const target = directory.byKey.get(action.targetKey);
        if (!target) return Promise.resolve({ ok: false as const });
        return write(() => MemoryMove(id, target.owner));
      }
      case "delete":
        return write(() => MemoryDelete(id));
    }
  };

  const toggleMemory = async (next: boolean) => {
    setEnabledOverride(next);
    try {
      await SetAgentMemoryEnabled({ id: owner.id, enabled: next });
      await reload();
    } catch (error) {
      const failure = memoryWriteFailure(error);
      toast.error(t("memoryPage.toggleFailed"), {
        description: failure.ok ? undefined : failure.reason,
      });
    } finally {
      setEnabledOverride(null);
    }
  };

  const groupLabels = {
    departments: t("memoryPage.sidebar.departments"),
    agents: t("memoryPage.sidebar.agents"),
    projects: t("memoryPage.sidebar.projects"),
  };
  const ready = state.status === "ready" ? state : null;
  const memoryEnabled = enabledOverride ?? ready?.list.memoryEnabled ?? true;

  return (
    <div className="flex min-h-0 flex-1 flex-col" data-slot="memory-owner">
      <header className="flex shrink-0 items-center gap-3 px-6 pb-3 pt-5">
        <OwnerMark entry={entry} />
        <div className="flex min-w-0 flex-1 flex-col">
          <h1 className="truncate text-sm font-semibold text-foreground">
            {entry.label}
          </h1>
          {entry.subtitle ? (
            <span className="truncate text-2xs text-muted-foreground">
              {entry.subtitle}
            </span>
          ) : null}
        </div>
        {ready ? (
          <MemoryBudgetMeter
            used={ready.list.usedChars}
            budget={ready.list.budget}
          />
        ) : null}
        {owner.kind === "agent" && ready ? (
          <label className="flex items-center gap-2 text-xs text-muted-foreground">
            {t("memoryPage.header.memoryEnabled")}
            <Switch
              checked={memoryEnabled}
              disabled={enabledOverride !== null}
              onCheckedChange={(next) => void toggleMemory(next)}
            />
          </label>
        ) : null}
      </header>
      {owner.kind === "agent" && ready && !memoryEnabled ? (
        <p className="px-6 pb-2 text-xs text-muted-foreground">
          {t("memoryPage.header.memoryDisabledHint")}
        </p>
      ) : null}

      <div className="px-6 pb-4">
        <AddBox owner={owner} onCreated={afterWrite} />
      </div>

      <div
        className="min-h-0 flex-1 overflow-y-auto px-4 pb-6"
        data-selectable-text="true"
      >
        {state.status === "loading" ? (
          <div
            role="status"
            aria-label={t("memoryPage.loading")}
            className="flex flex-col gap-3 px-2 pt-2"
          >
            <Skeleton className="h-4 w-3/4" />
            <Skeleton className="h-4 w-1/2" />
            <Skeleton className="h-4 w-2/3" />
          </div>
        ) : state.status === "error" ? (
          <div className="flex flex-col items-center gap-3 pt-16">
            <span className="text-sm text-destructive-text">
              {t("memoryPage.loadFailed")}
            </span>
            <Button variant="outline" size="sm" onClick={retry}>
              {t("common.retry")}
            </Button>
          </div>
        ) : (
          <>
            {state.list.carried.length +
              state.list.overBudget.length +
              state.list.archived.length ===
            0 ? (
              <EmptyState />
            ) : null}
            <MemoryEntryGroups
              normalLabel={
                owner.kind === "agent"
                  ? t("memoryPage.groups.own")
                  : t("memoryPage.groups.layer")
              }
              normal={state.list.carried.map((item) =>
                toEntryView(item, creator),
              )}
              overBudget={state.list.overBudget.map((item) =>
                toEntryView(item, creator),
              )}
              archived={state.list.archived.map((item) =>
                toEntryView(item, creator),
              )}
              inherited={state.inherited.map((layer) => {
                const key = ownerKey(layer.owner);
                const source = directory.byKey.get(key);
                return {
                  key,
                  label: source?.label ?? t("memoryPage.owner.unknown"),
                  count:
                    (layer.carried?.length ?? 0) +
                    (layer.overBudget?.length ?? 0),
                  onOpen: () => {
                    if (source) onOpenOwner(source);
                  },
                };
              })}
              editingId={editingId}
              onEditingChange={setEditingId}
              onSave={(id, draft) =>
                write(() =>
                  MemoryUpdate({
                    id,
                    summary: draft.summary,
                    detail: draft.detail,
                  }),
                )
              }
              onAction={onAction}
              moveTargetsFor={() =>
                moveTargets(directory, entry.key, groupLabels)
              }
            />
          </>
        )}
      </div>
    </div>
  );
}

/** 添加框：输入摘要回车即新增，创建者为用户；失败时文字留着、原因写在下面。 */
function AddBox({
  owner,
  onCreated,
}: {
  owner: MemoryDirectoryEntry["owner"];
  onCreated: () => Promise<void>;
}) {
  const { t } = useTranslation();
  const [value, setValue] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [note, setNote] = React.useState<
    { kind: "failed"; reason?: string } | { kind: "existed" } | null
  >(null);

  const submit = async () => {
    const summary = value.trim();
    if (!summary || busy) return;
    setBusy(true);
    setNote(null);
    try {
      const res = await MemoryCreate(
        app.MemoryCreateRequest.createFrom({ owner, summary, detail: "" }),
      );
      setValue("");
      if (res?.existed) setNote({ kind: "existed" });
      await onCreated();
    } catch (error) {
      const failure = memoryWriteFailure(error);
      setNote({
        kind: "failed",
        reason: failure.ok ? undefined : failure.reason,
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex flex-col gap-1">
      <Input
        value={value}
        disabled={busy}
        placeholder={t("memoryPage.add.placeholder")}
        aria-label={t("memoryPage.add.placeholder")}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.nativeEvent.isComposing) {
            e.preventDefault();
            void submit();
          }
        }}
      />
      {note?.kind === "failed" ? (
        <p role="alert" className="px-1 text-xs text-destructive-text">
          {note.reason
            ? t("memoryPage.add.failedWithReason", { reason: note.reason })
            : t("memoryPage.add.failed")}
        </p>
      ) : note?.kind === "existed" ? (
        <p role="status" className="px-1 text-xs text-muted-foreground">
          {t("memoryPage.add.existed")}
        </p>
      ) : null}
    </div>
  );
}

function EmptyState() {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-center gap-2 px-6 pb-6 pt-16 text-center">
      <span className="flex size-8 items-center justify-center rounded-lg bg-primary-soft text-primary-text">
        <Brain aria-hidden="true" className="size-4" />
      </span>
      <p className="text-sm font-semibold text-foreground">
        {t("memoryPage.empty.title")}
      </p>
      <p className="text-xs text-muted-foreground">
        {t("memoryPage.empty.description")}
      </p>
    </div>
  );
}
