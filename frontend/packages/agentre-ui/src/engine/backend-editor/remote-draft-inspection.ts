// 远端执行的草稿体检：保存前先确认目标 daemon 上真有这份供应商 / 模型，缺了就挡住保存。
// 不再有「同步到设备」：账号供应商经快照到 agentred,key 只经租约在内存里拿。
import type { PickerProvider } from "../model-target-picker";
import type { EngineSettingsBridge } from "../port-bridge";
import type { RouteTarget, Translate } from "../agent-backends-shared";
import { resolveExecutionDevice } from "../device-identity";
import type { RuntimeDeviceView } from "../ports";

import { referencedProviderKeys, type BackendDraft } from "./draft";
import type { ProviderSummary } from "./editor-types";

export type RemoteDraftInspection = {
  targetIssue: "fixedUnsupported" | "syncNeeded" | null;
};

export async function inspectRemoteDraft(args: {
  draft: BackendDraft;
  localFingerprint: string;
  devices: RuntimeDeviceView[];
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
    return { targetIssue: null };
  }

  const providerKeys = referencedProviderKeys(draft);
  if (providerKeys.length === 0) {
    return { targetIssue: null };
  }
  const deviceID = resolved.pairedDeviceId;
  // 本机没有通往该设备的已配对行（典型是账号内另一台桌面端）：这台机器读不到它的
  // 目录。此时不能断言"供应商缺失" —— 未验证就是未验证，按无结论放行（门控仍在
  // Picker 侧禁用未验证目标）。
  if (deviceID <= 0) {
    return { targetIssue: null };
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
  if (providerKeys.some((key) => !remoteByKey.has(key))) {
    return { targetIssue: "syncNeeded" };
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
        return { targetIssue: "fixedUnsupported" };
      }
      const remoteModel = (remoteProvider.models ?? []).find(
        (model) => model.key === modelKey && model.enabled,
      );
      if (!remoteModel) {
        return { targetIssue: "syncNeeded" };
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
        return { targetIssue: "syncNeeded" };
      }
    }
  }
  return { targetIssue: null };
}

export function remoteTargetIssueMessage(
  issue: RemoteDraftInspection["targetIssue"],
  t: Translate,
): string {
  return issue === "fixedUnsupported"
    ? t("modelTargetPicker.fixedModelUnsupported")
    : t("modelTargetPicker.remoteSyncNeeded");
}
