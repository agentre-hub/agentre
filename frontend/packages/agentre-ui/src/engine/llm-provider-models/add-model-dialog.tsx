import * as React from "react";
import { useUiTranslation as useTranslation } from "../../i18n";
import { Info, Loader2, Plus } from "lucide-react";

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
import { Input } from "../ui/input";

import {
  ImportLLMModels,
  LookupLLMModel,
} from "../port-bridge";
import { llm_provider_svc } from "../port-bridge";
import { type Provider, errMessage } from "./index";

function Field({
  children,
  label,
}: {
  children: React.ReactNode;
  label: string;
}) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className="text-xs font-medium text-foreground">{label}</span>
      {children}
    </label>
  );
}

export function AddModelDialog({
  provider,
  onClose,
  onImported,
}: {
  provider: Provider | null;
  onClose: () => void;
  onImported: (resp: llm_provider_svc.ImportModelsResponse) => void;
}) {
  const { t } = useTranslation();
  const open = provider !== null;
  const [modelId, setModelId] = React.useState("");
  const [name, setName] = React.useState("");
  const [contextWindow, setContextWindow] = React.useState<number>(0);
  const [maxOutput, setMaxOutput] = React.useState<number>(0);
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [catalogFilledId, setCatalogFilledId] = React.useState("");
  const lastFilledIdRef = React.useRef("");

  React.useEffect(() => {
    if (!open) return;
    setModelId("");
    setName("");
    setContextWindow(0);
    setMaxOutput(0);
    setError(null);
    setCatalogFilledId("");
    lastFilledIdRef.current = "";
  }, [open]);

  // 内置目录命中时自动回填上下文与最大输出；同一 id 只回填一次，后续改动不被覆盖。
  const handleModelIdBlur = React.useCallback(async () => {
    const id = modelId.trim();
    if (!id || id === lastFilledIdRef.current) return;
    try {
      const resp = await LookupLLMModel(
        new llm_provider_svc.LookupModelRequest({ id }),
      );
      if (!resp.known) return;
      setContextWindow(resp.contextWindow >= 0 ? resp.contextWindow : 0);
      setMaxOutput(resp.maxOutput >= 0 ? resp.maxOutput : 0);
      lastFilledIdRef.current = id;
      // 自动改写用户看得见的字段，必须当场说明来源，否则就是静默覆盖。
      setCatalogFilledId(id);
    } catch {
      // 目录查询失败不阻塞手动添加：保留用户已填写的值。
    }
  }, [modelId]);

  const catalogMatched =
    catalogFilledId !== "" && catalogFilledId === modelId.trim();

  const submit = React.useCallback(
    async (event: React.FormEvent<HTMLFormElement>) => {
      event.preventDefault();
      if (!provider) return;
      if (!modelId.trim()) {
        setError(t("llmProviders.validation.modelIdRequired"));
        return;
      }
      setSubmitting(true);
      setError(null);
      try {
        const resp = await ImportLLMModels(
          new llm_provider_svc.ImportModelsRequest({
            providerId: provider.id,
            models: [
              new llm_provider_svc.ModelInput({
                modelId: modelId.trim(),
                name: name.trim(),
                contextWindow: contextWindow >= 0 ? contextWindow : 0,
                maxOutput: maxOutput >= 0 ? maxOutput : 0,
              }),
            ],
          }),
        );
        onImported(resp);
        onClose();
      } catch (err) {
        setError(errMessage(err));
      } finally {
        setSubmitting(false);
      }
    },
    [contextWindow, maxOutput, modelId, name, onClose, onImported, provider, t],
  );

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next && !submitting) onClose();
      }}
    >
      <DialogContent className="max-w-[440px]">
        <form
          onSubmit={submit}
          aria-label={t("llmProviders.addModel.ariaLabel")}
        >
          <DialogHeader>
            <DialogTitle>
              {t("llmProviders.addModel.title", { name: provider?.name ?? "" })}
            </DialogTitle>
            <DialogDescription>
              {t("llmProviders.addModel.description")}
            </DialogDescription>
          </DialogHeader>

          <DialogBody className="space-y-4">
            <Field label={t("llmProviders.modelEdit.modelId")}>
              <Input
                value={modelId}
                placeholder={t("llmProviders.fields.modelIdPlaceholder")}
                onChange={(e) => setModelId(e.currentTarget.value)}
                onBlur={() => void handleModelIdBlur()}
                className="h-9 font-mono text-xs"
                aria-label={t("llmProviders.modelEdit.modelId")}
              />
            </Field>
            {catalogMatched ? (
              <p className="flex items-start gap-2 rounded-md border border-border bg-secondary px-3 py-2.5 text-2xs leading-relaxed text-muted-foreground">
                <Info className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
                {t("llmProviders.addModel.catalogMatched")}
              </p>
            ) : null}
            <Field label={t("llmProviders.modelEdit.name")}>
              <Input
                value={name}
                placeholder={t("llmProviders.modelEdit.namePlaceholder")}
                onChange={(e) => setName(e.currentTarget.value)}
                className="h-9 text-sm"
                aria-label={t("llmProviders.modelEdit.name")}
              />
            </Field>
            <div className="grid grid-cols-2 gap-4">
              <Field label={t("llmProviders.modelEdit.contextWindow")}>
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
              </Field>
              <Field label={t("llmProviders.modelEdit.maxOutput")}>
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
              </Field>
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
              disabled={submitting}
            >
              {t("common.cancel")}
            </Button>
            <Button
              type="submit"
              size="sm"
              className="h-8 gap-1.5 text-xs"
              disabled={submitting}
            >
              {submitting ? (
                <Loader2
                  className="size-3.5 animate-spin"
                  data-icon="inline-start"
                  aria-hidden="true"
                />
              ) : (
                <Plus
                  className="size-3.5"
                  data-icon="inline-start"
                  aria-hidden="true"
                />
              )}
              {t("llmProviders.addModel.submit")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
