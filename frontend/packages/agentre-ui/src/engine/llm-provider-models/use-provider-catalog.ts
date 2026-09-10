// 供应商面板的读路径：供应商清单 / 当前选中项的模型表 / 两级引用计数，以及
// 它们各自的加载与失败态。四个副作用都在这里，写路径（use-provider-actions）
// 只通过 refreshProviders / refreshModels 回到这条读路径上来。
import * as React from "react";
import { useUiTranslation as useTranslation } from "../../i18n";

import { llm_provider_svc } from "../port-bridge";
import type { EngineSettingsBridge } from "../port-bridge";

import { type Model, type Provider, type ReferenceCounts, errMessage } from ".";

// PanelFlash 面板顶部那条结果横幅；读写两条路径共用同一个槽位。
export type PanelFlash =
  | { kind: "ok"; text: string }
  | { kind: "err"; text: string }
  | null;

export type ProviderCatalogBridge = Pick<
  EngineSettingsBridge,
  | "ListLLMModels"
  | "ListLLMProviders"
  | "LLMModelRefCounts"
  | "LLMProviderRefCounts"
>;

export function useProviderCatalog(args: {
  bridge: ProviderCatalogBridge;
  setFlash: (flash: PanelFlash) => void;
}) {
  const { setFlash } = args;
  const {
    ListLLMModels,
    ListLLMProviders,
    LLMModelRefCounts,
    LLMProviderRefCounts,
  } = args.bridge;
  const { t } = useTranslation();
  const [providers, setProviders] = React.useState<Provider[]>([]);
  const [providersLoading, setProvidersLoading] = React.useState(true);
  const [selectedId, setSelectedId] = React.useState<number | null>(null);
  const [models, setModels] = React.useState<Model[]>([]);
  const [modelsLoading, setModelsLoading] = React.useState(false);
  const [modelsError, setModelsError] = React.useState<string | null>(null);
  const [providerRefCounts, setProviderRefCounts] =
    React.useState<ReferenceCounts | null>(null);
  const [modelRefCounts, setModelRefCounts] = React.useState<
    Map<string, ReferenceCounts>
  >(new Map());
  // 行内「已通过」是会话内瞬时态：后端不持久化测试结果，刷新即消失。

  const selectedProvider = providers.find((p) => p.id === selectedId) ?? null;

  const refreshProviders = React.useCallback(async () => {
    try {
      const items = (await ListLLMProviders()).items ?? [];
      setProviders(items);
      setSelectedId((prev) =>
        prev !== null && items.some((p) => p.id === prev)
          ? prev
          : items.length > 0
            ? items[0].id
            : null,
      );
    } catch (err) {
      setFlash({
        kind: "err",
        text: t("llmProviders.flash.loadFailed", {
          message: errMessage(err),
        }),
      });
    }
  }, [ListLLMProviders, setFlash, t]);

  const refreshModels = React.useCallback(async () => {
    if (selectedId === null) return;
    try {
      const resp = await ListLLMModels(
        new llm_provider_svc.ListModelsRequest({ id: selectedId }),
      );
      setModels(resp.items ?? []);
      setModelsError(null);
    } catch (err) {
      setModelsError(errMessage(err));
    }
  }, [ListLLMModels, selectedId]);

  React.useEffect(() => {
    let mounted = true;
    void (async () => {
      try {
        const items = (await ListLLMProviders()).items ?? [];
        if (!mounted) return;
        setProviders(items);
        setSelectedId(items.length > 0 ? items[0].id : null);
      } catch (err) {
        if (mounted) {
          setFlash({
            kind: "err",
            text: t("llmProviders.flash.loadFailed", {
              message: errMessage(err),
            }),
          });
        }
      } finally {
        if (mounted) setProvidersLoading(false);
      }
    })();
    return () => {
      mounted = false;
    };
  }, [ListLLMProviders, setFlash, t]);

  React.useEffect(() => {
    if (selectedId === null) {
      setModels([]);
      setModelsLoading(false);
      setModelsError(null);
      return;
    }
    let cancelled = false;
    setModelsLoading(true);
    setModelsError(null);
    void (async () => {
      try {
        const resp = await ListLLMModels(
          new llm_provider_svc.ListModelsRequest({ id: selectedId }),
        );
        if (!cancelled) setModels(resp.items ?? []);
      } catch (err) {
        if (!cancelled) setModelsError(errMessage(err));
      } finally {
        if (!cancelled) setModelsLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [ListLLMModels, selectedId]);

  // 元信息行的「被引用」计数：切换供应商时按 providerKey 拉取，失败降级为 0。
  React.useEffect(() => {
    if (!selectedProvider) {
      setProviderRefCounts(null);
      return;
    }
    const providerKey = selectedProvider.providerKey;
    let cancelled = false;
    setProviderRefCounts(null);
    void (async () => {
      try {
        const resp = await LLMProviderRefCounts(
          new llm_provider_svc.ProviderRefCountsRequest({ providerKey }),
        );
        if (!cancelled) {
          setProviderRefCounts(
            resp.counts ?? { backends: 0, sessions: 0, routes: 0 },
          );
        }
      } catch {
        if (!cancelled) {
          setProviderRefCounts({ backends: 0, sessions: 0, routes: 0 });
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [LLMProviderRefCounts, selectedProvider]);

  // 模型表「引用」列：按 modelKey 逐条拉取引用计数。新目录先清掉旧计数；单条
  // 失败保持 unknown，绝不能把「未确认」降级成 0 后开放批量删除。
  React.useEffect(() => {
    const keys = models.map((m) => m.modelKey);
    setModelRefCounts(new Map());
    if (keys.length === 0) return;
    let cancelled = false;
    void (async () => {
      const entries = await Promise.all(
        keys.map(async (key): Promise<[string, ReferenceCounts] | null> => {
          try {
            const resp = await LLMModelRefCounts(
              new llm_provider_svc.ModelRefCountsRequest({ modelKey: key }),
            );
            return [
              key,
              resp.counts ?? { backends: 0, sessions: 0, routes: 0 },
            ];
          } catch {
            return null;
          }
        }),
      );
      if (!cancelled) {
        setModelRefCounts(
          new Map(
            entries.filter(
              (entry): entry is [string, ReferenceCounts] => entry !== null,
            ),
          ),
        );
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [LLMModelRefCounts, models]);
  return {
    providers,
    providersLoading,
    selectedId,
    setSelectedId,
    selectedProvider,
    models,
    modelsLoading,
    modelsError,
    providerRefCounts,
    modelRefCounts,
    refreshProviders,
    refreshModels,
  };
}
