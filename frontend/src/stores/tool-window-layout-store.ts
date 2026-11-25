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

const EMPTY_SHOWN: Readonly<Record<SlotId, ToolWindowId | null>> = {
  rightTop: null,
  rightBottom: null,
  bottomLeft: null,
  bottomRight: null,
};

/**
 * 三种视图模式（spec「视图模式」）：停靠·固定、停靠·失焦自动收起、浮动（只在主
 * 窗口内浮动，决策 11）。
 */
export const TOOL_WINDOW_MODES = ["pinned", "autoHide", "float"] as const;

export type ToolWindowMode = (typeof TOOL_WINDOW_MODES)[number];

export const DEFAULT_TOOL_WINDOW_MODES: Readonly<
  Record<ToolWindowId, ToolWindowMode>
> = {
  outline: "pinned",
  changes: "pinned",
  webPreview: "pinned",
  directory: "pinned",
  relations: "pinned",
  sessionInfo: "pinned",
  terminal: "pinned",
};

/** 浮动窗相对工具窗区域左上角的位置与大小（px）。 */
export type FloatRect = { x: number; y: number; width: number; height: number };

export const MIN_FLOAT_SIZE = { width: 220, height: 140 } as const;

/**
 * 四条可拖的分隔条：编辑区 | 右侧（right = 右侧列占的宽度比例）、编辑区 / 底部
 * （bottom = 底部行占的高度比例）、右上 / 右下（rightSplit = 右上占右侧列的比例）、
 * 底部左 | 底部右（bottomSplit = 底部左占底部行的比例）。
 */
export const DIVIDER_IDS = [
  "right",
  "bottom",
  "rightSplit",
  "bottomSplit",
] as const;

export type DividerId = (typeof DIVIDER_IDS)[number];

export const DEFAULT_DIVIDER_RATIOS: Readonly<Record<DividerId, number>> = {
  right: 0.25,
  bottom: 0.32,
  rightSplit: 0.5,
  bottomSplit: 0.5,
};

export const DIVIDER_LIMITS: Readonly<
  Record<DividerId, { min: number; max: number }>
> = {
  right: { min: 0.12, max: 0.6 },
  bottom: { min: 0.12, max: 0.75 },
  rightSplit: { min: 0.15, max: 0.85 },
  bottomSplit: { min: 0.15, max: 0.85 },
};

/**
 * 一个工具窗此刻怎么呈现。可用宽度不足（窄窗口）时，右侧槽位里停靠的工具窗改为
 * 「浮动 + 失焦自动收起」盖在对话上方——只是呈现，保存的模式不变（spec「窄窗口」）。
 */
export function resolvePresentation(
  mode: ToolWindowMode,
  slot: SlotId,
  narrow: boolean,
): { floating: boolean; autoHide: boolean } {
  if (mode === "float") return { floating: true, autoHide: false };
  if (narrow && (slot === "rightTop" || slot === "rightBottom")) {
    return { floating: true, autoHide: true };
  }
  return { floating: false, autoHide: mode === "autoHide" };
}

export const TOOL_WINDOW_LAYOUT_KEY = "tool-window-layout";

/** 旧侧栏（chat-sidebar-store）的持久化键：升级时从它读「开没开、停在哪个 tab」。 */
const LEGACY_SIDEBAR_KEY = "chat-sidebar-state";

type PersistedLayout = {
  /** 每个工具窗住在哪个槽位。 */
  slotOf: Record<ToolWindowId, SlotId>;
  /** 每个槽位此刻显示的工具窗；一个槽位同一时刻最多显示一个。 */
  shown: Record<SlotId, ToolWindowId | null>;
  /** 每个工具窗的视图模式。 */
  mode: Record<ToolWindowId, ToolWindowMode>;
  /** 浮动窗被拖过 / 改过大小后的位置与大小；没有的用默认位置。 */
  floatRect: Partial<Record<ToolWindowId, FloatRect>>;
  /** 四条分隔条的比例。 */
  ratios: Record<DividerId, number>;
  /** 对话页左侧的会话列表是否收起（spec 决策 8：它只能收起 / 展开）。 */
  sessionListCollapsed: boolean;
  /**
   * 专注（⇧⌘\）进入前的显示状态；null = 不在专注。随布局一起保存：在专注里重启，
   * 再按一次仍恢复进入专注之前的显示（spec「专注」+「持久化与升级」）。打开任一工具窗
   * 即退出专注（丢掉它）。
   */
  focusSnapshot: Record<SlotId, ToolWindowId | null> | null;
};

