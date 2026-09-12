import * as React from "react";
import { useUiTranslation as useTranslation } from "../../i18n";
import { AlertTriangle, Loader2 } from "lucide-react";

import { Button } from "../../ui/button";
import { Checkbox } from "../../ui/checkbox";
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../ui/dialog";
import { Input } from "../../ui/input";

import { useEngineSettingsBridge } from "../port-bridge";
import { llm_provider_svc } from "../port-bridge";
import {
  type Model,
  type ReferenceCounts,
  errMessage,
  totalReferences,
} from "./index";

export function ModelEditDialog({
  model,
  onClose,
  onSaved,
}: {
  model: Model | null;
  onClose: () => void;
  onSaved: (updated: llm_provider_svc.ModelItem) => void;
}) {
  const { LLMModelRefCounts, UpdateLLMModel } = useEngineSettingsBridge();
  const { t } = useTranslation();
  const open = model !== null;
  const [modelId, setModelId] = React.useState("");
  const [name, setName] = React.useState("");
  const [contextWindow, setContextWindow] = React.useState<number>(0);
  const [maxOutput, setMaxOutput] = React.useState<number>(0);
  const [counts, setCounts] = React.useState<ReferenceCounts | null>(null);
  const [saving, setSaving] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [checkedRefs, setCheckedRefs] = React.useState(false);

  React.useEffect(() => {
    if (!model) return;
    let cancelled = false;
    setModelId(model.modelId);
    setName(model.name);
    setContextWindow(model.contextWindow);
    setMaxOutput(model.maxOutput);
    setCounts(null);
    setCheckedRefs(false);
    setError(null);
    void LLMModelRefCounts(
      new llm_provider_svc.ModelRefCountsRequest({ modelKey: model.modelKey }),
    )
      .then((resp) => {
        if (!cancelled) {
          setCounts(resp.counts ?? { backends: 0, sessions: 0, routes: 0 });
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(errMessage(err));
      });
    return () => {
      cancelled = true;
    };
  }, [LLMModelRefCounts, model]);

  const referenced = counts ? totalReferences(counts) > 0 : false;
  const modelIdChanged = model !== null && modelId.trim() !== model.modelId;
  const needsConfirm = referenced && modelIdChanged;
  const saveDisabled = saving || (needsConfirm && !checkedRefs);

  const submit = React.useCallback(
    async (event: React.FormEvent<HTMLFormElement>) => {
      event.preventDefault();
      if (!model) return;
      if (!modelId.trim()) {
        setError(t("llmProviders.validation.modelIdRequired"));
        return;
      }
      setSaving(true);
      setError(null);
      try {
        const resp = await UpdateLLMModel(
          new llm_provider_svc.UpdateModelRequest({
            id: model.id,
            modelId: modelId.trim(),
            name: name.trim(),
            contextWindow: contextWindow >= 0 ? contextWindow : 0,
            maxOutput: maxOutput >= 0 ? maxOutput : 0,
            confirmReference: needsConfirm,
          }),
        );
        if (resp.item) onSaved(resp.item);
        onClose();
      } catch (err) {
        setError(errMessage(err));
      } finally {
        setSaving(false);
      }
    },
    [
      UpdateLLMModel,
      contextWindow,
      maxOutput,
      model,
      modelId,
      name,
      needsConfirm,
      onClose,
      onSaved,
      t,
    ],
  );

  const modelName = model ? model.modelId : "";

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next && !saving) onClose();
      }}
    >
      <DialogContent className="max-w-[480px]">
        <form
          onSubmit={submit}
          aria-label={t("llmProviders.modelEdit.ariaLabel")}
        >
          <DialogHeader>
            <DialogTitle>
              {t("llmProviders.modelEdit.title", { model: modelName })}
            </DialogTitle>
            <DialogDescription>
              {t("llmProviders.modelEdit.description")}
            </DialogDescription>
          </DialogHeader>

          <DialogBody className="space-y-4">
            <label className="flex flex-col gap-1.5">
              <span className="flex items-center gap-1.5 text-xs font-medium text-foreground">
                {t("llmProviders.modelEdit.name")}
              </span>
              <Input
                value={name}
                placeholder={t("llmProviders.modelEdit.namePlaceholder")}
                onChange={(e) => setName(e.currentTarget.value)}
                className="h-9 text-sm"
                aria-label={t("llmProviders.modelEdit.name")}
              />
            </label>

            <label className="flex flex-col gap-1.5">
              <span className="flex items-center gap-1.5 text-xs font-medium text-foreground">
                {t("llmProviders.modelEdit.modelId")}
              </span>
              <Input
                value={modelId}
                onChange={(e) => setModelId(e.currentTarget.value)}
                className="h-9 font-mono text-xs"
                aria-label={t("llmProviders.modelEdit.modelId")}
              />
            </label>

            {/* 改 ID 的影响提示紧跟着模型 ID，读到哪里就在哪里生效 */}
            {needsConfirm && counts ? (
              <div
                role="alert"
                className="flex flex-col gap-2 rounded-md border border-status-waiting/40 bg-status-waiting/10 px-3 py-2.5"
              >
                <div className="flex items-start gap-2 text-xs text-status-waiting">
                  <AlertTriangle
                    className="mt-0.5 size-4 shrink-0"
                    aria-hidden="true"
                  />
                  <div className="flex flex-col gap-0.5">
                    <span className="font-semibold">
                      {t("llmProviders.modelEdit.referenceImpactTitle")}
                    </span>
                    <span className="text-2xs leading-relaxed">
                      {t("llmProviders.modelEdit.referenceImpactDescription", {
                        backends: counts.backends,
                        sessions: counts.sessions,
                        routes: counts.routes,
                      })}
                    </span>
                  </div>
                </div>
                <label className="flex items-start gap-2 text-2xs text-foreground">
                  <Checkbox
                    checked={checkedRefs}
                    onCheckedChange={(next) => setCheckedRefs(next === true)}
                    aria-label={t("llmProviders.modelEdit.confirmLabel")}
                  />
                  {t("llmProviders.modelEdit.confirmLabel")}
                </label>
              </div>
            ) : null}

            <div className="grid grid-cols-2 gap-4">
              <label className="flex flex-col gap-1.5">
                <span className="flex items-center gap-1.5 text-xs font-medium text-foreground">
                  {t("llmProviders.modelEdit.contextWindow")}
                </span>
                <Input
                  type="number"
                  min={0}
                  step={1}
                  inputMode="numeric"
                  value={contextWindow || ""}
                  placeholder="—"
                  onChange={(e) => {
                    const n = parseInt(e.currentTarget.value, 10);
                    setContextWindow(Number.isFinite(n) && n > 0 ? n : 0);
                  }}
                  className="h-9 font-mono text-xs"
                  aria-label={t("llmProviders.modelEdit.contextWindow")}
                />
              </label>
              <label className="flex flex-col gap-1.5">
                <span className="flex items-center gap-1.5 text-xs font-medium text-foreground">
                  {t("llmProviders.modelEdit.maxOutput")}
                </span>
                <Input
                  type="number"
                  min={0}
                  step={1}
                  inputMode="numeric"
                  value={maxOutput || ""}
                  placeholder="—"
                  onChange={(e) => {
                    const n = parseInt(e.currentTarget.value, 10);
                    setMaxOutput(Number.isFinite(n) && n > 0 ? n : 0);
                  }}
                  className="h-9 font-mono text-xs"
                  aria-label={t("llmProviders.modelEdit.maxOutput")}
                />
              </label>
            </div>

            {/* Model Key 归到最后：它是给机器看的稳定引用，只读、可选中复制 */}
            <div className="flex flex-col gap-1.5">
              <span className="flex items-center gap-1.5 text-xs font-medium text-foreground">
                {t("llmProviders.modelEdit.modelKey")}
              </span>
              <code
                data-selectable-text="true"
                className="rounded-md bg-secondary px-2.5 py-1.5 font-mono text-2xs text-muted-foreground"
              >
                {model ? model.modelKey : ""}
              </code>
              <span className="text-2xs leading-relaxed text-muted-foreground">
                {t("llmProviders.modelEdit.modelKeyHint")}
              </span>
            </div>

            {error ? (
              <p className="text-2xs text-status-error">{error}</p>
            ) : null}
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
            <Button
              type="submit"
              size="sm"
              className="h-8 gap-1.5 text-xs"
              disabled={saveDisabled}
            >
              {saving ? (
                <Loader2
                  className="size-3.5 animate-spin"
                  data-icon="inline-start"
                  aria-hidden="true"
                />
              ) : null}
              {t("common.saveChanges")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
