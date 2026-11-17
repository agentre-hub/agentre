import { create } from "zustand";

import { GetAppSetting, UpdateAppSettings } from "../../wailsjs/go/app/App";
import type { app_settings_svc } from "../../wailsjs/go/models";

/** 标签栏的本机偏好（spec 2026-11-17-group-new-tabs-by-project）。 */
export type TabSettings = {
  /** 新建对话的标签页插到同一顶层项目族最后一个标签页右侧。 */
  groupNewByProject: boolean;
};

// 默认关闭：升级后不得悄悄改变任何人的标签顺序（spec 决策 7）。
export const DEFAULT_TAB_SETTINGS: TabSettings = {
  groupNewByProject: false,
};

const KEYS = {
  groupNewByProject: "tabs.group_new_by_project",
} as const;

// GetAppSetting 在 key 不存在时会 reject（后端 AppSettingNotFound）；读不到一律按缺省。
async function readRaw(key: string): Promise<string | null> {
  try {
    const r = await GetAppSetting({ key });
    return r?.value ?? null;
  } catch {
    return null;
  }
}

type State = {
  settings: TabSettings;
  load: () => Promise<void>;
  /** 写库成功后才更新状态；失败时原样抛出，状态保持原值。 */
  save: (patch: Partial<TabSettings>) => Promise<void>;
};

// 每次写库成功加一。load 发出读取后若期间有写入落定，读回的是写之前的值，丢弃，
// 否则会把刚存上的开关盖回旧值（启动时与外观页挂载时都会 load）。
let saved = 0;

export const useTabSettingsStore = create<State>((set, get) => ({
  settings: { ...DEFAULT_TAB_SETTINGS },
  load: async () => {
    const before = saved;
    const raw = await readRaw(KEYS.groupNewByProject);
    if (saved !== before) return;
    set({ settings: { groupNewByProject: raw === "true" } });
  },
  save: async (patch) => {
    const entries = Object.entries(patch).map(([k, v]) => ({
      key: KEYS[k as keyof TabSettings],
      value: String(v),
    }));
    if (entries.length === 0) return;
    await UpdateAppSettings({ entries } as app_settings_svc.UpdateRequest);
    saved++;
    set({ settings: { ...get().settings, ...patch } });
  },
}));
