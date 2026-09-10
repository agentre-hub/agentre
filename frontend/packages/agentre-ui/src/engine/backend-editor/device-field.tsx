// 「运行设备」下拉：本地项（宿主有本机可指代时才有）、可选设备行，以及一条只读的
// 兜底行 —— 后端钉的那台机器已经不在清单里时，至少让用户看见它钉的是谁。

import { useUiTranslation as useTranslation } from "../../i18n";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../../ui/select";
import type { DeviceOption } from "../agent-backends-utils";
import type { BackendType } from "../agent-backends-shared";

import { LOCAL_DEVICE_SELECT_VALUE } from "./use-backend-devices";

export function DeviceField({
  type,
  value,
  onSelect,
  hasLocalDevice,
  deviceOptions,
  selectedDeviceKnown,
  deviceId,
  revokedFallbackName,
}: {
  type: BackendType;
  value: string;
  onSelect: (selectValue: string) => void;
  hasLocalDevice: boolean;
  deviceOptions: DeviceOption[];
  selectedDeviceKnown: boolean;
  deviceId: string;
  revokedFallbackName: string;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-1.5 text-xs">
      <span className="font-medium">{t("agentBackends.fields.device")}</span>
      <Select
        value={value}
        onValueChange={onSelect}
        disabled={type === "builtin"}
      >
        <SelectTrigger aria-label={t("agentBackends.fields.device")}>
          <SelectValue placeholder={t("agentBackends.device.placeholder")} />
        </SelectTrigger>
        <SelectContent>
          {hasLocalDevice ? (
            <SelectItem value={LOCAL_DEVICE_SELECT_VALUE}>
              {t("agentBackends.device.local")}
            </SelectItem>
          ) : null}
          {deviceOptions.map((d) => (
            // 离线的机器照样能选：保存不依赖设备在线，只有测试连接 / 发现模型 /
            // 自动扫描依赖。禁用它等于让刚登记或暂时掉线的机器配不了后端。
            <SelectItem key={d.value} value={d.value}>
              📡 {d.name}
              {d.online ? "" : t("agentBackends.device.offlineSuffix")}
            </SelectItem>
          ))}
          {!selectedDeviceKnown && deviceId ? (
            <SelectItem value={deviceId} disabled>
              📡 {revokedFallbackName}
            </SelectItem>
          ) : null}
        </SelectContent>
      </Select>
      {type === "builtin" ? (
        <span className="text-2xs text-muted-foreground">
          {t("agentBackends.device.builtinLocalOnly")}
        </span>
      ) : null}
    </div>
  );
}
