/**
 * 供应商新建/编辑表单的全部状态与提交逻辑。
 *
 * 与 `provider-form-dialog.tsx` 的分工：那份只负责把这些值摆进对话框，本文件
 * 负责「表单此刻是什么、按下去会发生什么」——重置、拉取模型清单、校验、提交。
 *
 * `PreviewLLMModels` 与 `t` 由调用方传进来而不是在这里各取一次：那两支自己也是
 * hook，在这里再调一次会改变宿主组件的 hook 数量与顺序。
 */
import * as React from "react";

import { useUiTranslation as useTranslation } from "../../i18n";
import { llm_provider_svc } from "../port-bridge";
import { useEngineSettingsBridge } from "../port-bridge";
import {
  type Provider,
  errMessage,
  isProviderType,
  providerTypeMeta,
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

export type FlashState =
  | { kind: "ok"; text: string }
  | { kind: "err"; text: string }
  | null;

type PreviewLLMModelsFn = ReturnType<
  typeof useEngineSettingsBridge
>["PreviewLLMModels"];
type TranslateFn = ReturnType<typeof useTranslation>["t"];

export interface UseProviderFormArgs {
  mode: ProviderFormMode | null;
  onSubmit: (
    mode: ProviderFormMode,
    values: ProviderFormValues,
  ) => Promise<void>;
  PreviewLLMModels: PreviewLLMModelsFn;
  t: TranslateFn;
}

export function useProviderForm({
  mode,
  onSubmit,
  PreviewLLMModels,
  t,
}: UseProviderFormArgs) {
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
    // PreviewLLMModels 由 useEngineSettingsBridge 给出，在组件生命周期内身份常驻；
    // 列进依赖会与本 hook 既有的 provider?.id 细粒度依赖一起触发
    // preserve-manual-memoization。
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 见上
  }, [baseUrl, effectiveApiKey, isEdit, provider?.id, type]);

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

  return {
    open,
    isEdit,
    provider,
    labelIds: {
      apiKey: apiKeyLabelId,
      baseUrl: baseUrlLabelId,
      name: nameLabelId,
      providerKey: providerKeyLabelId,
      type: typeLabelId,
    },
    type,
    setType,
    name,
    setName,
    apiKey,
    setApiKey,
    baseUrl,
    setBaseUrl,
    models,
    defaultModelId,
    setDefaultModelId,
    showKey,
    setShowKey,
    submitting,
    error,
    flash,
    fetching,
    fetchedOnce,
    meta,
    fetchModels,
    addModel,
    removeModel,
    updateModel,
    submit,
  };
}

export type ProviderFormState = ReturnType<typeof useProviderForm>;
