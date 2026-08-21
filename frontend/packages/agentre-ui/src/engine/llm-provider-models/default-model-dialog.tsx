// @ts-nocheck
import * as React from "react";
import { useUiTranslation as useTranslation } from "../../i18n";
import { Loader2, RefreshCw } from "lucide-react";

import { Button } from "../ui/button";
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";

import {
  LLMProviderRefCounts,
  SetLLMModelDefault,
} from "../port-bridge";
import { llm_provider_svc } from "../port-bridge";
import {
  type Model,
  type Provider,
  type ReferenceCounts,
  errMessage,
  totalReferences,
} from "./index";

// DefaultModelTarget 一次「把某模型设为供应商默认模型」的确认请求。spec 2026-08-11
// 「Provider management」：修改 Provider 默认模型前，界面先展示将动态影响的
// Backend / Session / Route 数量并二次确认；保存不改写这些引用、不批量插入会话
// notice —— 默认变化自下一轮起被 provider-default 动态跟随。
export type DefaultModelTarget = { provider: Provider; model: Model };

type DialogState =
  | { phase: "loading" }
  | { phase: "confirm"; counts: ReferenceCounts }
  | { phase: "saving" };

function refsDetail(
  counts: ReferenceCounts,
  t: (key: string) => string,
): string {
  const parts: string[] = [];
  if (counts.backends > 0) {
    parts.push(
      `${counts.backends} ${t("llmProviders.defaultSet.refBackends")}`,
    );
  }
  if (counts.sessions > 0) {
    parts.push(
      `${counts.sessions} ${t("llmProviders.defaultSet.refSessions")}`,
    );
  }
  if (counts.routes > 0) {
    parts.push(`${counts.routes} ${t("llmProviders.defaultSet.refRoutes")}`);
  }
  return parts.join(" · ") || t("llmProviders.defaultSet.noRefs");
}

export function DefaultModelDialog({
  target,
  onClose,
  onSaved,
}: {
  target: DefaultModelTarget | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const [state, setState] = React.useState<DialogState>({ phase: "loading" });
  const [error, setError] = React.useState<string | null>(null);
  const open = target !== null;

  React.useEffect(() => {
    if (!target) return;
    let cancelled = false;
    setState({ phase: "loading" });
    setError(null);
    void (async () => {
      try {
        const resp = await LLMProviderRefCounts(
          new llm_provider_svc.ProviderRefCountsRequest({
            providerKey: target.provider.providerKey,
          }),
        );
        if (cancelled) return;
        const counts = resp.counts ?? {
          backends: 0,
          sessions: 0,
          routes: 0,
        };
        setState({ phase: "confirm", counts });
      } catch (err) {
        if (!cancelled) setError(errMessage(err));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [target]);

  const confirm = React.useCallback(async () => {
    if (!target) return;
    setState({ phase: "saving" });
    try {
      await SetLLMModelDefault(
        new llm_provider_svc.SetModelDefaultRequest({
          providerId: target.provider.id,
          modelKey: target.model.modelKey,
        }),
      );
      onSaved();
      onClose();
    } catch (err) {
      setError(errMessage(err));
      setState({
        phase: "confirm",
        counts: { backends: 0, sessions: 0, routes: 0 },
      });
    }
  }, [onClose, onSaved, target]);

  const saving = state.phase === "saving";
  const counts =
    state.phase === "confirm"
      ? state.counts
      : { backends: 0, sessions: 0, routes: 0 };
  const hasRefs = totalReferences(counts) > 0;

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next && !saving) onClose();
      }}
    >
      <DialogContent className="max-w-[440px]">
        <DialogHeader>
          <DialogTitle>
            {t("llmProviders.defaultSet.title", {
              model: target?.model.modelId ?? "",
              provider: target?.provider.name ?? "",
            })}
          </DialogTitle>
          <DialogDescription>
            {t("llmProviders.defaultSet.description")}
          </DialogDescription>
        </DialogHeader>

        <DialogBody className="space-y-3">
          {error ? (
            <p className="text-2xs text-status-error">{error}</p>
          ) : state.phase === "loading" ? (
            <div className="flex items-center gap-2 text-2xs text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
              {t("llmProviders.defaultSet.checking")}
            </div>
          ) : (
            <>
              <p className="text-2xs leading-relaxed text-muted-foreground">
                {t("llmProviders.defaultSet.confirmHint", {
                  model: target?.model.modelId ?? "",
                })}
              </p>
              {hasRefs ? (
                <div
                  role="alert"
                  className="flex flex-col gap-2 rounded-md border border-status-waiting/40 bg-status-waiting-bg px-3 py-2.5"
                >
                  <div className="flex items-start gap-2 text-xs">
                    <RefreshCw
                      className="mt-0.5 size-4 shrink-0 text-status-waiting"
                      aria-hidden="true"
                    />
                    <div className="flex flex-col gap-0.5">
                      <span className="font-semibold text-status-waiting">
                        {t("llmProviders.defaultSet.impactTitle")}
                      </span>
                      <span className="text-2xs leading-relaxed text-muted-foreground">
                        {t("llmProviders.defaultSet.impactDetail", {
                          refs: refsDetail(counts, t),
                        })}
                      </span>
                    </div>
                  </div>
                </div>
              ) : null}
              <p className="text-2xs leading-relaxed text-muted-foreground">
                {t("llmProviders.defaultSet.dynamicHint")}
              </p>
            </>
          )}
        </DialogBody>

        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-8 text-xs"
            onClick={onClose}
            disabled={saving}
          >
            {t("common.cancel")}
          </Button>
          {state.phase === "confirm" ? (
            <Button
              type="button"
              size="sm"
              className="h-8 gap-1.5 text-xs"
              onClick={() => void confirm()}
              disabled={saving}
            >
              {saving ? (
                <Loader2
                  className="size-3.5 animate-spin"
                  data-icon="inline-start"
                  aria-hidden="true"
                />
              ) : null}
              {t("llmProviders.defaultSet.confirmButton")}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
