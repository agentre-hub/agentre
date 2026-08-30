// use-cc-usage
//
// Hook 给 ChatComposer 订阅"当前 chat 对应 device 的 Claude Code 5h/7d 配额"。
//
//   const usage = useCCUsage(deviceKey)
//
// 内部:
//   - 第一次任何组件调用时,挂一个全局 wails EventsOn("cc_usage:update")
//     把推送灌进 cc-usage-store(后续 mount 不重挂)。
//   - 第一次见到一个 deviceKey 时,主动 GetCCUsage(key) 拉一次缓存灌进 store
//     (避免等下一次后端 5 分钟 tick)。
//
// 全局订阅生命周期跟 app 进程一致 —— 卸载所有 ChatComposer 也不解订(下次
// chat tab 打开还要用)。Wails event runtime 退出时自动清理。

import { useEffect } from "react";

import { GetCCUsage } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import {
  useCCUsageFor,
  useCCUsageStore,
  type UsageState,
} from "../stores/cc-usage-store";

type Payload = {
  deviceKey: string;
  state: UsageState;
};

let subscribed = false;
const initializedKeys = new Set<string>();

function ensureSubscription() {
  if (subscribed) return;
  subscribed = true;
  EventsOn("cc_usage:update", (raw: unknown) => {
    const p = raw as Payload | null;
    if (p?.deviceKey && p.state) {
      useCCUsageStore.getState().upsert(p.deviceKey, p.state);
    }
  });
}

export function useCCUsage(deviceKey: string): UsageState | undefined {
  useEffect(() => {
    ensureSubscription();
  }, []);

  useEffect(() => {
    if (!deviceKey) return;
    if (initializedKeys.has(deviceKey)) return;
    initializedKeys.add(deviceKey);
    void GetCCUsage(deviceKey)
      .then((s) => {
        // reason="" 表示"还没首探过":不写入,等后端 5s 后第一次 tick 推送。
        if (s && s.reason) {
          useCCUsageStore.getState().upsert(deviceKey, s);
        }
      })
      .catch(() => {
        // wails RPC 失败(罕见):放任,等下一次后端推送。
      });
  }, [deviceKey]);

  return useCCUsageFor(deviceKey);
}
