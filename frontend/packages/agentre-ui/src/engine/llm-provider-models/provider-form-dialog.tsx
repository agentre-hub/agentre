import * as React from "react";
import { useUiTranslation as useTranslation } from "../../i18n";
import {
  CheckCircle2,
  Eye,
  EyeOff,
  Globe,
  Hash,
  KeyRound,
  Loader2,
  Plus,
  RefreshCw,
  Trash2,
} from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "../../ui/alert";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../ui/dialog";
import { Input } from "../ui/input";
import { RadioGroup, RadioGroupItem } from "../ui/radio-group";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../ui/select";

import { PreviewLLMModels } from "../port-bridge";
import { llm_provider_svc } from "../port-bridge";
import { LlmModelLogo, LlmProviderLogo } from "../ai-brand-logo";
import { cn } from "../../lib/utils";
import {
  type Provider,
  errMessage,
  isProviderType,
  providerTypeMeta,
  providerTypeOrder,
} from "./index";

export type ProviderFormMode =
  | { kind: "create" }
  | { kind: "edit"; provider: Provider };

export type ProviderFormValues = {
  apiKey: string;
  baseUrl: string;
  defaultModelId: string;
  models: Array<{
    contextWindow: number;
    maxOutput: number;
    modelId: string;
    name: string;
  }>;
  name: string;
  type: string;
};

type FlashState =
  | { kind: "ok"; text: string }
  | { kind: "err"; text: string }
  | null;

function Field({
  children,
  className,
  hint,
  icon: Icon,
  label,
  labelId,
}: {
  children: React.ReactNode;
  className?: string;
  hint?: string;
  icon?: React.ComponentType<{ className?: string; "aria-hidden"?: boolean }>;
  label: string;
  labelId: string;
}) {
  return (
    <div className={cn("flex flex-col gap-1.5", className)}>
      <span
        id={labelId}
        className="flex items-center gap-1.5 text-xs font-medium text-foreground"
      >
        {Icon ? (
          <Icon className="size-3.5 text-muted-foreground" aria-hidden />
        ) : null}
        {label}
      </span>
      {children}
      {hint ? (
        <span className="text-2xs leading-relaxed text-muted-foreground">
          {hint}
        </span>
      ) : null}
    </div>
  );
}

