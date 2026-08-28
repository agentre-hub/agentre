/**
 * 供应商表单的「身份」那几行：类型 / 名称 / API Key / Base URL / 供应商键。
 *
 * 与模型清单（`provider-model-rows.tsx`）分开：这几行在新建与编辑两态都在，
 * 而模型清单只在新建时出现——编辑态改模型走的是独立的模型表格。
 */
import * as React from "react";
import { Eye, EyeOff, Globe, KeyRound } from "lucide-react";

import { useUiTranslation as useTranslation } from "../../i18n";
import { cn } from "../../lib/utils";
import { Input } from "../../ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../../ui/select";

import { LlmProviderLogo } from "../ai-brand-logo";
import { type Provider, providerTypeMeta, providerTypeOrder } from "./index";

export function Field({
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

/** 表单里那几个 `aria-labelledby` 的 id，由持有状态的一侧统一分配。 */
export interface ProviderFormLabelIds {
  apiKey: string;
  baseUrl: string;
  name: string;
  providerKey: string;
  type: string;
}

export interface ProviderIdentityFieldsProps {
  labelIds: ProviderFormLabelIds;
  isEdit: boolean;
  provider: Provider | null;
  meta: (typeof providerTypeMeta)[keyof typeof providerTypeMeta] | undefined;
  type: string;
  onTypeChange(type: string): void;
  name: string;
  onNameChange(name: string): void;
  apiKey: string;
  onApiKeyChange(apiKey: string): void;
  showKey: boolean;
  onToggleShowKey(): void;
  baseUrl: string;
  onBaseUrlChange(baseUrl: string): void;
}

export function ProviderIdentityFields({
  labelIds,
  isEdit,
  provider,
  meta,
  type,
  onTypeChange,
  name,
  onNameChange,
  apiKey,
  onApiKeyChange,
  showKey,
  onToggleShowKey,
  baseUrl,
  onBaseUrlChange,
}: ProviderIdentityFieldsProps) {
  const { t } = useTranslation();

  return (
    <>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <Field label={t("llmProviders.fields.type")} labelId={labelIds.type}>
          <Select value={type} onValueChange={onTypeChange} disabled={isEdit}>
            <SelectTrigger
              aria-labelledby={labelIds.type}
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

        <Field label={t("llmProviders.fields.name")} labelId={labelIds.name}>
          <Input
            value={name}
            placeholder={t("llmProviders.fields.namePlaceholder")}
            onChange={(e) => onNameChange(e.currentTarget.value)}
            className="h-9 text-sm"
            aria-labelledby={labelIds.name}
            required
          />
        </Field>
      </div>

      <Field
        label={t("llmProviders.fields.apiKey")}
        labelId={labelIds.apiKey}
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
            onChange={(e) => onApiKeyChange(e.currentTarget.value)}
            className="h-9 pr-9 font-mono text-xs"
            aria-labelledby={labelIds.apiKey}
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
            onClick={onToggleShowKey}
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
        labelId={labelIds.baseUrl}
        hint={t("llmProviders.fields.baseUrlHint", {
          url: meta?.defaultBaseUrl ?? "",
        })}
        icon={Globe}
      >
        <Input
          value={baseUrl}
          placeholder={meta?.defaultBaseUrl ?? ""}
          onChange={(e) => onBaseUrlChange(e.currentTarget.value)}
          className="h-9 font-mono text-xs"
          aria-labelledby={labelIds.baseUrl}
        />
      </Field>

      {isEdit ? (
        <Field
          label={t("llmProviders.fields.providerKey")}
          labelId={labelIds.providerKey}
          hint={t("llmProviders.fields.providerKeyHint")}
        >
          <Input
            value={provider?.providerKey ?? ""}
            readOnly
            disabled
            placeholder="—"
            className="h-9 flex-1 font-mono text-xs"
            aria-labelledby={labelIds.providerKey}
          />
        </Field>
      ) : null}
    </>
  );
}
