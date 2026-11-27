import type { DesktopPlatform } from "../chrome";

import type { KeyChord, ModifierKind } from "./types";

const ACCEPTABLE_KEY_RE = /^[A-Z0-9,]$/;

export function isPrimaryModifier(
  event: Pick<KeyboardEvent, "metaKey" | "ctrlKey">,
  platform: DesktopPlatform,
): boolean {
  return platform === "darwin" ? event.metaKey : event.ctrlKey;
}

export function formatPrimaryModifier(platform: DesktopPlatform): string {
  return platform === "darwin" ? "⌘" : "Ctrl";
}

function isOtherModifier(
  event: KeyboardEvent,
  platform: DesktopPlatform,
): boolean {
  if (event.altKey) return true;
  // Forbid mixing ctrl+meta — we only accept the platform's primary key.
  return platform === "darwin" ? event.ctrlKey : event.metaKey;
}

export function isPrimaryModifierKey(
  event: KeyboardEvent,
  platform: DesktopPlatform,
): boolean {
  if (event.altKey) return false;
  if (platform === "darwin") {
    return event.key === "Meta" && event.metaKey && !event.ctrlKey;
  }
  return event.key === "Control" && event.ctrlKey && !event.metaKey;
}

export function isPrimaryShortcut(
  event: KeyboardEvent,
  platform: DesktopPlatform,
): boolean {
  return (
    isPrimaryModifier(event, platform) && !isOtherModifier(event, platform)
  );
}

/** 物理键 → 可绑定按键：⌥ 会改写 event.key（macOS 上 ⌥B 是「∫」），只能看 code。 */
function keyFromCode(code: string): string | null {
  const m = /^(?:Key([A-Z])|Digit([0-9]))$/.exec(code);
  if (m) return m[1] ?? m[2] ?? null;
  return code === "Comma" ? "," : null;
}

/**
 * 录制 / 查表用的和弦：主修饰键 + 可选的 ⇧ 或 ⌥（其它平台为 Alt），⇧⌥ 同按不收。
 * 不带 ⌥ 时按 event.key 取键（沿用至今的行为）；带 ⌥ 时按物理键取。
 */
export function chordFromEvent(
  event: Pick<
    KeyboardEvent,
    "code" | "key" | "metaKey" | "ctrlKey" | "altKey" | "shiftKey"
  >,
  platform: DesktopPlatform,
): KeyChord | null {
  if (!isPrimaryModifier(event, platform)) return null;
  // Forbid mixing ctrl+meta — we only accept the platform's primary key.
  if (platform === "darwin" ? event.ctrlKey : event.metaKey) return null;
  if (event.altKey && event.shiftKey) return null;

  const raw = event.key;
  if (raw === "Meta" || raw === "Control" || raw === "Shift" || raw === "Alt") {
    return null;
  }

  const key = event.altKey
    ? keyFromCode(event.code)
    : raw.length === 1
      ? raw.toUpperCase()
      : raw;
  if (key === null || !ACCEPTABLE_KEY_RE.test(key)) return null;

  const mod: ModifierKind = event.altKey
    ? "primary-alt"
    : event.shiftKey
      ? "primary-shift"
      : "primary";
  return { mod, key };
}

export function formatChord(
  chord: KeyChord,
  platform: DesktopPlatform,
): string {
  const isMac = platform === "darwin";
  const key = chord.key === "Escape" ? "Esc" : chord.key;
  switch (chord.mod) {
    case "secondary":
      return isMac ? `⌃${key}` : `Alt+${key}`;
    case "control":
      return isMac ? `⌃${key}` : `Ctrl+${key}`;
    case "shift":
      return isMac ? `⇧${key}` : `Shift+${key}`;
    case "none":
      return key;
    default:
      break;
  }
  if (isMac) {
    if (chord.mod === "primary-alt") return `⌥⌘${key}`;
    const shift = chord.mod === "primary-shift" ? "⇧" : "";
    return `⌘${shift}${key}`;
  }
  if (chord.mod === "primary-alt") return `Ctrl+Alt+${key}`;
  const shift = chord.mod === "primary-shift" ? "Shift+" : "";
  // Keep the comma literal so "Ctrl+," reads as a key, not a separator.
  return `Ctrl+${shift}${key}`;
}

/** 固定快捷键的按键 → KeyboardEvent.code：按物理键比，不受 ⇧ / 输入法改写 key 的影响。 */
const FIXED_KEY_CODES: Record<string, string> = {
  "`": "Backquote",
  "\\": "Backslash",
  "'": "Quote",
  Escape: "Escape",
};

function codeForKey(key: string): string {
  if (/^[0-9]$/.test(key)) return `Digit${key}`;
  return FIXED_KEY_CODES[key] ?? key;
}

/**
 * 按键是否正好是这个和弦：修饰键必须完全一致（多按一个也不算），按键按物理键
 * （event.code）比。给固定、不可改绑的工具窗快捷键用。
 */
export function matchesChord(
  event: Pick<
    KeyboardEvent,
    "code" | "key" | "metaKey" | "ctrlKey" | "altKey" | "shiftKey"
  >,
  chord: KeyChord,
  platform: DesktopPlatform,
): boolean {
  const code = codeForKey(chord.key);
  if (event.code !== code && !(chord.key === "Escape" && event.key === code)) {
    return false;
  }
  const isMac = platform === "darwin";
  const want = { meta: false, ctrl: false, alt: false, shift: false };
  switch (chord.mod) {
    case "primary":
    case "primary-shift":
    case "primary-alt":
      if (isMac) want.meta = true;
      else want.ctrl = true;
      want.shift = chord.mod === "primary-shift";
      want.alt = chord.mod === "primary-alt";
      break;
    case "secondary":
      if (isMac) want.ctrl = true;
      else want.alt = true;
      break;
    case "control":
      want.ctrl = true;
      break;
    case "shift":
      want.shift = true;
      break;
    case "none":
      break;
  }
  return (
    event.metaKey === want.meta &&
    event.ctrlKey === want.ctrl &&
    event.altKey === want.alt &&
    event.shiftKey === want.shift
  );
}
