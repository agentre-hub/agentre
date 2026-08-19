import * as React from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, Loader2, PowerOff, Trash2 } from "lucide-react";

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

import {
  DeleteLLMModel,
  DeleteLLMProvider,
  LLMModelRefCounts,
  LLMProviderRefCounts,
  SetLLMModelEnabled,
  SetLLMProviderEnabled,
} from "../../../../wailsjs/go/app/App";
import { llm_provider_svc } from "../../../../wailsjs/go/models";
import {
  type Model,
  type Provider,
  type ReferenceCounts,
  errMessage,
  totalReferences,
} from "./index";

export type DeleteTarget =
  | { kind: "provider"; provider: Provider }
  | { kind: "model"; model: Model };

export type DeleteState =
  | { phase: "loading" }
  // counts === null：引用影响没查到。它只是知情材料，不再是删除的前提。
  | { phase: "confirm"; counts: ReferenceCounts | null }
  | { phase: "deleting" }
  | { phase: "disabling" };

function refsDetail(
  counts: ReferenceCounts,
  t: (key: string) => string,
): string {
  const parts: string[] = [];
  if (counts.backends > 0) {
    parts.push(`${counts.backends} ${t("llmProviders.delete.refBackends")}`);
  }
  if (counts.sessions > 0) {
    parts.push(`${counts.sessions} ${t("llmProviders.delete.refSessions")}`);
  }
  if (counts.routes > 0) {
    parts.push(`${counts.routes} ${t("llmProviders.delete.refRoutes")}`);
  }
  return parts.join(" · ") || t("llmProviders.delete.noRefs");
}

