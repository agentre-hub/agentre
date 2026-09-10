import { Check, Pencil, Trash2, X } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { Input } from "../ui/input";

import { LabelPalette } from "./label-palette";
import { toneClass } from "./tones";
import { useLabelManager, type LabelMutateResult } from "./use-label-manager";
import type { LabelMutation, LabelUsageView } from "./query-types";

const ACTION_CLASS = cn(
  "cursor-pointer rounded-md p-1 text-muted-foreground transition-colors",
  "hover:bg-secondary hover:text-foreground",
  "focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40",
  "disabled:cursor-not-allowed disabled:opacity-50",
);

export interface LabelManagerPanelProps {
  labels: LabelUsageView[];
  onLabelMutate: (mutation: LabelMutation) => LabelMutateResult;
  className?: string;
}

/**
 * 标签管理：逐行是 chip、使用数与编辑 / 删除两个动作，底部是新建（名称 + 8 档色板）。
 *
 * 编辑一行同时改名字与色调：`LabelMutation.update` 本来就一次带齐两者，拆成改名与
 * 换色两个动作会让「改完名字再换个色」变成两次往返、两个失败面。
 *
 * 删除是软删（`labels.status` 已有该语义），但**对使用者不可逆**，所以动手之前先说
 * 清爆炸半径：它会从多少个任务上消失，以及任务本身不受影响。
 */
export function LabelManagerPanel({
  labels,
  onLabelMutate,
  className,
}: LabelManagerPanelProps) {
  const { t } = useUiTranslation();
  const state = useLabelManager(onLabelMutate);

  return (
    <div className={cn("flex min-h-0 flex-col", className)}>
      {state.failed ? (
        <p
          role="alert"
          data-testid="label-manager-error"
          className="shrink-0 rounded-md bg-destructive-soft px-2 py-1.5 text-2xs text-destructive-text"
        >
          {state.error ?? t("board.labels.failed")}
        </p>
      ) : null}
      <div className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto p-1">
        {labels.length === 0 ? (
          <p className="px-2 py-3 text-center text-2xs text-muted-foreground">
            {t("board.labels.empty")}
          </p>
        ) : null}
        {labels.map((label) => (
          <div
            key={label.id}
            data-testid={`label-row-${label.id}`}
            className="flex flex-col gap-1 rounded-md px-2 py-1.5 hover:bg-secondary/40"
          >
            <div className="flex items-center gap-2">
              {state.renaming === label.id ? (
                <>
                  <Input
                    data-testid={`label-name-input-${label.id}`}
                    value={state.draftName}
                    aria-label={t("board.labels.name")}
                    onChange={(event) => state.setDraftName(event.target.value)}
                    className="h-7 flex-1 text-xs"
                  />
                  <button
                    type="button"
                    data-testid={`label-rename-confirm-${label.id}`}
                    aria-label={t("board.labels.save")}
                    disabled={!state.draftName.trim() || state.busy}
                    onClick={() =>
                      state.mutate({
                        kind: "update",
                        id: label.id,
                        name: state.draftName.trim(),
                        tone: state.draftTone,
                      })
                    }
                    className={ACTION_CLASS}
                  >
                    <Check className="size-3.5" aria-hidden="true" />
                  </button>
                  <button
                    type="button"
                    data-testid={`label-rename-cancel-${label.id}`}
                    aria-label={t("board.form.cancel")}
                    onClick={state.cancelRename}
                    className={ACTION_CLASS}
                  >
                    <X className="size-3.5" aria-hidden="true" />
                  </button>
                </>
              ) : (
                <>
                  <span
                    className={cn(
                      "inline-flex shrink-0 items-center rounded-full px-2 py-0.5 text-2xs",
                      toneClass(label.tone),
                    )}
                  >
                    {label.name}
                  </span>
                  <span
                    data-testid={`label-usage-${label.id}`}
                    className="min-w-0 flex-1 truncate text-2xs text-muted-foreground"
                  >
                    {t("board.labels.usage", { count: label.usageCount })}
                  </span>
                  <button
                    type="button"
                    data-testid={`label-rename-${label.id}`}
                    aria-label={t("board.labels.rename")}
                    onClick={() =>
                      state.startRename(label.id, label.name, label.tone)
                    }
                    className={ACTION_CLASS}
                  >
                    <Pencil className="size-3.5" aria-hidden="true" />
                  </button>
                  <button
                    type="button"
                    data-testid={`label-delete-${label.id}`}
                    aria-label={t("board.delete")}
                    onClick={() => state.askDelete(label.id)}
                    className={ACTION_CLASS}
                  >
                    <Trash2 className="size-3.5" aria-hidden="true" />
                  </button>
                </>
              )}
            </div>
            {state.renaming === label.id ? (
              // 换色与改名同在一行编辑态里：色板就是取值域，自由取色不开放。
              <LabelPalette
                value={state.draftTone}
                onChange={state.setDraftTone}
                testIdPrefix={`label-${label.id}`}
              />
            ) : null}
            {state.deleting === label.id ? (
              <div className="flex flex-col gap-1.5 rounded-md bg-destructive-soft px-2 py-1.5">
                <p
                  data-testid={`label-delete-warning-${label.id}`}
                  className="text-2xs text-destructive-text"
                >
                  {t("board.labels.deleteWarning", { count: label.usageCount })}
                </p>
                <div className="flex items-center justify-end gap-1.5">
                  <button
                    type="button"
                    data-testid={`label-delete-cancel-${label.id}`}
                    onClick={state.cancelDelete}
                    className="cursor-pointer rounded-md px-2 py-1 text-2xs text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40"
                  >
                    {t("board.form.cancel")}
                  </button>
                  <button
                    type="button"
                    data-testid={`label-delete-confirm-${label.id}`}
                    disabled={state.busy}
                    onClick={() =>
                      state.mutate({ kind: "delete", id: label.id })
                    }
                    className="cursor-pointer rounded-md bg-destructive px-2 py-1 text-2xs text-destructive-foreground transition-colors hover:bg-destructive/90 focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-destructive/60"
                  >
                    {t("board.delete")}
                  </button>
                </div>
              </div>
            ) : null}
          </div>
        ))}
      </div>
      <div className="flex shrink-0 flex-col gap-2 border-t border-border-strong p-2">
        <div className="flex items-center gap-1.5">
          <Input
            data-testid="label-new-name"
            value={state.newName}
            aria-label={t("board.labels.newName")}
            placeholder={t("board.labels.newName")}
            onChange={(event) => state.setNewName(event.target.value)}
            className="h-7 flex-1 text-xs"
          />
          <button
            type="button"
            data-testid="label-create"
            disabled={!state.newName.trim() || state.busy}
            onClick={() =>
              state.mutate({
                kind: "create",
                name: state.newName.trim(),
                tone: state.newTone,
              })
            }
            className="h-7 cursor-pointer rounded-md bg-primary px-2.5 text-2xs font-medium text-primary-foreground transition-colors hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {t("board.labels.create")}
          </button>
        </div>
        <LabelPalette value={state.newTone} onChange={state.setNewTone} />
      </div>
    </div>
  );
}
