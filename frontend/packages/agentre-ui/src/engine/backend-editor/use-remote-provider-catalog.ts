// 目标 daemon 上的 Provider/Model 目录：拉一次（换设备就重拉），转成 Picker 认得的
// 非敏感摘要，并读出该设备的 fixed-model 能力位。
import * as React from "react";

import type { PickerProvider } from "../model-target-picker";
import type { EngineSettingsBridge } from "../port-bridge";

import type { DeviceView, EditorState, ProviderSummary } from "./editor-types";

export type RemoteProviderCatalog = ReturnType<typeof useRemoteProviderCatalog>;

export function useRemoteProviderCatalog(args: {
  stateKind: EditorState["kind"];
  remoteDeviceID: number;
  devices: DeviceView[];
  listRemoteProviders: EngineSettingsBridge["RemoteDeviceListProviders"];
}) {
  const { stateKind, remoteDeviceID, devices, listRemoteProviders } = args;
  const [remoteProviders, setRemoteProviders] = React.useState<
    ProviderSummary[]
  >([]);
  const remoteProviderRequestRef = React.useRef(0);
  const refreshRemoteProviders = React.useCallback(async () => {
    const request = ++remoteProviderRequestRef.current;
    if (stateKind === "closed" || remoteDeviceID <= 0) {
      setRemoteProviders([]);
      return;
    }
    try {
      const rows = await listRemoteProviders(remoteDeviceID);
      if (remoteProviderRequestRef.current === request) {
        setRemoteProviders((rows ?? []) as ProviderSummary[]);
      }
    } catch {
      if (remoteProviderRequestRef.current === request) {
        setRemoteProviders([]);
      }
    }
  }, [stateKind, remoteDeviceID, listRemoteProviders]);
  React.useEffect(() => {
    void refreshRemoteProviders();
  }, [refreshRemoteProviders]);

  const remoteSupportsFixedModel = React.useMemo(() => {
    const dv = devices.find((d) => d.id === remoteDeviceID);
    return dv?.supportsLLMModelTarget ?? false;
  }, [devices, remoteDeviceID]);

  // 把 daemon 目录转成 PickerProvider[]（非敏感摘要）：供 Picker 判断哪些 desktop
  // 行在 daemon 上不存在 / 模型未同步，以及 fixed-model 是否被能力位允许。
  const remotePickerCatalog = React.useMemo<PickerProvider[]>(() => {
    if (remoteDeviceID <= 0) return [];
    return remoteProviders.map((p) => {
      const models = (p.models ?? []).map((m) => ({
        modelKey: m.key,
        modelId: m.modelId,
        name: m.name,
        enabled: m.enabled,
      }));
      const defaultModel =
        (p.defaultModelKey &&
          models.find((m) => m.modelKey === p.defaultModelKey)) ||
        null;
      return {
        providerKey: p.key ?? "",
        id: 0,
        name: p.name ?? p.key ?? "",
        type: p.type ?? "",
        enabled: true,
        defaultModel,
        models,
      };
    });
  }, [remoteDeviceID, remoteProviders]);

  return {
    refreshRemoteProviders,
    remoteSupportsFixedModel,
    remotePickerCatalog,
  };
}