export function DeleteDialog({
  target,
  onClose,
  onDeleted,
  onDisabled,
}: {
  target: DeleteTarget | null;
  onClose: () => void;
  onDeleted: (target: DeleteTarget) => void;
  // 删除之外的另一条路：停用保留全部引用且可恢复（弹窗内的次要出口）。
  onDisabled?: (target: DeleteTarget) => void;
}) {
  const { t } = useTranslation();
  const [state, setState] = React.useState<DeleteState>({ phase: "loading" });
  // 引用计数与 phase 分开存：删除/停用失败回到确认态时，影响披露不能跟着丢。
  const [counts, setCounts] = React.useState<ReferenceCounts | null>(null);
  const [error, setError] = React.useState<string | null>(null);

  const provider = target?.kind === "provider" ? target.provider : null;
  const model = target?.kind === "model" ? target.model : null;
  const open = target !== null;

  React.useEffect(() => {
    if (!target) return;
    let cancelled = false;
    setState({ phase: "loading" });
    setCounts(null);
    setError(null);
    void (async () => {
      try {
        const fetched =
          target.kind === "provider"
            ? (
                await LLMProviderRefCounts(
                  new llm_provider_svc.ProviderRefCountsRequest({
                    providerKey: target.provider.providerKey,
                  }),
                )
              ).counts
            : (
                await LLMModelRefCounts(
                  new llm_provider_svc.ModelRefCountsRequest({
                    modelKey: target.model.modelKey,
                  }),
                )
              ).counts;
        if (cancelled) return;
        const next = fetched ?? { backends: 0, sessions: 0, routes: 0 };
        setCounts(next);
        setState({ phase: "confirm", counts: next });
      } catch (err) {
        // 查不到引用影响不挡删除：只把这个缺口写在弹窗里，让用户自己判断。
        if (cancelled) return;
        setCounts(null);
        setState({ phase: "confirm", counts: null });
        void err;
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [target]);

  const confirmDelete = React.useCallback(async () => {
    if (!target) return;
    setState({ phase: "deleting" });
    try {
      // confirmReference：服务层要求「被引用的删除必须知情」，而这个弹窗就是那次知情。
      if (target.kind === "provider") {
        await DeleteLLMProvider(
          new llm_provider_svc.DeleteProviderRequest({
            id: target.provider.id,
            confirmReference: true,
          }),
        );
      } else {
        await DeleteLLMModel(
          new llm_provider_svc.DeleteModelRequest({
            id: target.model.id,
            confirmReference: true,
          }),
        );
      }
      onDeleted(target);
      onClose();
    } catch (err) {
      setError(errMessage(err));
      setState({ phase: "confirm", counts });
    }
  }, [counts, onClose, onDeleted, target]);

  // 删除之外的另一条路：停用保留全部引用且可恢复，适合「只是暂时不用」。
  const disableInstead = React.useCallback(async () => {
    if (!target) return;
    setState({ phase: "disabling" });
    setError(null);
    try {
      if (target.kind === "provider") {
        await SetLLMProviderEnabled(
          new llm_provider_svc.SetProviderEnabledRequest({
            id: target.provider.id,
            enabled: false,
          }),
        );
      } else {
        await SetLLMModelEnabled(
          new llm_provider_svc.SetModelEnabledRequest({
            id: target.model.id,
            enabled: false,
          }),
        );
      }
      onDisabled?.(target);
      onClose();
    } catch (err) {
      setError(errMessage(err));
      setState({ phase: "confirm", counts });
    }
  }, [counts, onClose, onDisabled, target]);

  const name = provider ? provider.name : model ? model.modelId : "";
  const deleting = state.phase === "deleting";
  const disabling = state.phase === "disabling";
  const busy = deleting || disabling;
  const settled = state.phase !== "loading";
  const referenced = counts !== null && totalReferences(counts) > 0;
  const countsUnavailable = settled && counts === null;

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next && !busy) onClose();
      }}
    >
      <DialogContent className="max-w-[440px]">
        <DialogHeader>
          <DialogTitle>
            {target?.kind === "provider"
              ? t("llmProviders.delete.providerTitle", { name })
              : t("llmProviders.delete.modelTitle", { model: name })}
          </DialogTitle>
          <DialogDescription>
            {target?.kind === "provider"
              ? t("llmProviders.delete.providerDescription")
              : t("llmProviders.delete.modelDescription")}
          </DialogDescription>
        </DialogHeader>

        <DialogBody className="space-y-3">
          {/* 失败信息与正文并存：删除报错时影响披露不该跟着消失。 */}
          {error ? <p className="text-2xs text-status-error">{error}</p> : null}

          {state.phase === "loading" ? (
            <div className="flex items-center gap-2 text-2xs text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
              {t("llmProviders.delete.checking")}
            </div>
          ) : referenced && counts ? (
            <div className="flex flex-col gap-2 rounded-md border border-status-waiting/40 bg-status-waiting/10 px-3 py-2.5">
              <div className="flex items-start gap-2 text-xs text-status-waiting">
                <AlertTriangle
                  className="mt-0.5 size-4 shrink-0"
                  aria-hidden="true"
                />
                <div className="flex flex-col gap-0.5">
                  <span className="font-semibold">
                    {t("llmProviders.delete.impactLabel")}
                  </span>
                  <span className="text-2xs leading-relaxed">
                    {refsDetail(counts, t)}
                  </span>
                </div>
              </div>
              <p className="text-2xs leading-relaxed text-muted-foreground">
                {target?.kind === "provider"
                  ? t("llmProviders.delete.impactProviderHint")
                  : t("llmProviders.delete.impactModelHint")}
              </p>
            </div>
          ) : countsUnavailable ? (
            <p className="text-2xs leading-relaxed text-muted-foreground">
              {t("llmProviders.delete.refsUnavailable")}
            </p>
          ) : (
            <div className="flex flex-col gap-2 text-2xs text-muted-foreground">
              <p className="leading-relaxed">
                {target?.kind === "provider"
                  ? t("llmProviders.delete.providerConfirmHint", { name })
                  : t("llmProviders.delete.modelConfirmHint", { model: name })}
              </p>
            </div>
          )}
        </DialogBody>

        {/* 有引用时并排给两条路：删（破坏性）与停用（可恢复）。 */}
        <DialogFooter
          className={referenced ? "justify-between gap-2" : undefined}
        >
          {referenced ? (
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-8 gap-1.5 text-xs"
              onClick={() => void disableInstead()}
              disabled={busy}
              title={t("llmProviders.delete.disableInsteadHint")}
            >
              {disabling ? (
                <Loader2
                  className="size-3.5 animate-spin"
                  data-icon="inline-start"
                  aria-hidden="true"
                />
              ) : (
                <PowerOff
                  className="size-3.5"
                  data-icon="inline-start"
                  aria-hidden="true"
                />
              )}
              {t("llmProviders.delete.disableInstead")}
            </Button>
          ) : null}
          <div className="flex items-center gap-2">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-8 text-xs"
              onClick={onClose}
              disabled={busy}
            >
              {t("common.cancel")}
            </Button>
            {settled ? (
              <Button
                type="button"
                variant="destructive"
                size="sm"
                className="h-8 gap-1.5 text-xs"
                onClick={() => void confirmDelete()}
                disabled={busy}
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
                {target?.kind === "provider"
                  ? t("llmProviders.delete.providerConfirmButton")
                  : t("llmProviders.delete.modelConfirmButton")}
              </Button>
            ) : null}
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
