import type { server_svc } from "../../../../wailsjs/go/models";
import type { DeviceRowModel } from "./use-remote-devices";

/** 交给 `@` 菜单的一台机器。形状与 agentre-ui 的 `buildMentionSources` 第三个入参一致。 */
export type DeviceMentionItem = { fp: string; name: string; online: boolean };

/** 一行设备的指纹:LAN 配对行与账号行各有一份,同一台机器上是同一个值。 */
function rowFingerprint(row: DeviceRowModel): string {
  return row.lan?.daemonFingerprint || row.account?.fingerprint || "";
}

/**
 * 把设备面板的那份「一行一台机器」投影成 `@` 菜单要的清单。
 *
 * 三条规矩:
 *
 *  1. **本机排最前** —— 与派发里「优先我面前这台」同一个落点(`ResolveExecTargetOrder`)。
 *     名字优先用账号清单里这台机器的名字;没登录时账号清单给不出名字,落回宿主给的
 *     文案。本机按定义可达,不去问在线态。
 *  2. **指纹是唯一身份**,没有指纹的机器不列 —— 设备提及在正文里只存指纹
 *     (见 agentre-ui 的 `MentionRef.fp`),写不出指纹就写不出一个指得回它的引用。
 *  3. **另一台桌面端也是一台机器**。`mergeDeviceSources` 把 kind=desktop 的账号行
 *     排除在外(设备面板给它们单独的行),但「@ 哪台机器」这个问题里它们不该消失。
 */
export function buildDeviceMentionItems({
  rows,
  accountDevices,
  selfFingerprint,
  selfFallbackName,
}: {
  rows: DeviceRowModel[];
  accountDevices: server_svc.Device[];
  selfFingerprint: string;
  selfFallbackName: string;
}): DeviceMentionItem[] {
  const out: DeviceMentionItem[] = [];
  const seen = new Set<string>();

  const add = (item: DeviceMentionItem) => {
    if (!item.fp || seen.has(item.fp)) return;
    seen.add(item.fp);
    out.push(item);
  };

  if (selfFingerprint) {
    const self = accountDevices.find((d) => d.isThisDevice);
    add({
      fp: selfFingerprint,
      name: self?.name || selfFallbackName,
      online: true,
    });
  }

  for (const row of rows) {
    add({ fp: rowFingerprint(row), name: row.name, online: row.online });
  }

  for (const device of accountDevices) {
    if (device.kind !== "desktop") continue;
    add({
      fp: device.fingerprint,
      name: device.name || device.fingerprint,
      online: device.online,
    });
  }

  return out;
}