type ToolWindowLayoutState = PersistedLayout & {
  /**
   * 刚被图标条点开、等着拿焦点的工具窗。瞬时信号、不持久化：工具窗挂上后自己
   * 把焦点拿走并调用 clearFocusRequest。
   */
  focusRequest: ToolWindowId | null;
  /**
   * 此刻挂着的图标条数（对话页挂一条；新建会话提示页与其它页面没有）。瞬时、不持久化。
   * 工具窗快捷键「行为与点击图标相同」：没有图标可点的页面上，它们不改写保存的布局。
   */
  stripMounts: number;
  /** 图标条挂上时调用；返回卸下时调用的函数。 */
  mountStrip: () => () => void;
  /** 被最大化的工具窗（⇧⌘'）；瞬时、不持久化。 */
  maximized: ToolWindowId | null;
  /**
   * 窄窗口下被收起的工具窗：保存的布局里它仍然显示，只是此刻不盖在对话上方
   * （spec「窄窗口」：停靠的工具窗改为「浮动 + 失焦自动收起」，这一切换不改写用户
   * 保存的布局）。瞬时、不持久化；窗口恢复宽度后清空，它们回到停靠并显示。
   */
  narrowCollapsed: Partial<Record<ToolWindowId, true>>;
  /**
   * 图标条点击：未显示（或窄窗口下被收起）→ 显示（替换同槽位的那个）并请求焦点；
   * 已显示 → 隐藏。
   */
  toggle: (id: ToolWindowId) => void;
  /**
   * 显示（替换同槽位当前的工具窗），不抢焦点；窄窗口下被收起的工具窗重新盖上来，
   * 这时它拿焦点（否则它会在焦点不在它身上时盖住对话）。
   */
  show: (id: ToolWindowId) => void;
  /** 隐藏；它本来就没显示时什么都不做。 */
  hide: (id: ToolWindowId) => void;
  clearFocusRequest: () => void;
  /** 窄窗口下收起一个正在显示的工具窗，不改写保存的显示状态。 */
  collapseNarrow: (id: ToolWindowId) => void;
  /** 窗口恢复宽度：窄窗口下收起的工具窗全部回到显示。 */
  expandNarrow: () => void;
  setMode: (id: ToolWindowId, mode: ToolWindowMode) => void;
  /** 移到另一个槽位并显示（替换那里当前的工具窗）、请求焦点；移回原槽位不变。 */
  moveTo: (id: ToolWindowId, slot: SlotId) => void;
  setFloatRect: (id: ToolWindowId, rect: FloatRect) => void;
  /** 设分隔比例；超出这条分隔条的上下限时收在边界上。 */
  setRatio: (divider: DividerId, ratio: number) => void;
  /** 专注：隐藏全部工具窗；再来一次恢复之前的显示。 */
  toggleFocusMode: () => void;
  /** 最大化 / 还原一个正在显示的工具窗。 */
  toggleMaximize: (id: ToolWindowId) => void;
  /** 让一个正在显示的工具窗拿焦点（换了呈现位置、被重新挂载之后）。 */
  requestFocus: (id: ToolWindowId) => void;
  /** 恢复默认布局：槽位、显示、模式、比例、浮动位置；会话列表收起状态不在其中。 */
  restoreDefaults: () => void;
  toggleSessionList: () => void;
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
  const shown = sanitizeShown(saved.shown, slotOf, DEFAULT_SHOWN);
  // 专注快照：整份不是对象 → 不在专注；逐槽位非法 → 那个槽位不显示。
  const focusSnapshot =
    saved.focusSnapshot !== null &&
    typeof saved.focusSnapshot === "object" &&
    !Array.isArray(saved.focusSnapshot)
      ? sanitizeShown(saved.focusSnapshot, slotOf, EMPTY_SHOWN)
      : null;
  const savedMode = asRecord(saved.mode);
  const mode = {} as Record<ToolWindowId, ToolWindowMode>;
  for (const id of TOOL_WINDOW_IDS) {
    const v = savedMode[id];
    mode[id] = isMode(v) ? v : DEFAULT_TOOL_WINDOW_MODES[id];
  }
  const savedRects = asRecord(saved.floatRect);
  const floatRect: Partial<Record<ToolWindowId, FloatRect>> = {};
  for (const id of TOOL_WINDOW_IDS) {
    const rect = parseFloatRect(savedRects[id]);
    if (rect !== null) floatRect[id] = rect;
  }
  const savedRatios = asRecord(saved.ratios);
  const ratios = {} as Record<DividerId, number>;
  for (const d of DIVIDER_IDS) {
    const v = savedRatios[d];
    const { min, max } = DIVIDER_LIMITS[d];
    ratios[d] =
      typeof v === "number" && v >= min && v <= max
        ? v
        : DEFAULT_DIVIDER_RATIOS[d];
  }
  const sessionListCollapsed =
    typeof saved.sessionListCollapsed === "boolean"
      ? saved.sessionListCollapsed
      : false;
  return {
    slotOf,
    shown,
    mode,
    floatRect,
    ratios,
    sessionListCollapsed,
    focusSnapshot,
  };
}

