// frontend/src/stores/device-list-store.ts
//
// device-list-store 是「这台桌面端看得见的机器」这份只读快照的共享数据源:本机指纹、
// LAN 配对行、账号设备清单。ChatComposer 的 @-mention 菜单消费它 —— chat-panel-host
// 把所有已打开 chat tab 同时挂载(CSS 隐藏而非卸载),每个 tab 各有一个 ChatComposer,
// hook 内各自 useState+useEffect 的写法会让 N 个 tab 打 N 遍 IPC。这是
// project-list-store 已经踩过的那一脚,这里沿用同一个解法。
//
// reload 并发去重:同一时刻只跑一个 in-flight;后续 reload 复用同一个 promise。
//
// 设置页的 useRemoteDevices 不走这里:它还要 add/remove/rename/TLS 这些写操作,
// 每次写完都要立刻重拉自己那一份。两边共用的是**合并算法**(mergeDeviceSources),
// 不是取数的时机。

import { create } from "zustand";

import {
  RemoteDeviceFingerprint,
  RemoteDeviceList,
  ServerListDevices,
} from "../../wailsjs/go/app/App";
import type { remote_device_svc, server_svc } from "../../wailsjs/go/models";

type State = {
  /** 本机指纹。空 = 还没拿到,或这台机器还没 provision 过。 */
  selfFingerprint: string;
  lanDevices: remote_device_svc.DeviceView[];
  accountDevices: server_svc.Device[];
  /** 账号清单是否拉到过。未登录 / 服务端离线时为 false —— 缺依据时不下结论。 */
  accountKnown: boolean;
  /** 拉到过一次没有。区分「确实一台都没有」与「还没拉」。 */
  loaded: boolean;
};

type Actions = {
  /** 首次装载:已经拉过就不再打 IPC。每个消费方 mount 时调。 */
  reload: () => Promise<void>;
  /** 明确要一份新的(窗口重新获得焦点)。并发调用共用同一次请求。 */
  refresh: () => Promise<void>;
  // 测试隔离用, 生产代码不该调。
  __reset: () => void;
};

let inflight: Promise<void> | null = null;

const EMPTY: State = {
  selfFingerprint: "",
  lanDevices: [],
  accountDevices: [],
  accountKnown: false,
  loaded: false,
};

export const useDeviceListStore = create<State & Actions>((set, get) => ({
  ...EMPTY,
  reload: () => {
    if (get().loaded) return Promise.resolve();
    return get().refresh();
  },
  refresh: () => {
    if (inflight) return inflight;
    inflight = (async () => {
      try {
        // 三条各自失败各自算:拿不到账号清单不该连配对行一起丢掉,反过来也一样。
        const [fingerprint, lan, account] = await Promise.all([
          RemoteDeviceFingerprint().catch(() => ""),
          RemoteDeviceList().catch(() => []),
          ServerListDevices().then(
            (devices) => ({ known: true, devices: devices ?? [] }),
            () => ({ known: false, devices: [] as server_svc.Device[] }),
          ),
        ]);
        set({
          selfFingerprint: fingerprint ?? "",
          lanDevices: lan ?? [],
          accountDevices: account.devices,
          accountKnown: account.known,
          loaded: true,
        });
      } finally {
        inflight = null;
      }
    })();
    return inflight;
  },
  __reset: () => {
    inflight = null;
    set({ ...EMPTY });
  },
}));
