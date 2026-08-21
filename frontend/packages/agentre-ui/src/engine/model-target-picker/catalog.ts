// 从 Provider/Model 展示 DTO 组装 Picker 目录（validated catalog）。
//
// 只消费脱敏 DTO（ProviderItem / ModelItem），绝不触碰 API Key / BaseURL 之外的敏感
// 字段；provider-default 项取 Provider 当前默认模型的解析结果（defaultModel），
// fixed-model 列表取该 Provider 名下全部模型。
import * as React from "react";

import { ListLLMModels } from "../port-bridge";
import { llm_provider_svc } from "../port-bridge";

import type { PickerModel, PickerProvider } from "./types";

type ProviderItem = llm_provider_svc.ProviderItem;
type ModelItem = llm_provider_svc.ModelItem;

// buildPickerCatalog 把 providers + 每 Provider 的模型列表拼成 Picker 目录。
export function buildPickerCatalog(
  providers: ProviderItem[],
  modelsByProvider: Map<number, ModelItem[]>,
): PickerProvider[] {
  return providers.map((p) => {
    const models: PickerModel[] = (modelsByProvider.get(p.id) ?? []).map(
      (m) => ({
        modelKey: m.modelKey,
        modelId: m.modelId,
        name: m.name,
        enabled: m.enabled,
        contextWindow: m.contextWindow,
        maxOutput: m.maxOutput,
      }),
    );
    let defaultModel: PickerModel | null = null;
    if (p.defaultModelKey) {
      defaultModel =
        models.find((m) => m.modelKey === p.defaultModelKey) ?? null;
    }
    return {
      providerKey: p.providerKey,
      id: p.id,
      name: p.name,
      type: p.type,
      enabled: p.enabled,
      defaultModel,
      models,
    };
  });
}

export type ModelTargetCatalogState = {
  catalog: PickerProvider[];
  loading: boolean;
  error: boolean;
  refresh: () => void;
};

// useModelTargetCatalog 拉取每个 Provider 的模型目录并拼成 Picker 目录。
// 单个 Provider 模型拉取失败容忍降级（其余 Provider 照常可选）；全部失败才置 error。
export function useModelTargetCatalog(
  providers: ProviderItem[],
): ModelTargetCatalogState {
  const [modelsByProvider, setModelsByProvider] = React.useState<
    Map<number, ModelItem[]>
  >(new Map());
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState(false);
  const generationRef = React.useRef(0);

  const refresh = React.useCallback(() => {
    const generation = ++generationRef.current;
    setLoading(true);
    setError(false);
    void (async () => {
      const next = new Map<number, ModelItem[]>();
      let anyFailed = false;
      let anyOk = false;
      await Promise.all(
        providers.map(async (p) => {
          try {
            const resp = await ListLLMModels(
              new llm_provider_svc.ListModelsRequest({ id: p.id }),
            );
            next.set(p.id, resp.items ?? []);
            anyOk = true;
          } catch {
            anyFailed = true;
          }
        }),
      );
      if (generationRef.current !== generation) return;
      setModelsByProvider(next);
      setError(anyFailed && !anyOk);
      setLoading(false);
    })();
  }, [providers]);

  React.useEffect(() => {
    refresh();
    return () => {
      generationRef.current++;
    };
  }, [refresh]);

  return {
    catalog: buildPickerCatalog(providers, modelsByProvider),
    loading,
    error,
    refresh,
  };
}
