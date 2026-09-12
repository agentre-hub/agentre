import { useEffect, useMemo } from "react";
import { useTranslation } from "react-i18next";

import { useDeviceListStore } from "@/stores/device-list-store";

import {
  buildDeviceMentionItems,
  type DeviceMentionItem,
} from "./mention-items";
import { mergeDeviceSources } from "./use-remote-devices";

/**
 * `@` 菜单里那份设备清单(桌面端这一侧)。
 *
 * 取数走 device-list-store —— 所有已打开 tab 的输入框共享一次拉取(见该 store 的
 * 注释)。合并沿用设备面板那一份 `mergeDeviceSources`:同一台机器在面板和 `@` 菜单里
 * 必须是同一行,两处各写一套合并规则迟早会互相矛盾。
 *
 * 刷新只挂在 mount 与窗口获得焦点上,**不订** `remote.device.state`:那条推送来自
 * Wails runtime,订它就把 runtime 拉进了每个输入框的依赖链(输入框在本仓库里是
 * 纯组件,不碰 Wails)。设备面板仍然是实时的;这里的在线态是一枚装饰点 ——
 * 提及是一个上下文引用,不是派发决定,回到窗口时对一次就够了。
 */
export function useDeviceMentionItems(): DeviceMentionItem[] {
  const { t } = useTranslation();
  const selfFingerprint = useDeviceListStore((s) => s.selfFingerprint);
  const lanDevices = useDeviceListStore((s) => s.lanDevices);
  const accountDevices = useDeviceListStore((s) => s.accountDevices);
  const accountKnown = useDeviceListStore((s) => s.accountKnown);
  const reload = useDeviceListStore((s) => s.reload);
  const refresh = useDeviceListStore((s) => s.refresh);

  useEffect(() => {
    void reload();
  }, [reload]);

  // 配对 / 解除配对 / 上下线都发生在别处,回到窗口时重拉一次 —— 免得刚配好的机器
  // 在 `@` 菜单里一直不出现。
  useEffect(() => {
    const onFocus = () => {
      void refresh();
    };
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, [refresh]);

  const selfFallbackName = t("remoteDevices.thisDevice");
  return useMemo(
    () =>
      buildDeviceMentionItems({
        rows: mergeDeviceSources(lanDevices, {
          known: accountKnown,
          devices: accountDevices,
        }),
        accountDevices,
        selfFingerprint,
        selfFallbackName,
      }),
    [
      lanDevices,
      accountDevices,
      accountKnown,
      selfFingerprint,
      selfFallbackName,
    ],
  );
}
