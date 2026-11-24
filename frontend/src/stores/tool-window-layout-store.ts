import { create } from "zustand";
import {
  createJSONStorage,
  persist,
  type StateStorage,
} from "zustand/middleware";

/**
 * 对话页的七个工具窗（spec 决策 2）。顺序就是图标条同一组里的排列顺序。
 * 布局 store 认识全部七个；还没有实现的工具窗只是图标条上不出现，布局照常记着它。
 */
export const TOOL_WINDOW_IDS = [
  "outline",
  "changes",
  "webPreview",
  "directory",
  "relations",
  "sessionInfo",
  "terminal",
] as const;

export type ToolWindowId = (typeof TOOL_WINDOW_IDS)[number];

/** 四个槽位：右上、右下、底部左、底部右（spec「槽位与默认」）。 */
export const SLOT_IDS = [
  "rightTop",
  "rightBottom",
  "bottomLeft",
  "bottomRight",
] as const;

export type SlotId = (typeof SLOT_IDS)[number];

/** 默认槽位（spec 决策 6）：网页预览放右上是因为它要高度；底部右默认为空。 */
export const DEFAULT_TOOL_WINDOW_SLOTS: Readonly<Record<ToolWindowId, SlotId>> =
  {
    outline: "rightTop",
    changes: "rightTop",
    webPreview: "rightTop",
    directory: "rightBottom",
    relations: "rightBottom",
    sessionInfo: "rightBottom",
    terminal: "bottomLeft",
  };

/**
 * 全新安装时显示什么：与旧侧栏的出厂状态一致（打开、停在「大纲」）。
 */
const DEFAULT_SHOWN: Readonly<Record<SlotId, ToolWindowId | null>> = {
  rightTop: "outline",
  rightBottom: null,
  bottomLeft: null,
  bottomRight: null,
};

export const TOOL_WINDOW_LAYOUT_KEY = "tool-window-layout";

/** 旧侧栏（chat-sidebar-store）的持久化键：升级时从它读「开没开、停在哪个 tab」。 */
const LEGACY_SIDEBAR_KEY = "chat-sidebar-state";

type PersistedLayout = {
  /** 每个工具窗住在哪个槽位。 */
  slotOf: Record<ToolWindowId, SlotId>;
  /** 每个槽位此刻显示的工具窗；一个槽位同一时刻最多显示一个。 */
  shown: Record<SlotId, ToolWindowId | null>;
};

type ToolWindowLayoutState = PersistedLayout & {
  /**
   * 刚被图标条点开、等着拿焦点的工具窗。瞬时信号、不持久化：工具窗挂上后自己
   * 把焦点拿走并调用 clearFocusRequest。
   */
  focusRequest: ToolWindowId | null;
  /** 图标条点击：未显示 → 显示（替换同槽位的那个）并请求焦点；已显示 → 隐藏。 */
  toggle: (id: ToolWindowId) => void;
  /** 显示（替换同槽位当前的工具窗），不抢焦点。 */
  show: (id: ToolWindowId) => void;
  /** 隐藏；它本来就没显示时什么都不做。 */
  hide: (id: ToolWindowId) => void;
  clearFocusRequest: () => void;
};

const TOOL_WINDOW_ID_SET: ReadonlySet<string> = new Set(TOOL_WINDOW_IDS);
const SLOT_ID_SET: ReadonlySet<string> = new Set(SLOT_IDS);

function isToolWindowId(v: unknown): v is ToolWindowId {
  return typeof v === "string" && TOOL_WINDOW_ID_SET.has(v);
}

function isSlotId(v: unknown): v is SlotId {
  return typeof v === "string" && SLOT_ID_SET.has(v);
}

function asRecord(v: unknown): Record<string, unknown> {
  return v !== null && typeof v === "object" && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : {};
}

/**
 * 读回的布局逐项过一遍：每个工具窗的槽位、每个槽位的显示值各自独立回落到默认，
 * 一项坏了不连累其它项（spec「持久化与升级」）。槽位显示的工具窗必须真的住在
 * 这个槽位——对不上时这个槽位不显示，而不是凭空把它挪过来。
 */
function sanitizeLayout(raw: unknown): PersistedLayout {
  const saved = asRecord(raw);
  const savedSlotOf = asRecord(saved.slotOf);
  const slotOf = {} as Record<ToolWindowId, SlotId>;
  for (const id of TOOL_WINDOW_IDS) {
    const v = savedSlotOf[id];
    slotOf[id] = isSlotId(v) ? v : DEFAULT_TOOL_WINDOW_SLOTS[id];
  }
  const savedShown = asRecord(saved.shown);
  const shown = {} as Record<SlotId, ToolWindowId | null>;
  for (const slot of SLOT_IDS) {
    const v = savedShown[slot];
    const candidate = v === null || isToolWindowId(v) ? v : DEFAULT_SHOWN[slot];
    shown[slot] =
      candidate !== null && slotOf[candidate] === slot ? candidate : null;
  }
  return { slotOf, shown };
}

