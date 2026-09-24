// cc-usage-store
//
// 单例 zustand store,索引 Claude Code OAuth usage state 按 deviceKey:
//   - "local"        → 桌面所在机器的 5h / 7d 配额
//   - "remote:<id>"  → 某台已配对 agentred 上的配额
//
// 数据源:后端 cc_usage_svc 在 wails event "cc_usage:update" 推送;
// hook use-cc-usage 订阅一次,把 payload 灌到 byDevice。
//
// 同值短路:byDevice Map 引用只在真有数据变动时更新,避免 60s tick
// 把数字 round 成同一个百分比却让所有订阅者集体 re-render。

import { create } from "zustand";

import type { cc_usage_svc } from "../../wailsjs/go/models";

export type UsageState = cc_usage_svc.UsageState;

type State = {
  byDevice: Map<string, UsageState>;
};

type Actions = {
  upsert: (deviceKey: string, state: UsageState) => void;
  remove: (deviceKey: string) => void;
  __reset: () => void;
};

// shallowSameState 判断两个 UsageState 是否在 HUD 显示维度上相同:
// reason / stale + data 的两个主窗口与各档模型周配额的百分比 / resets_at(都是 QuotaMeter 与其
// HoverCard 面板会渲染的量)。resets_at 是 ISO 字符串,直接 === 比较即可。
//
// 刻意不比较 fetchedAtMs:后端每次 probe 都写新的时间戳(cc_usage_svc/manager.go),
// 60s tick 下它必变。把它算进"是否同值"会让短路恒不成立,订阅者每分钟集体
// re-render 一次 —— 那正是这个函数要避免的事。
// 代价:数值没变时 store 里留的是上一次的 fetchedAtMs。目前前端没有任何
// "数据有多新"的展示消费它;将来若要展示,得改成比较后单独更新该字段。
function shallowSameState(a: UsageState, b: UsageState): boolean {
  if (a.reason !== b.reason) return false;
  if ((a.stale ?? false) !== (b.stale ?? false)) return false;
  const da = a.data;
  const db = b.data;
  if (!da && !db) return true;
  if (!da || !db) return false;
  return (
    da.fiveHourPercent === db.fiveHourPercent &&
    da.weeklyPercent === db.weeklyPercent &&
    (da.fiveHourResetsAt ?? null) === (db.fiveHourResetsAt ?? null) &&
    (da.weeklyResetsAt ?? null) === (db.weeklyResetsAt ?? null) &&
    sameModelWeekly(da.modelWeekly ?? [], db.modelWeekly ?? [])
  );
}

function sameModelWeekly(
  a: NonNullable<NonNullable<UsageState["data"]>["modelWeekly"]>,
  b: NonNullable<NonNullable<UsageState["data"]>["modelWeekly"]>,
): boolean {
  return (
    a.length === b.length &&
    a.every(
      (x, i) =>
        x.model === b[i].model &&
        x.percent === b[i].percent &&
        (x.resetsAt ?? null) === (b[i].resetsAt ?? null),
    )
  );
}

export const useCCUsageStore = create<State & Actions>((set) => ({
  byDevice: new Map(),

  upsert: (deviceKey, state) =>
    set((s) => {
      const prev = s.byDevice.get(deviceKey);
      if (prev && shallowSameState(prev, state)) return s;
      const byDevice = new Map(s.byDevice);
      byDevice.set(deviceKey, state);
      return { byDevice };
    }),

  remove: (deviceKey) =>
    set((s) => {
      if (!s.byDevice.has(deviceKey)) return s;
      const byDevice = new Map(s.byDevice);
      byDevice.delete(deviceKey);
      return { byDevice };
    }),

  __reset: () => set({ byDevice: new Map() }),
}));

// useCCUsageFor 给单个 deviceKey 返回当前 state,未拉过 → undefined。
export function useCCUsageFor(deviceKey: string): UsageState | undefined {
  return useCCUsageStore((s) => s.byDevice.get(deviceKey));
}
