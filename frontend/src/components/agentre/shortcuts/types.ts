export type ShortcutScope = "global" | "session" | "tabs" | "toolWindows";

/**
 * primary = macOS ⌘ / 其它 Ctrl。下面几种只给固定（不可改绑）的工具窗快捷键用：
 * secondary = macOS ⌃ / 其它 Alt（⌃1–6，⌘1–9 已被标签页占用）；control = 两边都是
 * Ctrl（⌃`）；shift / none = 只有 ⇧ 或不带修饰键（⇧Esc / Esc）。
 */
export type ModifierKind =
  | "primary"
  | "primary-shift"
  | "secondary"
  | "control"
  | "shift"
  | "none";

export type KeyChord = {
  mod: ModifierKind;
  key: string;
};

export type ShortcutDef = {
  id: string;
  label: string;
  hint: string;
  scope: ShortcutScope;
  defaultBinding: KeyChord;
  rebindable: boolean;
};

export type AttentionEntry = {
  agentId: number;
  sessionId: number;
};

// Bridge object injected by <PaletteScopeBridge/>. ShortcutsProvider keeps it
// behind a ref so it can fire palette.open / cmd.new-chat from its single window
// keydown handler without taking a direct dependency on the palette store.
export type PaletteScope = {
  toggle: () => void;
  openWith: (query: string) => void;
};
