import type { PickerProvider } from "./model-target-picker";

const FLASH_DISPLAY_LIMIT = 80;

export type ResolvedModelTarget = {
  providerName: string;
  providerType: string;
  modelId: string;
  mode: "native" | "provider-default" | "fixed" | "invalid";
};

export function resolveModelTarget(
  providerKey: string,
  modelKey: string,
  catalog: PickerProvider[],
): ResolvedModelTarget {
  if (providerKey.trim() === "") {
    return {
      providerName: "",
      providerType: "",
      modelId: "",
      mode: "native",
    };
  }
  const provider = catalog.find((item) => item.providerKey === providerKey);
  if (!provider || !provider.enabled) {
    return {
      providerName: provider?.name ?? providerKey,
      providerType: provider?.type ?? "",
      modelId: modelKey,
      mode: "invalid",
    };
  }
  if (modelKey.trim() === "") {
    return provider.defaultModel?.enabled
      ? {
          providerName: provider.name,
          providerType: provider.type,
          modelId: provider.defaultModel.modelId,
          mode: "provider-default",
        }
      : {
          providerName: provider.name,
          providerType: provider.type,
          modelId: "",
          mode: "invalid",
        };
  }
  const model = provider.models.find((item) => item.modelKey === modelKey);
  return model?.enabled
    ? {
        providerName: provider.name,
        providerType: provider.type,
        modelId: model.modelId,
        mode: "fixed",
      }
    : {
        providerName: provider.name,
        providerType: provider.type,
        modelId: model?.modelId ?? modelKey,
        mode: "invalid",
      };
}

export function truncateFlashText(text: string): {
  display: string;
  full: string;
  truncated: boolean;
} {
  const full = text;
  const normalized = text.replace(/[\r\n\t]+/g, " ").replace(/ {2,}/g, " ");
  if (normalized.length <= FLASH_DISPLAY_LIMIT) {
    return { display: normalized, full, truncated: false };
  }
  return {
    display: normalized.slice(0, FLASH_DISPLAY_LIMIT) + "…",
    full,
    truncated: true,
  };
}
