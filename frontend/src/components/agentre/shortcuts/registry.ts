import i18n from "@/i18n";
import type { ToolWindowId } from "@/stores/tool-window-layout-store";

import type { KeyChord, ShortcutDef } from "./types";

const navDef = (
  id: string,
  label: string,
  hint: string,
  key: string,
): ShortcutDef => ({
  id,
  label,
  hint,
  scope: "global",
  defaultBinding: { mod: "primary", key },
  rebindable: true,
});

const NAV_REGISTRY: ShortcutDef[] = [
  navDef(
    "nav.chat",
    i18n.t("shortcuts.registry.nav.chat.label"),
    i18n.t("shortcuts.registry.nav.chat.hint"),
    "E",
  ),
  navDef(
    "nav.projects",
    i18n.t("shortcuts.registry.nav.projects.label"),
    i18n.t("shortcuts.registry.nav.projects.hint"),
    "D",
  ),
  navDef(
    "nav.issues",
    i18n.t("shortcuts.registry.nav.issues.label"),
    i18n.t("shortcuts.registry.nav.issues.hint"),
    "B",
  ),
  navDef(
    "nav.org",
    i18n.t("shortcuts.registry.nav.org.label"),
    i18n.t("shortcuts.registry.nav.org.hint"),
    "G",
  ),
  navDef(
    "nav.hooks",
    i18n.t("shortcuts.registry.nav.hooks.label"),
    i18n.t("shortcuts.registry.nav.hooks.hint"),
    "Y",
  ),
  navDef(
    "nav.memory",
    i18n.t("memoryPage.shortcut.label"),
    i18n.t("memoryPage.shortcut.hint"),
    "U",
  ),
  navDef(
    "nav.settings",
    i18n.t("shortcuts.registry.nav.settings.label"),
    i18n.t("shortcuts.registry.nav.settings.hint"),
    ",",
  ),
];

// SESSION_CHIP_IDS —— 侧边栏会话快捷键 id。⌘1-9 优先由 chat.tab.N（TabsScope）
// 接管，没挂 TabsScope 时才落到这一档（shortcuts-provider.findIdForChord）。
// 这些 id 不进 localStorage：rebindable=false ⇒ 绑定永远等于默认值，
// 而 saveShortcutsState 只写「与默认不同」的那些。
export const SESSION_CHIP_IDS: string[] = Array.from(
  { length: 9 },
  (_, i) => `chat.session.${i + 1}`,
);

const SESSION_CHIP_REGISTRY: ShortcutDef[] = SESSION_CHIP_IDS.map((id, i) => ({
  id,
  label: i18n.t("shortcuts.registry.sessionChip.label", { index: i + 1 }),
  hint: i18n.t("shortcuts.registry.sessionChip.hint"),
  scope: "session" as const,
  defaultBinding: { mod: "primary", key: String(i + 1) },
  rebindable: false,
}));

// TAB_CHIP_IDS —— ⌘1..9 切换排序后的第 N 个 Tab。
// 与 SESSION_CHIP_IDS 使用相同的 ⌘1-9 chord，但 scope="tabs"，
// 通过 tabsScopeRef 而非 sessionScopeRef 派发，两套机制互斥（tab 优先）。
export const TAB_CHIP_IDS: string[] = Array.from(
  { length: 9 },
  (_, i) => `chat.tab.${i + 1}`,
);

export const TAB_CLOSE_ID = "chat.tab.close";

const TABS_CHIP_REGISTRY: ShortcutDef[] = [
  ...TAB_CHIP_IDS.map((id, i) => ({
    id,
    label: i18n.t("shortcuts.registry.tabChip.label", { index: i + 1 }),
    hint: i18n.t("shortcuts.registry.tabChip.hint"),
    scope: "tabs" as const,
    defaultBinding: { mod: "primary", key: String(i + 1) } as KeyChord,
    rebindable: false,
  })),
  {
    id: TAB_CLOSE_ID,
    label: i18n.t("shortcuts.registry.closeTab.label"),
    hint: i18n.t("shortcuts.registry.closeTab.hint"),
    scope: "tabs" as const,
    defaultBinding: { mod: "primary", key: "W" } as KeyChord,
    rebindable: false,
  },
];

