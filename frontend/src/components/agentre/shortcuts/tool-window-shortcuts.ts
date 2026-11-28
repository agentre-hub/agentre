import {
  TOOL_WINDOW_IDS,
  useToolWindowLayout,
  type ToolWindowId,
  type ToolWindowRegion,
} from "@/stores/tool-window-layout-store";

import type { DesktopPlatform } from "../chrome";

import { sameChord } from "./conflict";
import { effectiveChord } from "./effective-bindings";
import { chordFromEvent, formatChord, matchesChord } from "./format";
import {
  SESSION_LIST_TOGGLE_ID,
  TOOL_WINDOWS_HIDE_ALL_ID,
  TOOL_WINDOW_HIDE_FOCUSED_ID,
  TOOL_WINDOW_MAXIMIZE_ID,
  TOOL_WINDOW_REGION_TOGGLE_IDS,
  TOOL_WINDOW_RETURN_FOCUS_ID,
  TOOL_WINDOW_TOGGLE_IDS,
  getDef,
} from "./registry";

type KeyEventLike = Pick<
  KeyboardEvent,
  "code" | "key" | "metaKey" | "ctrlKey" | "altKey" | "shiftKey"
>;

function matchesId(
  event: KeyEventLike,
  id: string,
  platform: DesktopPlatform,
): boolean {
  const def = getDef(id);
  return def !== undefined && matchesChord(event, def.defaultBinding, platform);
}

const REGIONS: readonly ToolWindowRegion[] = ["bottom", "right"];

/**
 * 按下的是不是某个区域开关（默认 ⌘J / ⌥⌘B）。它们可改绑，所以按用户当前的绑定比，
 * 并且和录制时一样经 chordFromEvent 取和弦——录下什么就认什么。
 */
function matchRegionToggle(
  event: KeyEventLike,
  platform: DesktopPlatform,
): ToolWindowRegion | null {
  const chord = chordFromEvent(event, platform);
  if (!chord) return null;
  for (const region of REGIONS) {
    const bound = effectiveChord(TOOL_WINDOW_REGION_TOGGLE_IDS[region]);
    if (bound && sameChord(chord, bound)) return region;
  }
  return null;
}

/**
 * 全局派发的工具窗快捷键（不论焦点在哪）：⌃1–6 / ⌃` 切换工具窗、⇧⌘\ 专注、⌘J /
 * ⌥⌘B 底部 / 右侧区域开关、⌘0 收起 / 展开会话列表。命中就执行并返回 true。
 */
export function dispatchGlobalToolWindowShortcut(
  event: KeyEventLike,
  platform: DesktopPlatform,
): boolean {
  const layout = useToolWindowLayout.getState();
  if (matchesId(event, SESSION_LIST_TOGGLE_ID, platform)) {
    layout.toggleSessionList();
    return true;
  }
  // 工具窗的那几个「行为与点击图标相同」：页面上没有图标条（新建会话提示页、对话页
  // 以外的页面）时没有图标可点，它们不生效、不改写保存的布局，按键照常往下走。
  // ⌘0 管的是会话列表，不受此限。
  if (layout.stripMounts === 0) return false;
  for (const id of TOOL_WINDOW_IDS) {
    if (matchesId(event, TOOL_WINDOW_TOGGLE_IDS[id], platform)) {
      layout.toggle(id);
      return true;
    }
  }
  if (matchesId(event, TOOL_WINDOWS_HIDE_ALL_ID, platform)) {
    layout.toggleFocusMode();
    return true;
  }
  const region = matchRegionToggle(event, platform);
  if (region) {
    layout.toggleRegion(region);
    return true;
  }
  return false;
}

export type FocusedToolWindowAction = "hide" | "returnFocus" | "maximize";

/** 只在焦点落在某个工具窗里时才有意义的按键：⇧Esc、Esc、⇧⌘'。 */
export function matchFocusedToolWindowShortcut(
  event: KeyEventLike,
  platform: DesktopPlatform,
): FocusedToolWindowAction | null {
  if (matchesId(event, TOOL_WINDOW_HIDE_FOCUSED_ID, platform)) return "hide";
  if (matchesId(event, TOOL_WINDOW_RETURN_FOCUS_ID, platform)) {
    return "returnFocus";
  }
  if (matchesId(event, TOOL_WINDOW_MAXIMIZE_ID, platform)) return "maximize";
  return null;
}

/**
 * 焦点在终端里时要交还给应用的按键：工具窗切换（⌃1–6 / Alt+数字、⌃`）、专注
 * （⇧⌘\）、区域开关（⌘J / ⌥⌘B 或用户改绑的组合）、⇧Esc、⇧⌘'。xterm 自己会吃掉其中一些（⌃3–⌃7、Alt+键、Esc 一类），不放行
 * 它们就到不了应用的键盘监听。单按 Esc 不在其中：它留给终端里的程序（vim 等），
 * 是对「Esc 把焦点还给输入框」的有意偏离。
 */
export function isToolWindowShortcutForTerminal(
  event: KeyEventLike,
  platform: DesktopPlatform,
): boolean {
  for (const id of TOOL_WINDOW_IDS) {
    if (matchesId(event, TOOL_WINDOW_TOGGLE_IDS[id], platform)) return true;
  }
  if (matchesId(event, TOOL_WINDOWS_HIDE_ALL_ID, platform)) return true;
  if (matchRegionToggle(event, platform)) return true;
  const focused = matchFocusedToolWindowShortcut(event, platform);
  return focused === "hide" || focused === "maximize";
}

/** 工具窗切换快捷键的按键文案（图标悬停提示用）。 */
export function formatToolWindowToggle(
  id: ToolWindowId,
  platform: DesktopPlatform,
): string {
  const def = getDef(TOOL_WINDOW_TOGGLE_IDS[id]);
  return def ? formatChord(def.defaultBinding, platform) : "";
}

/** 菜单里「最大化」「隐藏」旁的按键文案。 */
export function formatFocusedToolWindowShortcut(
  action: Exclude<FocusedToolWindowAction, "returnFocus">,
  platform: DesktopPlatform,
): string {
  const def = getDef(
    action === "hide" ? TOOL_WINDOW_HIDE_FOCUSED_ID : TOOL_WINDOW_MAXIMIZE_ID,
  );
  return def ? formatChord(def.defaultBinding, platform) : "";
}
