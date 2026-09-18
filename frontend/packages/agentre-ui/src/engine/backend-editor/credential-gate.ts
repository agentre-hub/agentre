// 凭据相关操作（Hermes 登录/登出/列提供方、OpenClaw 存/清 token、测试连接）
// 在发出请求前先问一句「这台绑定设备现在能不能接」。三种否决在这里收敛成一个
// 判据，好让编辑器只在「未选择设备 / 已知但离线 / 不在账号内」这三种情形下
// 一律不发请求，并且三处调用点（Hermes 区块、OpenClaw 区块、测试按钮）读到的
// 是同一个结论——不是各自拿 deviceId/online 现算一遍。
import type { DeviceOption } from "../agent-backends-utils";

export type CredentialDeviceGate =
  | ""
  | "deviceRequired"
  | "deviceOffline"
  | "deviceUnknown";

export function credentialDeviceGate(args: {
  hasLocalDevice: boolean;
  deviceId: string;
  selectedDeviceValue: string;
  localSelectValue: string;
  deviceOptions: DeviceOption[];
}): CredentialDeviceGate {
  const {
    hasLocalDevice,
    deviceId,
    selectedDeviceValue,
    localSelectValue,
    deviceOptions,
  } = args;
  // 本机对桌面端永远是可达的：它不是「一台需要在线的设备」，而是这个进程自己。
  if (hasLocalDevice && selectedDeviceValue === localSelectValue) return "";
  // 没有本机可指代的宿主（控制台）里，空 deviceId 是一项没填的必填项。
  if (deviceId.trim() === "") return "deviceRequired";
  const option = deviceOptions.find(
    (candidate) => candidate.value === selectedDeviceValue,
  );
  if (!option) return "deviceUnknown";
  return option.online ? "" : "deviceOffline";
}

/**
 * 三条提示复用 agentre-server 既有的 `settings.errors.device*` 文案（控制台侧
 * 后续会改接这一份，见 spec 决策与 task 5），只是把「保存前」的语境放宽成
 * 「这个凭据操作前」。
 */
export function credentialGateMessage(
  gate: CredentialDeviceGate,
  t: (key: string) => string,
): string {
  switch (gate) {
    case "deviceRequired":
      return t("agentBackends.credential.deviceRequired");
    case "deviceOffline":
      return t("agentBackends.credential.deviceOffline");
    case "deviceUnknown":
      return t("agentBackends.credential.deviceUnknown");
    default:
      return "";
  }
}
