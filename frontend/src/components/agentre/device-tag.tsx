import * as React from "react";
import { MapPin, Server, ServerOff } from "lucide-react";
import { useTranslation } from "react-i18next";

import { cn } from "@agentre-hub/agentre-ui";

type DeviceTagProps = React.ComponentProps<"span"> & {
  deviceId: string;
  deviceName: string;
  online: boolean;
  // lastSeen: 远端离线时显示的"上次在线"时长后缀,例如 "2h"。
  // 由调用方格式化好后传入(空串或 undefined 时不渲染)。仅在 online=false 时生效。
  lastSeen?: string;
};

// DeviceTag 渲染 agent / location 的设备归属 chip。
//   - deviceId === "" → 本地(蓝色 MapPin)
//   - deviceId !== "" + online → 远端在线(绿色 Server + name)
//   - deviceId !== "" + !online → 远端离线(灰色 ServerOff + name + "offline [lastSeen]")
//
// 共享给 MembersTab / Command Palette / 聊天头三处使用,保证视觉一致。
//
// 拆成单文件而不是放进 primitives.tsx,是为了让 device-tag.test.tsx
// 不被 primitives barrel 带进来的 shortcuts → use-project-tree →
// wailsjs 这条传递依赖链污染(否则测试需要额外 vi.mock 一堆运行时桩)。
export function DeviceTag({
  className,
  deviceId,
  deviceName,
  online,
  lastSeen,
  ...props
}: DeviceTagProps) {
  const { t } = useTranslation();

  if (!deviceId) {
    return (
      <span
        className={cn(
          "inline-flex items-center gap-1 rounded-sm bg-primary-soft px-1.5 py-0.5 text-2xs font-medium text-primary",
          className,
        )}
        {...props}
      >
        <MapPin className="size-3" aria-hidden="true" />
        {t("deviceTag.local")}
      </span>
    );
  }
  if (online) {
    return (
      <span
        className={cn(
          "inline-flex items-center gap-1 rounded-sm bg-status-running-bg px-1.5 py-0.5 text-2xs font-medium text-status-running",
          className,
        )}
        {...props}
      >
        <Server className="size-3" aria-hidden="true" />
        {deviceName}
      </span>
    );
  }
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-sm bg-muted px-1.5 py-0.5 text-2xs font-medium text-muted-foreground",
        className,
      )}
      {...props}
    >
      <ServerOff className="size-3" aria-hidden="true" />
      {t("deviceTag.offline", { name: deviceName })}
      {lastSeen ? ` ${lastSeen}` : ""}
    </span>
  );
}
