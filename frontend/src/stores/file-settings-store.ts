import { create } from "zustand";

import { GetAppSetting, UpdateAppSettings } from "../../wailsjs/go/app/App";
import type { app_settings_svc } from "../../wailsjs/go/models";

/** 单击一个文件时的去向。allowlist 外的文件不参与这个选择（spec 决策 2）。 */
export type FileOpenAction = "preview" | "external";

export type FileSettings = {
  openAction: FileOpenAction;
};

export const DEFAULT_FILE_SETTINGS: FileSettings = {
  openAction: "preview",
};

const KEYS = {
  openAction: "files.open_action",
} as const;

// GetAppSetting 在 key 不存在时会 reject（后端 AppSettingNotFound），逐 key 兜底默认值。
async function readRaw(key: string): Promise<string | null> {
  try {
    const r = await GetAppSetting({ key });
    return r?.value ?? null;
  } catch {
    return null;
  }
}

// 只认得 "external" 这一个非默认取值：键没被设置过、或存进去的是别的东西时都按
// 默认的内置预览处理（spec 决策 3——升级不得静默改变任何人的单击语义）。
function parseOpenAction(raw: string | null): FileOpenAction {
  return raw === "external" ? "external" : DEFAULT_FILE_SETTINGS.openAction;
}

type State = {
  settings: FileSettings;
  load: () => Promise<void>;
  save: (patch: Partial<FileSettings>) => Promise<void>;
};

export const useFileSettingsStore = create<State>((set, get) => ({
  settings: { ...DEFAULT_FILE_SETTINGS },
  load: async () => {
    const openAction = await readRaw(KEYS.openAction);
    set({ settings: { openAction: parseOpenAction(openAction) } });
  },
  save: async (patch) => {
    const entries = Object.entries(patch).map(([k, v]) => ({
      key: KEYS[k as keyof FileSettings],
      value: String(v),
    }));
    if (entries.length === 0) return;
    await UpdateAppSettings({ entries } as app_settings_svc.UpdateRequest);
    set({ settings: { ...get().settings, ...patch } });
  },
}));