const LEGACY_TABS: ReadonlySet<string> = new Set([
  "outline",
  "changes",
  "directory",
]);

/**
 * 从旧侧栏状态推出第一份布局：旧侧栏开着 → 显示旧一级 tab 对应的工具窗；关着 →
 * 什么都不显示。开关与 tab 的取值按旧 store 自己的回落规则解读（开关缺省为开、
 * 非法 tab 回落到「大纲」），用户升级前看到的是什么，升级后就还是什么。
 * 没有旧状态（全新安装）或读不懂时返回 null，交给默认值。
 */
function layoutFromLegacySidebar(raw: string | null): PersistedLayout | null {
  if (raw === null) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  const state = asRecord(asRecord(parsed).state);
  const open = typeof state.open === "boolean" ? state.open : true;
  const shown: Record<SlotId, ToolWindowId | null> = {
    rightTop: null,
    rightBottom: null,
    bottomLeft: null,
    bottomRight: null,
  };
  if (open) {
    const tab =
      typeof state.activeTab === "string" && LEGACY_TABS.has(state.activeTab)
        ? (state.activeTab as ToolWindowId)
        : "outline";
    shown[DEFAULT_TOOL_WINDOW_SLOTS[tab]] = tab;
  }
  return { slotOf: { ...DEFAULT_TOOL_WINDOW_SLOTS }, shown };
}

/**
 * 布局键还不存在时从旧侧栏迁移一次，并**立刻**把结果写成新键：旧键之后会被侧栏
 * store 改写成只剩「变更」档位与「显示忽略项」两项偏好，迁移只能发生在那之前，
 * 且只发生一次。迁移放在存储层而不是 merge 里，是因为 merge 不会触发落盘。
 */
const layoutStorage: StateStorage = {
  getItem: (name) => {
    const own = localStorage.getItem(name);
    if (own !== null) return own;
    const migrated = layoutFromLegacySidebar(
      localStorage.getItem(LEGACY_SIDEBAR_KEY),
    );
    if (migrated === null) return null;
    const value = JSON.stringify({ state: migrated, version: 0 });
    localStorage.setItem(name, value);
    return value;
  },
  setItem: (name, value) => localStorage.setItem(name, value),
  removeItem: (name) => localStorage.removeItem(name),
};

/**
 * 对话页工具窗的布局，全局一份（spec 决策 7）：换会话、换标签页、重启都保持。
 */
export const useToolWindowLayout = create<ToolWindowLayoutState>()(
  persist(
    (set) => ({
      slotOf: { ...DEFAULT_TOOL_WINDOW_SLOTS },
      shown: { ...DEFAULT_SHOWN },
      focusRequest: null,
      toggle: (id) =>
        set((s) => {
          const slot = s.slotOf[id];
          if (s.shown[slot] === id) {
            return {
              shown: { ...s.shown, [slot]: null },
              focusRequest: s.focusRequest === id ? null : s.focusRequest,
            };
          }
          return { shown: { ...s.shown, [slot]: id }, focusRequest: id };
        }),
      show: (id) =>
        set((s) => {
          const slot = s.slotOf[id];
          return s.shown[slot] === id
            ? s
            : { shown: { ...s.shown, [slot]: id } };
        }),
      hide: (id) =>
        set((s) => {
          const slot = s.slotOf[id];
          return s.shown[slot] === id
            ? { shown: { ...s.shown, [slot]: null } }
            : s;
        }),
      clearFocusRequest: () => set({ focusRequest: null }),
    }),
    {
      name: TOOL_WINDOW_LAYOUT_KEY,
      storage: createJSONStorage(() => layoutStorage),
      partialize: (s): PersistedLayout => ({
        slotOf: s.slotOf,
        shown: s.shown,
      }),
      // 读回值缺失时同样逐项回落到默认，而不是沿用内存里的当前值。
      merge: (persisted, current) => ({
        ...current,
        ...sanitizeLayout(persisted),
      }),
    },
  ),
);

/** 这个工具窗此刻是否显示。 */
export function selectIsToolWindowShown(id: ToolWindowId) {
  return (s: ToolWindowLayoutState) => s.shown[s.slotOf[id]] === id;
}
