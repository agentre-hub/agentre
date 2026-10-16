import { create } from "zustand";

import type { SettingsPageId } from "@/components/agentre/settings/pages";

/**
 * 设置对话框的开关与当前子页（规格 2026-10-15「设置对话框」）。
 *
 * 设置不再是 `/settings` 路由，而是浮在当前页面上的模态框：图标栏齿轮、⌘, / Ctrl+,、
 * 命令面板以及各处「去设置」深链都从这里打开它，所以放在 store 里而不是 React context，
 * 与 `command-palette-store` 同一口径。
 *
 * `page` 只活在内存里：不带子页的入口落在「本次运行里上次打开的子页」，首次为「外观」；
 * 关掉对话框不清它，重启应用回到「外观」——刻意不写 localStorage。
 */
type State = {
  open: boolean;
  page: SettingsPageId;
};

type Actions = {
  /** 打开设置；给了子页就切到它（已打开时也切），不给就停在上次的子页。 */
  openSettings: (page?: SettingsPageId) => void;
  closeSettings: () => void;
  /** ⌘, 的语义：关着就打开（落在上次的子页），开着就关。 */
  toggleSettings: () => void;
  /** 对话框内切换子页；记下来供下次不带子页的打开使用。 */
  setPage: (page: SettingsPageId) => void;
};

export const useSettingsDialogStore = create<State & Actions>((set, get) => ({
  open: false,
  page: "appearance",
  openSettings: (page) => set(page ? { open: true, page } : { open: true }),
  closeSettings: () => set({ open: false }),
  toggleSettings: () => set({ open: !get().open }),
  setPage: (page) => set({ page }),
}));

export function openSettings(page?: SettingsPageId): void {
  useSettingsDialogStore.getState().openSettings(page);
}

export function closeSettings(): void {
  useSettingsDialogStore.getState().closeSettings();
}

export function toggleSettings(): void {
  useSettingsDialogStore.getState().toggleSettings();
}