/** 每个槽位的显示值：非法时回落到 fallback；显示的工具窗必须真的住在这个槽位。 */
function sanitizeShown(
  raw: unknown,
  slotOf: Record<ToolWindowId, SlotId>,
  fallback: Readonly<Record<SlotId, ToolWindowId | null>>,
): Record<SlotId, ToolWindowId | null> {
  const saved = asRecord(raw);
  const shown = {} as Record<SlotId, ToolWindowId | null>;
  for (const slot of SLOT_IDS) {
    const v = saved[slot];
    const candidate = v === null || isToolWindowId(v) ? v : fallback[slot];
    shown[slot] =
      candidate !== null && slotOf[candidate] === slot ? candidate : null;
  }
  return shown;
}

const MODE_SET: ReadonlySet<string> = new Set(TOOL_WINDOW_MODES);

function isMode(v: unknown): v is ToolWindowMode {
  return typeof v === "string" && MODE_SET.has(v);
}

function isFiniteNumber(v: unknown): v is number {
  return typeof v === "number" && Number.isFinite(v);
}

/** 读回的浮动位置：四项都是有限数、不在左上角之外、不小于最小尺寸才算数。 */
function parseFloatRect(v: unknown): FloatRect | null {
  const r = asRecord(v);
  const { x, y, width, height } = r;
  if (
    !isFiniteNumber(x) ||
    !isFiniteNumber(y) ||
    !isFiniteNumber(width) ||
    !isFiniteNumber(height)
  ) {
    return null;
  }
  if (x < 0 || y < 0) return null;
  if (width < MIN_FLOAT_SIZE.width || height < MIN_FLOAT_SIZE.height) {
    return null;
  }
  return { x, y, width, height };
}

function clamp(v: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, v));
}

function defaultLayoutParts(): Omit<
  PersistedLayout,
  "sessionListCollapsed" | "focusSnapshot"