export const PALETTE_OPEN_ID = "palette.open";
export const CMD_NEW_CHAT_ID = "cmd.new-chat";

// ⌘N 触发时往命令面板里预填的 query。CommandPalette parseMode 命中 ">" → command 模式。
// 只填前缀 + 空格（payload="" → 列表显示全部 agent，每行渲染 "New chat with <name>"）。
// 不要预填 "New chat with" —— 它会进 payload 当搜索词用，全 miss agent 名 → 列表空。
export const NEW_CHAT_INITIAL_QUERY = "> ";

const PALETTE_REGISTRY: ShortcutDef[] = [
  {
    id: PALETTE_OPEN_ID,
    label: i18n.t("shortcuts.registry.palette.label"),
    hint: i18n.t("shortcuts.registry.palette.hint"),
    scope: "global",
    defaultBinding: { mod: "primary", key: "P" },
    rebindable: true,
  },
  {
    id: CMD_NEW_CHAT_ID,
    label: i18n.t("shortcuts.registry.newChat.label"),
    hint: i18n.t("shortcuts.registry.newChat.hint"),
    scope: "global",
    defaultBinding: { mod: "primary", key: "N" },
    rebindable: true,
  },
];

// 工具窗快捷键（spec 决策 10、「快捷键」）：本轮不可改绑。⌃数字（Windows / Linux 为
// Alt+数字）切换工具窗，因为 ⌘1–9 已被标签页切换占用；终端两边都是 ⌃`。
// ⇧Esc / Esc / ⇧⌘' 只在焦点落在工具窗里时生效，由工具窗自己处理；其余由
// ShortcutsProvider 全局派发。
export const TOOL_WINDOW_TOGGLE_IDS: Readonly<Record<ToolWindowId, string>> = {
  outline: "toolWindow.toggle.outline",
  changes: "toolWindow.toggle.changes",
  directory: "toolWindow.toggle.directory",
  relations: "toolWindow.toggle.relations",
  sessionInfo: "toolWindow.toggle.sessionInfo",
  webPreview: "toolWindow.toggle.webPreview",
  terminal: "toolWindow.toggle.terminal",
};
export const TOOL_WINDOWS_HIDE_ALL_ID = "toolWindow.hideAll";
export const TOOL_WINDOW_HIDE_FOCUSED_ID = "toolWindow.hideFocused";
export const TOOL_WINDOW_RETURN_FOCUS_ID = "toolWindow.returnFocus";
export const TOOL_WINDOW_MAXIMIZE_ID = "toolWindow.maximize";
export const SESSION_LIST_TOGGLE_ID = "sessionList.toggle";

const fixedToolWindowDef = (
  id: string,
  label: string,
  hint: string,
  defaultBinding: KeyChord,
): ShortcutDef => ({
  id,
  label,
  hint,
  scope: "toolWindows",
  defaultBinding,
  rebindable: false,
});

