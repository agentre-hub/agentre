import * as React from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, Loader2, Lock, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

import { DeleteLLMModel } from "../../../../wailsjs/go/app/App";
import { llm_provider_svc } from "../../../../wailsjs/go/models";
import {
  type Model,
  type ReferenceCounts,
  errMessage,
  modelDeleteability,
  totalReferences,
} from "./index";

export type BatchDeleteResult = {
  deleted: number;
  unprocessed: number;
  error: string | null;
};

export function BatchDeleteDialog({
  models,
  defaultModelKey,
  modelRefCounts,
  onClose,
  onDone,
}: {
  models: Model[] | null;
  defaultModelKey: string;
  modelRefCounts: Map<string, ReferenceCounts>;
  onClose: () => void;
  onDone: (result: BatchDeleteResult) => void;
}) {
  const { t } = useTranslation();
  const [deleting, setDeleting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  const open = models !== null;

  // 打开弹窗时重置删除态
  React.useEffect(() => {
    if (models) {
      setDeleting(false);
      setError(null);
    }
  }, [models]);

  const groups = React.useMemo(() => {
    const list = models ?? [];
    // 只有默认模型仍被保护；被引用的模型照删，引用数改为标在「将删除」行上。
    const deletable: { model: Model; note: string | null }[] = [];
    const protectedItems: { model: Model; reason: string }[] = [];
    for (const model of list) {
      const del = modelDeleteability(model, defaultModelKey, modelRefCounts);
      if (del.kind === "default") {
        protectedItems.push({
          model,
          reason: t("llmProviders.modelsTable.batch.rowDefaultBlocked"),
        });
        continue;
      }
      const refs = totalReferences(modelRefCounts.get(model.modelKey));
      deletable.push({
        model,
        note:
          refs > 0
            ? t("llmProviders.modelsTable.batch.rowReferenced", { count: refs })
            : null,
      });
    }
    return { deletable, protectedItems };
  }, [defaultModelKey, models, modelRefCounts, t]);

  const runDelete = React.useCallback(async () => {
    setDeleting(true);
    setError(null);
    let deleted = 0;
    let failure: string | null = null;
    for (const { model } of groups.deletable) {
      try {
        await DeleteLLMModel(
          // 弹窗已逐行披露引用影响，这里带上服务层要求的二次确认标记。
          new llm_provider_svc.DeleteModelRequest({
            id: model.id,
            confirmReference: true,
          }),
        );
        deleted += 1;
      } catch (err) {
        failure = errMessage(err);
        break;
      }
    }
    const unprocessed = groups.deletable.length - deleted;
    onDone({ deleted, unprocessed, error: failure });
  }, [groups.deletable, onDone]);

  const deletableCount = groups.deletable.length;
  const protectedCount = groups.protectedItems.length;
  const selectedCount = deletableCount + protectedCount;
  const noneDeletable = deletableCount === 0;

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next && !deleting) onClose();
      }}
    >
      <DialogContent className="max-w-[440px]">
        <DialogHeader>
          <DialogTitle>
            {t("llmProviders.batchDelete.title", { count: selectedCount })}
          </DialogTitle>
          <DialogDescription>
            {protectedCount > 0
              ? t("llmProviders.batchDelete.descriptionWithProtected", {
                  protectedCount,
                  deletableCount,
                })
              : t("llmProviders.batchDelete.descriptionAllDeletable", {
                  count: selectedCount,
                })}
          </DialogDescription>
        </DialogHeader>

        <DialogBody className="space-y-3">
          {error ? <p className="text-2xs text-status-error">{error}</p> : null}

          <div className="flex flex-col gap-1.5">
            <span className="text-2xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
              {t("llmProviders.batchDelete.willDelete")}
            </span>
            {groups.deletable.length === 0 ? (
              <p className="text-2xs text-muted-foreground">
                {t("llmProviders.batchDelete.noneDeletable")}
              </p>
            ) : (
              <ul className="flex flex-col gap-1">
                {groups.deletable.map(({ model, note }) => (
                  <li key={model.id} className="flex flex-col gap-0.5">
                    <span className="truncate font-mono text-2xs text-foreground">
                      {model.name || model.modelId}
                    </span>
                    {note ? (
                      <span className="text-2xs text-status-waiting">
                        {note}
                      </span>
                    ) : null}
                  </li>
                ))}
              </ul>
            )}
          </div>

          {groups.protectedItems.length > 0 ? (
            <div className="flex flex-col gap-1.5">
              <span className="flex items-center gap-1 text-2xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                <Lock className="size-3" aria-hidden="true" />
                {t("llmProviders.batchDelete.protected")}
              </span>
              <ul className="flex flex-col gap-1">
                {groups.protectedItems.map(({ model, reason }) => (
                  <li key={model.id} className="flex flex-col gap-0.5">
                    <span className="truncate font-mono text-2xs text-foreground">
                      {model.name || model.modelId}
                    </span>
                    <span className="text-2xs text-muted-foreground">
                      {reason}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}

          {/* 后端只有单条 DeleteLLMModel：逐个调用，不是事务，必须先说清楚 */}
          <div className="flex items-start gap-2 rounded-md border border-status-waiting/40 bg-status-waiting/10 px-3 py-2.5 text-2xs leading-relaxed text-status-waiting">
            <AlertTriangle
              className="mt-0.5 size-3.5 shrink-0"
              aria-hidden="true"
            />
            <span>{t("llmProviders.batchDelete.notTransactional")}</span>
          </div>
        </DialogBody>

        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-8 text-xs"
            onClick={onClose}
            disabled={deleting}
          >
            {t("common.cancel")}
          </Button>
          <Button
            type="button"
            variant="destructive"
            size="sm"
            className="h-8 gap-1.5 text-xs"
            onClick={() => void runDelete()}
            disabled={deleting || noneDeletable}
            title={
              noneDeletable
                ? t("llmProviders.batchDelete.noneDeletable")
                : undefined
            }
          >
            {deleting ? (
              <Loader2
                className="size-3.5 animate-spin"
                data-icon="inline-start"
                aria-hidden="true"
              />
            ) : (
              <Trash2
                className="size-3.5"
                data-icon="inline-start"
                aria-hidden="true"
              />
            )}
            {t("llmProviders.batchDelete.confirmButton", {
              count: deletableCount,
            })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
