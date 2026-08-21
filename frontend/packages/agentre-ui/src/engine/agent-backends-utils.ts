import type { PickerProvider } from "./model-target-picker";
import type { AccountDeviceView } from "./ports";

const FLASH_DISPLAY_LIMIT = 80;

// 只有这两种设备跑得动 agent；浏览器与手机不是执行端，列出来等于给一个选了也跑不了
// 的选项。宿主如实报 Kind，筛选留在包里，两端才是同一条口径。
const EXECUTABLE_DEVICE_KINDS = new Set(["desktop", "agentred"]);

/** 运行设备下拉里的一项：取值是设备的 canonical fingerprint。 */
export type DeviceOption = {
  value: string;
  name: string;
  online: boolean;
};

export function accountDeviceOptions(
  devices: AccountDeviceView[],
): DeviceOption[] {
  return devices
    .filter(
      (device) =>
        device.Fingerprint !== "" &&
        EXECUTABLE_DEVICE_KINDS.has(device.Kind ?? ""),
    )
    .map((device) => ({
      value: device.Fingerprint,
      name: device.Name || device.Fingerprint,
      online: device.Online === true,
    }));
}

/**
 * 一个后端「在哪跑」的四种如实说法。
 *
 * 空 deviceId 只在宿主有本机概念时才是「本机」：浏览器不是执行端，那里的空值是
 * 一条没填的必填项，说成「本机」是凭空指认一台不存在的机器。指纹在但名字解析不出
 * 来，则是那台机器已从账号撤销——保留这一行，让用户能改指到别的机器。
 */
export type BackendDeviceLocation =
  | "local"
  | "named"
  | "revoked"
  | "unspecified";

export function backendDeviceLocation(
  deviceId: string,
  deviceName: string,
  hasLocalDevice: boolean,
): BackendDeviceLocation {
  if (deviceId.trim() === "") return hasLocalDevice ? "local" : "unspecified";
  return deviceName.trim() === "" ? "revoked" : "named";
}

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