export function ProviderFormDialog({
  mode,
  onClose,
  onSubmit,
}: {
  mode: ProviderFormMode | null;
  onClose: () => void;
  onSubmit: (
    mode: ProviderFormMode,
    values: ProviderFormValues,
  ) => Promise<void>;
}) {
  const { t } = useTranslation();
  const open = mode !== null;
  const isEdit = mode?.kind === "edit";
  const provider = isEdit && mode.kind === "edit" ? mode.provider : null;

  const typeLabelId = React.useId();
  const nameLabelId = React.useId();
  const apiKeyLabelId = React.useId();
  const baseUrlLabelId = React.useId();
  const providerKeyLabelId = React.useId();

  const [type, setType] = React.useState<string>("anthropic");
  const [name, setName] = React.useState("");
  const [apiKey, setApiKey] = React.useState("");
  const [baseUrl, setBaseUrl] = React.useState("");
  const [models, setModels] = React.useState<ProviderFormValues["models"]>([]);
  const [defaultModelId, setDefaultModelId] = React.useState("");
  const [showKey, setShowKey] = React.useState(false);
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [flash, setFlash] = React.useState<FlashState>(null);
  const [fetching, setFetching] = React.useState(false);
  const [fetchedOnce, setFetchedOnce] = React.useState(false);

  // 重置为 mode 对应的初始值
  React.useEffect(() => {
    if (!open) return;
    if (mode?.kind === "edit" && mode.provider) {
      setType(mode.provider.type);
      setName(mode.provider.name);
      setApiKey(mode.provider.maskedApiKey ?? "");
      setBaseUrl(mode.provider.baseUrl);
      setModels([]);
      setDefaultModelId("");
    } else {
      setType("anthropic");
      setName("");
      setApiKey("");
      setBaseUrl("");
      setModels([]);
      setDefaultModelId("");
    }
    setShowKey(false);
    setError(null);
    setFlash(null);
    setFetchedOnce(false);
  }, [mode, open]);

  const meta = isProviderType(type)
    ? providerTypeMeta[type as keyof typeof providerTypeMeta]
    : undefined;

  const maskedApiKey = provider?.maskedApiKey ?? "";
  const trimmedApiKey = apiKey.trim();
  const effectiveApiKey = isEdit
    ? trimmedApiKey === maskedApiKey.trim()
      ? ""
      : trimmedApiKey
    : trimmedApiKey;

  const fetchModels = React.useCallback(async () => {
    setFetching(true);
    setError(null);
    try {
      const resp = await PreviewLLMModels(
        new llm_provider_svc.PreviewModelsRequest({
          id: isEdit ? (provider?.id ?? 0) : 0,
          type,
          apiKey: effectiveApiKey,
          baseUrl: baseUrl.trim(),
        }),
      );
      const list = (resp.items ?? []).map((m) => ({
        modelId: m.id,
        name: "",
        contextWindow: m.contextWindow,
        maxOutput: m.maxOutput,
      }));
      setModels(list);
      setFetchedOnce(true);
      // 只有一个候选模型时预选默认；多模型时由用户明确确认
      setDefaultModelId((prev) =>
        list.length === 1
          ? list[0].modelId
          : prev && list.some((m) => m.modelId === prev)
            ? prev
            : "",
      );
    } catch (err) {
      setError(errMessage(err));
    } finally {
      setFetching(false);
    }
  }, [baseUrl, effectiveApiKey, isEdit, provider, type]);

  const addModel = React.useCallback(() => {
    setModels((prev) => [
      ...prev,
      { modelId: "", name: "", contextWindow: 0, maxOutput: 0 },
    ]);
  }, []);

  const removeModel = React.useCallback(
    (index: number) => {
      setModels((prev) => {
        const removed = prev[index];
        const next = prev.filter((_, i) => i !== index);
        if (removed && removed.modelId === defaultModelId) {
          setDefaultModelId("");
        }
        return next;
      });
    },
    [defaultModelId],
  );

  const updateModel = React.useCallback(
    (index: number, patch: Partial<ProviderFormValues["models"][number]>) => {
      setModels((prev) =>
        prev.map((row, i) => (i === index ? { ...row, ...patch } : row)),
      );
    },
    [],
  );

  const submit = React.useCallback(
    async (event: React.FormEvent<HTMLFormElement>) => {
      event.preventDefault();
      if (!mode) return;
      setError(null);
      setFlash(null);
      if (!name.trim()) {
        setError(t("llmProviders.validation.nameRequired"));
        return;
      }
      if (!isEdit && !apiKey.trim()) {
        setError(t("llmProviders.validation.apiKeyRequired"));
        return;
      }
      const normalized = models.map((m) => ({
        ...m,
        modelId: m.modelId.trim(),
        name: m.name.trim(),
      }));
      if (normalized.some((m) => !m.modelId)) {
        setError(t("llmProviders.validation.modelIdRequired"));
        return;
      }
      const seen = new Set<string>();
      for (const m of normalized) {
        if (seen.has(m.modelId)) {
          setError(
            t("llmProviders.validation.duplicateModelId", {
              modelId: m.modelId,
            }),
          );
          return;
        }
        seen.add(m.modelId);
      }
      if (normalized.length > 0 && !defaultModelId) {
        setError(t("llmProviders.validation.defaultRequired"));
        return;
      }
      setSubmitting(true);
      try {
        await onSubmit(mode, {
          type,
          name,
          apiKey: effectiveApiKey,
          baseUrl,
          models: normalized,
          defaultModelId,
        });
      } catch (err) {
        setFlash({ kind: "err", text: errMessage(err) });
      } finally {
        setSubmitting(false);
      }
    },
    [
      apiKey,
      baseUrl,
      defaultModelId,
      effectiveApiKey,
      isEdit,
      mode,
      models,
      name,
      onSubmit,
      t,
      type,
    ],
  );

  const title = isEdit
    ? t("llmProviders.form.editTitle", { name: provider?.name ?? "" })
    : t("llmProviders.form.createTitle");
  const description = isEdit
    ? t("llmProviders.form.editDescription")
    : t("llmProviders.form.createDescription");

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next && !submitting) onClose();
      }}
    >
      <DialogContent className="max-w-[600px]">
        <form
          onSubmit={submit}
          aria-label={
            isEdit
              ? t("llmProviders.form.editAriaLabel")
              : t("llmProviders.form.createAriaLabel")
          }
        >
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            <DialogDescription>{description}</DialogDescription>
          </DialogHeader>

          <DialogBody className="space-y-4">
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Field
                label={t("llmProviders.fields.type")}
                labelId={typeLabelId}
              >
                <Select value={type} onValueChange={setType} disabled={isEdit}>
                  <SelectTrigger
                    aria-labelledby={typeLabelId}
                    className="font-medium"
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {providerTypeOrder.map((key) => (
                      <SelectItem key={key} value={key}>
                        <LlmProviderLogo
                          providerType={key}
                          className="size-4 rounded-sm"
                        />
                        <span className="flex min-w-0 flex-col">
                          <span className="text-sm font-medium leading-tight">
                            {t(`llmProviders.providerType.${key}.label`)}
                          </span>
                          <span className="font-mono text-2xs text-muted-foreground leading-tight">
                            {providerTypeMeta[key].defaultBaseUrl}
                          </span>
                        </span>
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>

              <Field
                label={t("llmProviders.fields.name")}
                labelId={nameLabelId}
              >
                <Input
                  value={name}
                  placeholder={t("llmProviders.fields.namePlaceholder")}
                  onChange={(e) => setName(e.currentTarget.value)}
                  className="h-9 text-sm"
                  aria-labelledby={nameLabelId}
                  required
                />
              </Field>
            </div>

            <Field
              label={t("llmProviders.fields.apiKey")}
              labelId={apiKeyLabelId}
              hint={
                isEdit
                  ? provider?.hasApiKey
                    ? t("llmProviders.fields.apiKeyHint")
                    : t("llmProviders.fields.apiKeyMissingHint")
                  : undefined
              }
              icon={KeyRound}
            >
              <div className="relative">
                <Input
                  type={showKey ? "text" : "password"}
                  value={apiKey}
                  placeholder={t("llmProviders.fields.apiKeyPlaceholder")}
                  onChange={(e) => setApiKey(e.currentTarget.value)}
                  className="h-9 pr-9 font-mono text-xs"
                  aria-labelledby={apiKeyLabelId}
                  autoComplete="off"
                />
                <button
                  type="button"
                  aria-label={
                    showKey
                      ? t("llmProviders.fields.hideApiKey")
                      : t("llmProviders.fields.showApiKey")
                  }
                  className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                  onClick={() => setShowKey((s) => !s)}
                >
                  {showKey ? (
                    <EyeOff className="size-3.5" aria-hidden="true" />
                  ) : (
                    <Eye className="size-3.5" aria-hidden="true" />
                  )}
                </button>
              </div>
            </Field>

            <Field
              label={t("llmProviders.fields.baseUrl")}
              labelId={baseUrlLabelId}
              hint={t("llmProviders.fields.baseUrlHint", {
                url: meta?.defaultBaseUrl ?? "",
              })}
              icon={Globe}
            >
              <Input
                value={baseUrl}
                placeholder={meta?.defaultBaseUrl ?? ""}
                onChange={(e) => setBaseUrl(e.currentTarget.value)}
                className="h-9 font-mono text-xs"
                aria-labelledby={baseUrlLabelId}
              />
            </Field>

            {isEdit ? (
              <Field
                label={t("llmProviders.fields.providerKey")}
                labelId={providerKeyLabelId}
                hint={t("llmProviders.fields.providerKeyHint")}
              >
                <Input
                  value={provider?.providerKey ?? ""}
                  readOnly
                  disabled
                  placeholder="—"
                  className="h-9 flex-1 font-mono text-xs"
                  aria-labelledby={providerKeyLabelId}
                />
              </Field>
            ) : (
              <div className="flex flex-col gap-2">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="flex items-center gap-1.5 text-xs font-medium text-foreground">
                    <span
                      className="inline-flex size-3.5 items-center justify-center rounded-sm bg-primary-soft font-mono text-2xs font-bold text-primary-text"
                      aria-hidden="true"
                    >
                      {meta
                        ? providerTypeMeta[
                            type as keyof typeof providerTypeMeta
                          ].badge
                        : ""}
                    </span>
                    {t("llmProviders.fields.models")}
                  </span>
                  <div className="flex items-center gap-2">
                    <Button
                      type="button"
                      variant="outline"
                      size="xs"
                      className="h-7 gap-1.5 text-2xs"
                      onClick={() => void fetchModels()}
                      disabled={fetching}
                    >
                      {fetching ? (
                        <Loader2
                          className="size-3 animate-spin"
                          data-icon="inline-start"
                          aria-hidden="true"
                        />
                      ) : (
                        <RefreshCw
                          className="size-3"
                          data-icon="inline-start"
                          aria-hidden="true"
                        />
                      )}
                      {t("llmProviders.fields.fetchModels")}
                    </Button>
                    <Button
                      type="button"
                      variant="outline"
                      size="xs"
                      className="h-7 gap-1.5 text-2xs"
                      onClick={addModel}
                    >
                      <Plus
                        className="size-3"
                        data-icon="inline-start"
                        aria-hidden="true"
                      />
                      {t("llmProviders.fields.addModel")}
                    </Button>
                  </div>
                </div>
                <span className="text-2xs leading-relaxed text-muted-foreground">
                  {t("llmProviders.fields.modelsDescription")}
                </span>

                {models.length === 0 ? (
                  <p className="text-2xs text-muted-foreground">
                    {t("llmProviders.fields.noModels")}
                  </p>
                ) : (
                  <RadioGroup
                    value={defaultModelId}
                    onValueChange={setDefaultModelId}
                    className="gap-2"
                  >
                    {models.map((row, index) => {
                      const rowLabel = row.modelId || `#${index + 1}`;
                      const modelIdLabelId = `create-model-${index}-id`;
                      const ctxLabelId = `create-model-${index}-ctx`;
                      const outLabelId = `create-model-${index}-out`;
                      return (
                        <div
                          key={index}
                          className="flex items-center gap-2 rounded-md border border-border px-2.5 py-2"
                        >
                          <RadioGroupItem
                            value={row.modelId}
                            id={`create-model-default-${index}`}
                            disabled={!row.modelId}
                            aria-label={t(
                              "llmProviders.modelsTable.setDefaultAria",
                              { model: rowLabel },
                            )}
                          />
                          <LlmModelLogo
                            providerType={type}
                            model={row.modelId || "model"}
                            className="size-4"
                          />
                          <Input
                            value={row.modelId}
                            placeholder={t(
                              "llmProviders.fields.modelIdPlaceholder",
                            )}
                            onChange={(e) =>
                              updateModel(index, {
                                modelId: e.currentTarget.value,
                              })
                            }
                            className="h-8 min-w-0 flex-1 font-mono text-xs"
                            aria-labelledby={modelIdLabelId}
                          />
                          <span id={modelIdLabelId} className="sr-only">
                            {t("llmProviders.modelEdit.modelId")}
                          </span>
                          <div className="flex items-center gap-1.5">
                            <span className="flex items-center gap-1 text-2xs text-muted-foreground">
                              <Hash className="size-3" aria-hidden="true" />
                              <span id={ctxLabelId}>
                                {t("llmProviders.fields.ctxShort")}
                              </span>
                              <Input
                                type="number"
                                min={0}
                                step={1}
                                inputMode="numeric"
                                value={row.contextWindow || ""}
                                placeholder={t("llmProviders.fields.ctxShort")}
                                onChange={(e) => {
                                  const n = parseInt(e.currentTarget.value, 10);
                                  updateModel(index, {
                                    contextWindow:
                                      Number.isFinite(n) && n > 0 ? n : 0,
                                  });
                                }}
                                className="h-8 w-[72px] font-mono text-xs"
                                aria-labelledby={ctxLabelId}
                              />
                            </span>
                            <span className="flex items-center gap-1 text-2xs text-muted-foreground">
                              <span id={outLabelId}>
                                {t("llmProviders.fields.outShort")}
                              </span>
                              <Input
                                type="number"
                                min={0}
                                step={1}
                                inputMode="numeric"
                                value={row.maxOutput || ""}
                                placeholder={t("llmProviders.fields.outShort")}
                                onChange={(e) => {
                                  const n = parseInt(e.currentTarget.value, 10);
                                  updateModel(index, {
                                    maxOutput:
                                      Number.isFinite(n) && n > 0 ? n : 0,
                                  });
                                }}
                                className="h-8 w-[72px] font-mono text-xs"
                                aria-labelledby={outLabelId}
                              />
                            </span>
                          </div>
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon-xs"
                            className="size-7 shrink-0 text-muted-foreground"
                            aria-label={t("llmProviders.fields.removeModel", {
                              model: rowLabel,
                            })}
                            onClick={() => removeModel(index)}
                          >
                            <Trash2 className="size-3.5" aria-hidden="true" />
                          </Button>
                        </div>
                      );
                    })}
                  </RadioGroup>
                )}
                <span className="text-2xs leading-relaxed text-muted-foreground">
                  {t("llmProviders.fields.chooseDefaultHint")}
                </span>
              </div>
            )}

            {error ? (
              <p className="text-2xs text-status-error">{error}</p>
            ) : null}
            {flash ? (
              <Alert
                className={cn(
                  "py-2",
                  flash.kind === "ok"
                    ? "border-status-running/40 bg-status-running/10 text-status-running"
                    : "border-status-error/40 bg-status-error/10 text-status-error",
                )}
              >
                {flash.kind === "ok" ? (
                  <CheckCircle2 className="size-4" aria-hidden="true" />
                ) : (
                  <Loader2 className="size-4" aria-hidden="true" />
                )}
                <AlertTitle className="text-xs font-semibold">
                  {flash.kind === "ok"
                    ? t("common.operationSucceeded")
                    : t("common.errorOccurred")}
                </AlertTitle>
                <AlertDescription className="text-2xs">
                  {flash.text}
                </AlertDescription>
              </Alert>
            ) : null}
            {fetchedOnce && models.length === 0 && !fetching ? (
              <p className="text-2xs text-muted-foreground">
                {t("llmProviders.models.empty")}
              </p>
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
              ) : null}
              {isEdit
                ? t("common.saveChanges")
                : t("llmProviders.form.createSubmit")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
