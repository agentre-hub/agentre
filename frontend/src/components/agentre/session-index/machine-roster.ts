// machine-roster.ts —— 「按机器」轴的机器名单（规格 2026-08-21）。
//
// 名单是**本机 + 每台配对的 daemon**。本机不是一个特例分支，它就是名单里的第一台：
// 会话表上的 exec_device_id 用 0 表示它（chat_entity.Session:86），机器轴那一组
// 发出去的 deviceId 就是 0。把它当成「没有机器」会让绝大多数会话无处可归。
//
// 空组照摆是有意的（决策 10）：刚配好的一台 daemon 上一条会话都没有，它也得在索引里
// 看得见，否则用户无从确认配对生效了。所以名单来自设备清单，而不是从会话里反推。
import * as React from "react";

import { RemoteDeviceList } from "../../../../wailsjs/go/app/App";

/** 本机在 exec_device_id 上的编号。与 chat_entity.Session 的约定同源。 */
export const LOCAL_DEVICE_ID = 0;

/** 名单里的一台机器。形状与共享包的 `MachineInfo` 一致。 */
export type MachineRosterEntry = {
  deviceId: number;
  name: string;
  online: boolean;
};

/** 设备清单里本名单用得上的那几列（`remote_device_svc.DeviceView` 结构上满足它）。 */
type PairedDevice = { id: number; name: string; online: boolean };

/**
 * 本机 + 配对的 daemon，按索引里要摆的顺序：本机最前，其余在线优先、同段内按名字。
 *
 * 顺序在这里定而不是交给共享投影：桌面端的组骨架归宿主（见 index-projection.ts），
 * 投影只负责把行分进组里、不重排组。
 */
export function buildMachineRoster(
  devices: readonly PairedDevice[] | null | undefined,
  localName: string,
): MachineRosterEntry[] {
  const paired = [...(devices ?? [])]
    .map((d) => ({ deviceId: d.id, name: d.name, online: d.online }))
    .sort(
      (a, b) =>
        Number(b.online) - Number(a.online) || a.name.localeCompare(b.name),
    );

  return [
    { deviceId: LOCAL_DEVICE_ID, name: localName, online: true },
    ...paired,
  ];
}

/**
 * 机器名单。`enabled` 为假时不发 RPC —— 别的轴不需要这份清单。
 *
 * 只读一次：配对与上下线由设备面板那条路（`use-remote-devices`）管，索引这边不订阅
 * 事件流。名单变了，侧栏跟着 reloadSidebarSources 那一轮重挂即可。
 */
export function useMachineRoster(
  enabled: boolean,
  localName: string,
): MachineRosterEntry[] {
  const [devices, setDevices] = React.useState<PairedDevice[]>([]);

  React.useEffect(() => {
    if (!enabled) return;
    let alive = true;
    void RemoteDeviceList()
      .then((list) => {
        if (alive) setDevices(list ?? []);
      })
      // 拉不到清单时退化成「只有本机」，而不是让整根轴打不开：本机那一组的会话
      // 与配对无关，照样列得出来。
      .catch(() => {
        if (alive) setDevices([]);
      });
    return () => {
      alive = false;
    };
  }, [enabled]);

  return React.useMemo(
    () => buildMachineRoster(devices, localName),
    [devices, localName],
  );
}
