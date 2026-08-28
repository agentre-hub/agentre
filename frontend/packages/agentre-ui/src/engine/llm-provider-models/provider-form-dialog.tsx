/**
 * 新建 / 编辑供应商的对话框外壳。
 *
 * 只做装配：状态与提交在 `use-provider-form.ts`，身份那几行在
 * `provider-form-fields.tsx`，新建时的模型清单在 `provider-model-rows.tsx`。
 */
import { CheckCircle2, Loader2 } from "lucide-react";

import { useUiTranslation as useTranslation } from "../../i18n";
import { Alert, AlertDescription, AlertTitle } from "../../ui/alert";
import { Button } from "../../ui/button";
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../ui/dialog";

import { useEngineSettingsBridge } from "../port-bridge";
import { cn } from "../../lib/utils";
import { ProviderIdentityFields } from "./provider-form-fields";
import { ProviderModelRows } from "./provider-model-rows";
import { useProviderForm } from "./use-provider-form";
import type { ProviderFormMode, ProviderFormValues } from "./use-provider-form";

export type { ProviderFormMode, ProviderFormValues };

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
  const { PreviewLLMModels } = useEngineSettingsBridge();
  const { t } = useTranslation();
  const form = useProviderForm({ mode, onSubmit, PreviewLLMModels, t });
  const { isEdit, provider, submitting } = form;

  const title = isEdit
    ? t("llmProviders.form.editTitle", { name: provider?.name ?? "" })
    : t("llmProviders.form.createTitle");
  const description = isEdit
    ? t("llmProviders.form.editDescription")
    : t("llmProviders.form.createDescription");

  return (
    <Dialog
      open={form.open}
      onOpenChange={(next) => {
        if (!next && !submitting) onClose();
      }}
    >
      <DialogContent className="max-w-[600px]">
        <form
          onSubmit={form.submit}
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
            <ProviderIdentityFields
              labelIds={form.labelIds}
              isEdit={isEdit}
              provider={provider}
              meta={form.meta}
              type={form.type}
              onTypeChange={form.setType}
              name={form.name}
              onNameChange={form.setName}
              apiKey={form.apiKey}
              onApiKeyChange={form.setApiKey}
              showKey={form.showKey}
              onToggleShowKey={() => form.setShowKey((s) => !s)}
              baseUrl={form.baseUrl}
              onBaseUrlChange={form.setBaseUrl}
            />

            {isEdit ? null : (
              <ProviderModelRows
                type={form.type}
                meta={form.meta}
                models={form.models}
                defaultModelId={form.defaultModelId}
                onDefaultModelIdChange={form.setDefaultModelId}
                fetching={form.fetching}
                onFetchModels={() => void form.fetchModels()}
                onAddModel={form.addModel}
                onRemoveModel={form.removeModel}
                onUpdateModel={form.updateModel}
              />
            )}

            {form.error ? (
              <p className="text-2xs text-status-error">{form.error}</p>
            ) : null}
            {form.flash ? (
              <Alert
                className={cn(
                  "py-2",
                  form.flash.kind === "ok"
                    ? "border-status-running/40 bg-status-running/10 text-status-running"
                    : "border-status-error/40 bg-status-error/10 text-status-error",
                )}
              >
                {form.flash.kind === "ok" ? (
                  <CheckCircle2 className="size-4" aria-hidden="true" />
                ) : (
                  <Loader2 className="size-4" aria-hidden="true" />
                )}
                <AlertTitle className="text-xs font-semibold">
                  {form.flash.kind === "ok"
                    ? t("common.operationSucceeded")
                    : t("common.errorOccurred")}
                </AlertTitle>
                <AlertDescription className="text-2xs">
                  {form.flash.text}
                </AlertDescription>
              </Alert>
            ) : null}
            {form.fetchedOnce && form.models.length === 0 && !form.fetching ? (
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