const TOOL_WINDOW_REGISTRY: ShortcutDef[] = [
  fixedToolWindowDef(
    TOOL_WINDOW_TOGGLE_IDS.outline,
    i18n.t("shortcuts.registry.toolWindows.outline.label"),
    i18n.t("shortcuts.registry.toolWindows.outline.hint"),
    { mod: "secondary", key: "1" },
  ),
  fixedToolWindowDef(
    TOOL_WINDOW_TOGGLE_IDS.changes,
    i18n.t("shortcuts.registry.toolWindows.changes.label"),
    i18n.t("shortcuts.registry.toolWindows.changes.hint"),
    { mod: "secondary", key: "2" },
  ),
  fixedToolWindowDef(
    TOOL_WINDOW_TOGGLE_IDS.directory,
    i18n.t("shortcuts.registry.toolWindows.directory.label"),
    i18n.t("shortcuts.registry.toolWindows.directory.hint"),
    { mod: "secondary", key: "3" },
  ),
  fixedToolWindowDef(
    TOOL_WINDOW_TOGGLE_IDS.relations,
    i18n.t("shortcuts.registry.toolWindows.relations.label"),
    i18n.t("shortcuts.registry.toolWindows.relations.hint"),
    { mod: "secondary", key: "4" },
  ),
  fixedToolWindowDef(
    TOOL_WINDOW_TOGGLE_IDS.sessionInfo,
    i18n.t("shortcuts.registry.toolWindows.sessionInfo.label"),
    i18n.t("shortcuts.registry.toolWindows.sessionInfo.hint"),
    { mod: "secondary", key: "5" },
  ),
  fixedToolWindowDef(
    TOOL_WINDOW_TOGGLE_IDS.webPreview,
    i18n.t("shortcuts.registry.toolWindows.webPreview.label"),
    i18n.t("shortcuts.registry.toolWindows.webPreview.hint"),
    { mod: "secondary", key: "6" },
  ),
  fixedToolWindowDef(
    TOOL_WINDOW_TOGGLE_IDS.terminal,
    i18n.t("shortcuts.registry.toolWindows.terminal.label"),
    i18n.t("shortcuts.registry.toolWindows.terminal.hint"),
    { mod: "control", key: "`" },
  ),
  fixedToolWindowDef(
    TOOL_WINDOWS_HIDE_ALL_ID,
    i18n.t("shortcuts.registry.toolWindows.hideAll.label"),
    i18n.t("shortcuts.registry.toolWindows.hideAll.hint"),
    { mod: "primary-shift", key: "\\" },
  ),
  fixedToolWindowDef(
    TOOL_WINDOW_HIDE_FOCUSED_ID,
    i18n.t("shortcuts.registry.toolWindows.hideFocused.label"),
    i18n.t("shortcuts.registry.toolWindows.hideFocused.hint"),
    { mod: "shift", key: "Escape" },
  ),
  fixedToolWindowDef(
    TOOL_WINDOW_RETURN_FOCUS_ID,
    i18n.t("shortcuts.registry.toolWindows.returnFocus.label"),
    i18n.t("shortcuts.registry.toolWindows.returnFocus.hint"),
    { mod: "none", key: "Escape" },
  ),
  fixedToolWindowDef(
    TOOL_WINDOW_MAXIMIZE_ID,
    i18n.t("shortcuts.registry.toolWindows.maximize.label"),
    i18n.t("shortcuts.registry.toolWindows.maximize.hint"),
    { mod: "primary-shift", key: "'" },
  ),
  fixedToolWindowDef(
    SESSION_LIST_TOGGLE_ID,
    i18n.t("shortcuts.registry.toolWindows.sessionList.label"),
    i18n.t("shortcuts.registry.toolWindows.sessionList.hint"),
    { mod: "primary", key: "0" },
  ),
];

export const REGISTRY: ShortcutDef[] = [
  ...NAV_REGISTRY,
  ...PALETTE_REGISTRY,
  ...SESSION_CHIP_REGISTRY,
  ...TABS_CHIP_REGISTRY,
  ...TOOL_WINDOW_REGISTRY,
];

const REGISTRY_BY_ID: Map<string, ShortcutDef> = new Map(
  REGISTRY.map((def) => [def.id, def]),
);

export function getDef(id: string): ShortcutDef | undefined {
  return REGISTRY_BY_ID.get(id);
}

export function getDefaultBindings(): Map<string, KeyChord> {
  return new Map(REGISTRY.map((def) => [def.id, def.defaultBinding]));
}

// macOS 系统级 + Wails 窗口管理保留集（spec §3）。任何 chord 命中这里都不能绑定，
// 也不会被 HUD 拦截 —— ⌘C 等永远走系统。
// 注意：⌘W 已从保留集移除 —— 在 Tab 化之后由 chat.tab.close 接管，
// 提供浏览器式「关闭当前 Tab」行为；Wails 关窗原生仍可用 ⌘Q。
export const SYSTEM_RESERVED: KeyChord[] = [
  { mod: "primary", key: "C" },
  { mod: "primary", key: "V" },
  { mod: "primary", key: "X" },
  { mod: "primary", key: "A" },
  { mod: "primary", key: "Z" },
  { mod: "primary", key: "Q" },
  { mod: "primary", key: "M" },
  { mod: "primary", key: "H" },
];
