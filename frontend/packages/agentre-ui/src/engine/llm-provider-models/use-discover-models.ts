/**
 * 「发现模型」对话框的状态：拉取远端清单、按 vendor 分组、勾选、导入。
 *
 * 拉取带请求序号（`requestRef`）：换供应商或连点重试时，先回来的旧结果会盖掉
 * 新结果——那正是「明明重试过了，列表还是上一家的」的由来。
 *
 * `PreviewLLMModels` / `ImportLLMModels` / `t` 由调用方传进来：它们各自也是 hook
 * 的产物，在这里再取一次会改变宿主组件的 hook 数量与顺序。
 */
import * as React from "react";

import { useUiTranslation as useTranslation } from "../../i18n";
import { llm_provider_svc } from "../port-bridge";
import { useEngineSettingsBridge } from "../port-bridge";
import { type Model, type ModelInfo, type Provider, errMessage } from "./index";
import { type VendorGroup, vendorLabel } from "./discover-failure";

type EngineBridge = ReturnType<typeof useEngineSettingsBridge>;
type TranslateFn = ReturnType<typeof useTranslation>["t"];

export interface UseDiscoverModelsArgs {
  provider: Provider | null;
  existingModels: Model[];
  onClose: () => void;
  onImported: (resp: llm_provider_svc.ImportModelsResponse) => void;
  PreviewLLMModels: EngineBridge["PreviewLLMModels"];
  ImportLLMModels: EngineBridge["ImportLLMModels"];
  t: TranslateFn;
}

export function useDiscoverModels({
  provider,
  existingModels,
  onClose,
  onImported,
  PreviewLLMModels,
  ImportLLMModels,
  t,
}: UseDiscoverModelsArgs) {
  const open = provider !== null;
  const [items, setItems] = React.useState<ModelInfo[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [fetchError, setFetchError] = React.useState<string | null>(null);
  const [importError, setImportError] = React.useState<string | null>(null);
  const [showRaw, setShowRaw] = React.useState(false);
  const [selected, setSelected] = React.useState<Set<string>>(new Set());
  const [search, setSearch] = React.useState("");
  const [importing, setImporting] = React.useState(false);
  const requestRef = React.useRef(0);

  const existingIds = React.useMemo(
    () => new Set(existingModels.map((m) => m.modelId)),
    [existingModels],
  );

  const fetchPreview = React.useCallback(async () => {
    if (!provider) return;
    const requestId = ++requestRef.current;
    setLoading(true);
    setFetchError(null);
    setImportError(null);
    setShowRaw(false);
    try {
      const resp = await PreviewLLMModels(
        new llm_provider_svc.PreviewModelsRequest({
          id: provider.id,
          type: provider.type,
          apiKey: "",
          baseUrl: "",
        }),
      );
      if (requestId !== requestRef.current) return;
      const list = resp.items ?? [];
      setItems(list);
      setSelected(
        new Set(list.filter((m) => !existingIds.has(m.id)).map((m) => m.id)),
      );
    } catch (err) {
      if (requestId === requestRef.current) setFetchError(errMessage(err));
    } finally {
      if (requestId === requestRef.current) setLoading(false);
    }
  }, [PreviewLLMModels, existingIds, provider]);

  React.useEffect(() => {
    if (open) void fetchPreview();
  }, [open, fetchPreview]);

  const newCount = items.filter((m) => !existingIds.has(m.id)).length;
  const existingCount = items.length - newCount;
  const selectedCount = [...selected].filter(
    (id) => !existingIds.has(id),
  ).length;

  const trimmed = search.trim().toLowerCase();
  const visible = trimmed
    ? items.filter(
        (m) =>
          m.id.toLowerCase().includes(trimmed) ||
          m.vendor.toLowerCase().includes(trimmed),
      )
    : items;

  const groups: VendorGroup[] = React.useMemo(() => {
    const map = new Map<string, ModelInfo[]>();
    for (const m of visible) {
      const key = m.vendor?.trim().toLowerCase() || "__unknown__";
      if (!map.has(key)) map.set(key, []);
      map.get(key)!.push(m);
    }
    return [...map.entries()]
      .map(([key, groupItems]) => ({
        key,
        label:
          key === "__unknown__"
            ? t("llmProviders.discover.otherVendor")
            : vendorLabel(key),
        items: groupItems,
      }))
      .sort((a, b) => {
        if (a.key === "__unknown__") return 1;
        if (b.key === "__unknown__") return -1;
        return a.label.localeCompare(b.label);
      });
  }, [visible, t]);

  const toggle = React.useCallback((id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }, []);

  const toggleAllNew = React.useCallback(
    (checked: boolean) => {
      setSelected((prev) => {
        const next = new Set(prev);
        for (const m of items) {
          if (existingIds.has(m.id)) continue;
          if (checked) {
            next.add(m.id);
          } else {
            next.delete(m.id);
          }
        }
        return next;
      });
    },
    [existingIds, items],
  );

  const allNewChecked =
    newCount > 0 &&
    items
      .filter((m) => !existingIds.has(m.id))
      .every((m) => selected.has(m.id));

  const importModels = React.useCallback(async () => {
    if (!provider || selectedCount === 0) return;
    setImporting(true);
    setImportError(null);
    try {
      const resp = await ImportLLMModels(
        new llm_provider_svc.ImportModelsRequest({
          providerId: provider.id,
          models: items
            .filter((m) => !existingIds.has(m.id) && selected.has(m.id))
            .map(
              (m) =>
                new llm_provider_svc.ModelInput({
                  modelId: m.id,
                  name: "",
                  contextWindow: m.contextWindow,
                  maxOutput: m.maxOutput,
                }),
            ),
        }),
      );
      onImported(resp);
      onClose();
    } catch (err) {
      setImportError(errMessage(err));
    } finally {
      setImporting(false);
    }
  }, [
    ImportLLMModels,
    existingIds,
    items,
    onClose,
    onImported,
    provider,
    selected,
    selectedCount,
  ]);

  return {
    open,
    items,
    loading,
    fetchError,
    importError,
    showRaw,
    setShowRaw,
    selected,
    search,
    setSearch,
    importing,
    existingIds,
    fetchPreview,
    newCount,
    existingCount,
    selectedCount,
    groups,
    toggle,
    toggleAllNew,
    allNewChecked,
    importModels,
  };
}
