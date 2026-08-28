/**
 * 新建供应商时那一段可编辑的模型清单。
 *
 * 只在**新建**态出现：已存在的供应商改模型走独立的模型表格，那里有删除/默认/
 * 批量的完整语义。新建时还没有供应商行可挂，所以这里就地编一份，并要求在提交前
 * 明确选出默认模型（多模型时不预选——猜错的那次要到发起对话才发现）。
 */
import { Hash, Loader2, Plus, RefreshCw, Trash2 } from "lucide-react";

import { useUiTranslation as useTranslation } from "../../i18n";
import { Button } from "../../ui/button";
import { Input } from "../../ui/input";
import { RadioGroup, RadioGroupItem } from "../../ui/radio-group";

import { LlmModelLogo } from "../ai-brand-logo";
import { providerTypeMeta } from "./index";
import type { ProviderFormValues } from "./use-provider-form";

export interface ProviderModelRowsProps {
  type: string;
  meta: (typeof providerTypeMeta)[keyof typeof providerTypeMeta] | undefined;
  models: ProviderFormValues["models"];
  defaultModelId: string;
  onDefaultModelIdChange(modelId: string): void;
  fetching: boolean;
  onFetchModels(): void;
  onAddModel(): void;
  onRemoveModel(index: number): void;
  onUpdateModel(
    index: number,
    patch: Partial<ProviderFormValues["models"][number]>,
  ): void;
}

export function ProviderModelRows({
  type,
  meta,
  models,
  defaultModelId,
  onDefaultModelIdChange,
  fetching,
  onFetchModels,
  onAddModel,
  onRemoveModel,
  onUpdateModel,
}: ProviderModelRowsProps) {
  const { t } = useTranslation();

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="flex items-center gap-1.5 text-xs font-medium text-foreground">
          <span
            className="inline-flex size-3.5 items-center justify-center rounded-sm bg-primary-soft font-mono text-2xs font-bold text-primary-text"
            aria-hidden="true"
          >
            {meta
              ? providerTypeMeta[type as keyof typeof providerTypeMeta].badge
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
            onClick={onFetchModels}
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
            onClick={onAddModel}
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
          onValueChange={onDefaultModelIdChange}
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
                  aria-label={t("llmProviders.modelsTable.setDefaultAria", {
                    model: rowLabel,
                  })}
                />
                <LlmModelLogo
                  providerType={type}
                  model={row.modelId || "model"}
                  className="size-4"
                />
                <Input
                  value={row.modelId}
                  placeholder={t("llmProviders.fields.modelIdPlaceholder")}
                  onChange={(e) =>
                    onUpdateModel(index, {
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
                        onUpdateModel(index, {
                          contextWindow: Number.isFinite(n) && n > 0 ? n : 0,
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
                        onUpdateModel(index, {
                          maxOutput: Number.isFinite(n) && n > 0 ? n : 0,
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
                  onClick={() => onRemoveModel(index)}
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
  );
}