> {
  return {
    slotOf: { ...DEFAULT_TOOL_WINDOW_SLOTS },
    shown: { ...DEFAULT_SHOWN },
    mode: { ...DEFAULT_TOOL_WINDOW_MODES },
    floatRect: {},
    ratios: { ...DEFAULT_DIVIDER_RATIOS },
  };
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
function layoutFromLegacySidebar(
  raw: string | null,
): Pick<PersistedLayout, "slotOf" | "shown"> | null {
  if (raw === null) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  const state = asRecord(asRecord(parsed).state);
  const open = typeof state.open === "boolean" ? state.open : true;
  const shown: Record<SlotId, ToolWindowId | null> = { ...EMPTY_SHOWN };
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
      ...defaultLayoutParts(),
      sessionListCollapsed: false,
      focusRequest: null,
      focusSnapshot: null,
      maximized: null,
      narrowCollapsed: {},
      stripMounts: 0,
      mountStrip: () => {
        set((s) => ({ stripMounts: s.stripMounts + 1 }));
        let mounted = true;
        return () => {
          if (!mounted) return;
          mounted = false;
          set((s) => ({ stripMounts: Math.max(0, s.stripMounts - 1) }));
        };
      },
      toggle: (id) =>
        set((s) => {
          const slot = s.slotOf[id];
          const narrowCollapsed = withoutCollapsed(s.narrowCollapsed, id);
          if (s.shown[slot] === id && s.narrowCollapsed[id] !== true) {
            return {
              shown: { ...s.shown, [slot]: null },
              focusRequest: s.focusRequest === id ? null : s.focusRequest,
              maximized: s.maximized === id ? null : s.maximized,
              narrowCollapsed,
            };
          }
          return {
            shown: { ...s.shown, [slot]: id },
            focusRequest: id,
            focusSnapshot: null,
            narrowCollapsed,
          };
        }),
      show: (id) =>
        set((s) => {
          const slot = s.slotOf[id];
          const narrowCollapsed = withoutCollapsed(s.narrowCollapsed, id);
          if (s.shown[slot] !== id) {
            return {
              shown: { ...s.shown, [slot]: id },
              focusSnapshot: null,
              narrowCollapsed,
            };
          }
          return narrowCollapsed === s.narrowCollapsed
            ? s
            : { narrowCollapsed, focusRequest: id, focusSnapshot: null };
        }),
      hide: (id) =>
        set((s) => {
          const slot = s.slotOf[id];
          return s.shown[slot] === id
            ? {
                shown: { ...s.shown, [slot]: null },
                maximized: s.maximized === id ? null : s.maximized,
                narrowCollapsed: withoutCollapsed(s.narrowCollapsed, id),
              }
            : s;
        }),
      clearFocusRequest: () => set({ focusRequest: null }),
      collapseNarrow: (id) =>
        set((s) =>
          s.shown[s.slotOf[id]] !== id || s.narrowCollapsed[id] === true
            ? s
            : {
                narrowCollapsed: { ...s.narrowCollapsed, [id]: true },
                focusRequest: s.focusRequest === id ? null : s.focusRequest,
              },
        ),
      expandNarrow: () =>
        set((s) =>
          Object.keys(s.narrowCollapsed).length === 0
            ? s
            : { narrowCollapsed: {} },
        ),
      setMode: (id, mode) =>
        set((s) =>
          s.mode[id] === mode ? s : { mode: { ...s.mode, [id]: mode } },
        ),
      moveTo: (id, slot) =>
        set((s) => {
          const from = s.slotOf[id];
          if (from === slot) return s;
          const shown = { ...s.shown, [slot]: id };
          if (s.shown[from] === id) shown[from] = null;
          return {
            slotOf: { ...s.slotOf, [id]: slot },
            shown,
            focusRequest: id,
            focusSnapshot: null,
            narrowCollapsed: withoutCollapsed(s.narrowCollapsed, id),
          };
        }),
      setFloatRect: (id, rect) =>
        set((s) => ({
          floatRect: {
            ...s.floatRect,
            [id]: {
              x: Math.max(0, rect.x),
              y: Math.max(0, rect.y),
              width: Math.max(MIN_FLOAT_SIZE.width, rect.width),
              height: Math.max(MIN_FLOAT_SIZE.height, rect.height),
            },
          },
        })),
      setRatio: (divider, ratio) =>
        set((s) => {
          const { min, max } = DIVIDER_LIMITS[divider];
          const next = clamp(ratio, min, max);
          return s.ratios[divider] === next
            ? s
            : { ratios: { ...s.ratios, [divider]: next } };
        }),
      toggleFocusMode: () =>
        set((s) => {
          if (s.focusSnapshot !== null) {
            // 恢复 = 让它们重新出现：窄窗口下此前失焦收起的也一并展开。
            let narrowCollapsed = s.narrowCollapsed;
            for (const id of Object.values(s.focusSnapshot)) {
              if (id !== null) {
                narrowCollapsed = withoutCollapsed(narrowCollapsed, id);
              }
            }
            return {
              shown: s.focusSnapshot,
              focusSnapshot: null,
              narrowCollapsed,
            };
          }
          if (SLOT_IDS.every((slot) => s.shown[slot] === null)) return s;
          return {
            shown: { ...EMPTY_SHOWN },
            focusSnapshot: s.shown,
            maximized: null,
          };
        }),
      // 最大化 / 还原会把工具窗挪到另一处渲染（重新挂载），焦点跟着它过去。
      toggleMaximize: (id) =>
        set((s) => {
          if (s.maximized === id) return { maximized: null, focusRequest: id };
          return s.shown[s.slotOf[id]] === id
            ? { maximized: id, focusRequest: id }
            : s;
        }),
      requestFocus: (id) =>
        set((s) =>
          s.shown[s.slotOf[id]] === id
            ? {
                focusRequest: id,
                narrowCollapsed: withoutCollapsed(s.narrowCollapsed, id),
              }
            : s,
        ),
      restoreDefaults: () =>
        set({
          ...defaultLayoutParts(),
          focusSnapshot: null,
          maximized: null,
          narrowCollapsed: {},
        }),
      toggleSessionList: () =>
        set((s) => ({ sessionListCollapsed: !s.sessionListCollapsed })),
    }),
    {
      name: TOOL_WINDOW_LAYOUT_KEY,
      storage: createJSONStorage(() => layoutStorage),
      partialize: (s): PersistedLayout => ({
        slotOf: s.slotOf,
        shown: s.shown,
        mode: s.mode,
        floatRect: s.floatRect,
        ratios: s.ratios,
        sessionListCollapsed: s.sessionListCollapsed,
        focusSnapshot: s.focusSnapshot,
      }),
      // 读回值缺失时同样逐项回落到默认，而不是沿用内存里的当前值。
      merge: (persisted, current) => ({
        ...current,
        ...sanitizeLayout(persisted),
      }),
    },
  ),
);

function withoutCollapsed(
  collapsed: Partial<Record<ToolWindowId, true>>,
  id: ToolWindowId,
): Partial<Record<ToolWindowId, true>> {
  if (collapsed[id] !== true) return collapsed;
  const next = { ...collapsed };
  delete next[id];
  return next;
}

/** 这个工具窗此刻是否显示。 */
export function selectIsToolWindowShown(id: ToolWindowId) {
  return (s: ToolWindowLayoutState) => s.shown[s.slotOf[id]] === id;
}
