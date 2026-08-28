// 远端执行的草稿体检：保存前先确认目标 daemon 上真有这份供应商 / 模型，
// 缺什么就把结论交回编辑器决定是弹同步、还是直接挡住保存。
import type { PickerProvider } from "../model-target-picker";
import type { EngineSettingsBridge } from "../port-bridge";
import type { RouteTarget, Translate } from "../agent-backends-shared";
import { resolveExecutionDevice } from "../device-identity";

import { referencedProviderKeys, type BackendDraft } from "./draft";
import type { DeviceView, ProviderSummary } from "./editor-types";

export type RemoteDraftInspection = {
  missingProviderKeys: string[];
  targetIssue: "fixedUnsupported" | "syncNeeded" | null;
};

export async function inspectRemoteDraft(args: {
  draft: BackendDraft;
  localFingerprint: string;
  devices: DeviceView[];
  targetCatalog: PickerProvider[];
  listRemoteProviders: EngineSettingsBridge["RemoteDeviceListProviders"];
}): Promise<RemoteDraftInspection> {
  const { draft, localFingerprint, devices, targetCatalog } = args;
  const resolved = resolveExecutionDevice(
    draft.deviceId,
    localFingerprint,
    devices,
  );
  if (!resolved.remote) {
    return { missingProviderKeys: [], targetIssue: null };
  }

  const providerKeys = referencedProviderKeys(draft);
  if (providerKeys.length === 0) {
    return { missingProviderKeys: [], targetIssue: null };
  }
  const deviceID = resolved.pairedDeviceId;
  // 本机没有通往该设备的已配对行（典型是账号内另一台桌面端）：这台机器读不到它的
  // 目录，也同步不过去。此时既不能断言"供应商缺失"，也不能拿一个做不到的同步挡住
  // 保存 —— 未验证就是未验证，按无结论放行（门控仍在 Picker 侧禁用未验证目标）。
  if (deviceID <= 0) {
    return { missingProviderKeys: [], targetIssue: null };
  }

  const remoteRaw = (await args.listRemoteProviders(deviceID)) as
    | ProviderSummary[]
    | null
    | undefined;
  const remoteByKey = new Map(
    (remoteRaw ?? [])
      .filter((provider) => provider.key)
      .map((provider) => [provider.key ?? "", provider] as const),
  );
  const missingProviderKeys = providerKeys.filter(
    (key) => !remoteByKey.has(key),
  );
  if (missingProviderKeys.length > 0) {
    return { missingProviderKeys, targetIssue: null };
  }

  const targets: RouteTarget[] = [];
  if (draft.llmProviderKey) {
    targets.push({
      providerKey: draft.llmProviderKey,
      modelKey: draft.llmModelKey,
    });
  }
  if (draft.type === "claudecode") {
    targets.push(...Object.values(draft.modelRoutes));
  }
  const supportsFixedModel =
    devices.find((device) => device.id === deviceID)?.supportsLLMModelTarget ??
    false;
  for (const target of targets) {
    const providerKey = target.providerKey.trim();
    if (!providerKey) continue;
    const remoteProvider = remoteByKey.get(providerKey);
    if (!remoteProvider) continue;
    const modelKey = target.modelKey.trim();
    if (modelKey) {
      if (!supportsFixedModel) {
        return {
          missingProviderKeys: [],
          targetIssue: "fixedUnsupported",
        };
      }
      const remoteModel = (remoteProvider.models ?? []).find(
        (model) => model.key === modelKey && model.enabled,
      );
      if (!remoteModel) {
        return { missingProviderKeys: [], targetIssue: "syncNeeded" };
      }
      continue;
    }
    const localDefaultModelKey = targetCatalog.find(
      (provider) => provider.providerKey === providerKey,
    )?.defaultModel?.modelKey;
    if (localDefaultModelKey) {
      const remoteDefaultModel = (remoteProvider.models ?? []).find(
        (model) =>
          model.key === remoteProvider.defaultModelKey && model.enabled,
      );
      if (
        remoteProvider.defaultModelKey !== localDefaultModelKey ||
        !remoteDefaultModel
      ) {
        return { missingProviderKeys: [], targetIssue: "syncNeeded" };
      }
    }
  }
  return { missingProviderKeys: [], targetIssue: null };
}

export function remoteTargetIssueMessage(
  issue: RemoteDraftInspection["targetIssue"],
  t: Translate,
): string {
  return issue === "fixedUnsupported"
    ? t("modelTargetPicker.fixedModelUnsupported")
    : t("modelTargetPicker.remoteSyncNeeded");
}
