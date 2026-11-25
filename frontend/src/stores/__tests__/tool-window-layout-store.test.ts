import { beforeEach, describe, expect, it } from "vitest";

import { useChatSidebarStore } from "../chat-sidebar-store";
import {
  DEFAULT_DIVIDER_RATIOS,
  DEFAULT_TOOL_WINDOW_MODES,
  DEFAULT_TOOL_WINDOW_SLOTS,
  DIVIDER_LIMITS,
  MIN_FLOAT_SIZE,
  TOOL_WINDOW_IDS,
  TOOL_WINDOW_LAYOUT_KEY,
  resolvePresentation,
  useToolWindowLayout,
  type SlotId,
  type ToolWindowId,
} from "../tool-window-layout-store";

const LEGACY_KEY = "chat-sidebar-state";

function persistLayout(state: unknown) {
  localStorage.setItem(
    TOOL_WINDOW_LAYOUT_KEY,
    JSON.stringify({ state, version: 0 }),
  );
}

function persistLegacy(state: unknown) {
  localStorage.setItem(LEGACY_KEY, JSON.stringify({ state, version: 0 }));
}

function shownWindows(): ToolWindowId[] {
  const { shown } = useToolWindowLayout.getState();
  return Object.values(shown).filter((id): id is ToolWindowId => id !== null);
}

async function rehydrate() {
  await useToolWindowLayout.persist.rehydrate();
}

function resetToDefaults() {
  useToolWindowLayout.setState({
    slotOf: { ...DEFAULT_TOOL_WINDOW_SLOTS },
    shown: {
      rightTop: "outline",
      rightBottom: null,
      bottomLeft: null,
      bottomRight: null,
    },
    focusRequest: null,
    mode: { ...DEFAULT_TOOL_WINDOW_MODES },
    floatRect: {},
    ratios: { ...DEFAULT_DIVIDER_RATIOS },
    sessionListCollapsed: false,
    focusSnapshot: null,
    maximized: null,
  });
}

describe("tool-window-layout-store", () => {
  beforeEach(() => {
    resetToDefaults();
    // setState 会经 persist 落盘；清在它之后，每条用例从「本机什么都没存」开始。
    localStorage.clear();
  });

  describe("槽位与默认", () => {
    it("Given 七个工具窗, Then 默认槽位是 右上 大纲/变更/网页预览、右下 目录/关联/会话信息、底部左 终端、底部右为空", () => {
      expect(TOOL_WINDOW_IDS).toHaveLength(7);
      expect(DEFAULT_TOOL_WINDOW_SLOTS).toEqual({
        outline: "rightTop",
        changes: "rightTop",
        webPreview: "rightTop",
        directory: "rightBottom",
        relations: "rightBottom",
        sessionInfo: "rightBottom",
        terminal: "bottomLeft",
      });
      const inBottomRight = TOOL_WINDOW_IDS.filter(
        (id) => DEFAULT_TOOL_WINDOW_SLOTS[id] === "bottomRight",
      );
      expect(inBottomRight).toEqual([]);
    });

    it("Given 全新安装（本机既没有布局也没有旧侧栏状态）, When 读回, Then 布局是默认值、只显示大纲", async () => {
      useToolWindowLayout.setState({
        shown: {
          rightTop: null,
          rightBottom: "directory",
          bottomLeft: null,
          bottomRight: null,
        },
      });
      localStorage.clear();
      await rehydrate();
      expect(useToolWindowLayout.getState().slotOf).toEqual(
        DEFAULT_TOOL_WINDOW_SLOTS,
      );
      expect(shownWindows()).toEqual(["outline"]);
    });
  });

  describe("显示 / 隐藏", () => {
    it("Given 大纲在右上显示, When 点变更, Then 变更替换右上的大纲并请求焦点", () => {
      useToolWindowLayout.getState().toggle("changes");
      const s = useToolWindowLayout.getState();
      expect(s.shown.rightTop).toBe("changes");
      expect(s.focusRequest).toBe("changes");
    });

    it("Given 大纲在右上显示, When 点目录, Then 目录显示在右下、大纲仍在右上", () => {
      useToolWindowLayout.getState().toggle("directory");
      const s = useToolWindowLayout.getState();
      expect(s.shown.rightTop).toBe("outline");
      expect(s.shown.rightBottom).toBe("directory");
    });

    it("Given 大纲已显示, When 再点大纲, Then 它被隐藏、不请求焦点", () => {
      useToolWindowLayout.getState().toggle("outline");
      const s = useToolWindowLayout.getState();
      expect(s.shown.rightTop).toBeNull();
      expect(s.focusRequest).toBeNull();
    });

    it("Given 变更未显示, When hide(变更), Then 右上的大纲不受影响", () => {
      useToolWindowLayout.getState().hide("changes");
      expect(useToolWindowLayout.getState().shown.rightTop).toBe("outline");
    });

    it("Given 显示状态改变, Then 写进本机布局键（全局一份）", () => {
      useToolWindowLayout.getState().toggle("directory");
      const raw = localStorage.getItem(TOOL_WINDOW_LAYOUT_KEY) ?? "";
      expect(raw).toContain('"rightBottom":"directory"');
      // 焦点请求是瞬时的，不进持久化。
      expect(raw).not.toContain("focusRequest");
    });

    it("Given 焦点请求已被工具窗消费, When clearFocusRequest, Then 请求清空", () => {
      useToolWindowLayout.getState().toggle("changes");
      useToolWindowLayout.getState().clearFocusRequest();
      expect(useToolWindowLayout.getState().focusRequest).toBeNull();
    });
  });

  describe("持久化读回：非法或缺失时逐项回落", () => {
    it("Given 已存的合法布局, When 读回, Then 原样恢复", async () => {
      persistLayout({
        slotOf: { ...DEFAULT_TOOL_WINDOW_SLOTS, changes: "bottomRight" },
        shown: {
          rightTop: null,
          rightBottom: "directory",
          bottomLeft: null,
          bottomRight: "changes",
        },
      });
      await rehydrate();
      const s = useToolWindowLayout.getState();
      expect(s.slotOf.changes).toBe("bottomRight");
      expect(s.shown).toEqual({
        rightTop: null,
        rightBottom: "directory",
        bottomLeft: null,
        bottomRight: "changes",
      });
    });

    it("Given 某个工具窗的槽位非法、另一个缺失, When 读回, Then 只有这两项回落到默认槽位，其余保持", async () => {
      persistLayout({
        slotOf: {
          outline: "nowhere",
          changes: "bottomRight",
          directory: "rightTop",
        },
        shown: {
          rightTop: "directory",
          rightBottom: null,
          bottomLeft: null,
          bottomRight: null,
        },
      });
      await rehydrate();
      const { slotOf, shown } = useToolWindowLayout.getState();
      expect(slotOf.outline).toBe("rightTop");
      expect(slotOf.changes).toBe("bottomRight");
      expect(slotOf.directory).toBe("rightTop");
      expect(slotOf.terminal).toBe("bottomLeft");
      expect(shown.rightTop).toBe("directory");
    });

    it("Given 某槽位显示的是未知工具窗, When 读回, Then 该槽位回落到默认，其它槽位保持", async () => {
      persistLayout({
        slotOf: DEFAULT_TOOL_WINDOW_SLOTS,
        shown: {
          rightTop: "bogus",
          rightBottom: "directory",
          bottomLeft: null,
          bottomRight: null,
        },
      });
      await rehydrate();
      expect(useToolWindowLayout.getState().shown).toEqual({
        rightTop: "outline",
        rightBottom: "directory",
        bottomLeft: null,
        bottomRight: null,
      });
    });

    it("Given 某槽位显示的工具窗并不住在这个槽位, When 读回, Then 该槽位不显示任何工具窗", async () => {
      persistLayout({
        slotOf: DEFAULT_TOOL_WINDOW_SLOTS,
        shown: {
          rightTop: "outline",
          rightBottom: "changes",
          bottomLeft: null,
          bottomRight: null,
        },
      });
      await rehydrate();
      const { shown } = useToolWindowLayout.getState();
      expect(shown.rightTop).toBe("outline");
      expect(shown.rightBottom).toBeNull();
    });

    it("Given 已存布局缺了 shown 整项, When 读回, Then 显示状态回落到默认、槽位照用已存的", async () => {
      persistLayout({
        slotOf: { ...DEFAULT_TOOL_WINDOW_SLOTS, outline: "rightBottom" },
      });
      await rehydrate();
      const { slotOf, shown } = useToolWindowLayout.getState();
      expect(slotOf.outline).toBe("rightBottom");
      // 默认显示的大纲已被挪走，右上的默认值与它住的槽位对不上 → 不显示。
      expect(shown.rightTop).toBeNull();
      expect(shown.rightBottom).toBeNull();
    });

    it("Given 已存值不是对象（被手改坏了）, When 读回, Then 整体回落到默认", async () => {
      persistLayout("garbage");
      await rehydrate();
      const s = useToolWindowLayout.getState();
      expect(s.slotOf).toEqual(DEFAULT_TOOL_WINDOW_SLOTS);
      expect(shownWindows()).toEqual(["outline"]);
    });
  });

  describe("从旧侧栏升级", () => {
    it.each<[string, ToolWindowId, SlotId]>([
      ["outline", "outline", "rightTop"],
      ["changes", "changes", "rightTop"],
      ["directory", "directory", "rightBottom"],
    ])(
      "Given 旧侧栏打开、一级 tab 是 %s, When 首次读回, Then 只显示对应的工具窗",
      async (tab, id, slot) => {
        persistLegacy({ open: true, activeTab: tab });
        await rehydrate();
        const { shown } = useToolWindowLayout.getState();
        expect(shown[slot]).toBe(id);
        expect(shownWindows()).toEqual([id]);
      },
    );

    it("Given 旧侧栏是关闭的, When 首次读回, Then 不显示任何工具窗", async () => {
      persistLegacy({ open: false, activeTab: "changes" });
      await rehydrate();
      expect(shownWindows()).toEqual([]);
      expect(useToolWindowLayout.getState().slotOf).toEqual(
        DEFAULT_TOOL_WINDOW_SLOTS,
      );
    });

    it("Given 旧侧栏打开但一级 tab 是已废弃的值, When 首次读回, Then 显示大纲（旧侧栏当时的回落）", async () => {
      persistLegacy({ open: true, activeTab: "git" });
      await rehydrate();
      expect(shownWindows()).toEqual(["outline"]);
    });

    it("Given 迁移完成, Then 新布局立刻落盘，旧键之后被改写也不会再迁一次", async () => {
      persistLegacy({ open: false, activeTab: "outline" });
      await rehydrate();
      expect(localStorage.getItem(TOOL_WINDOW_LAYOUT_KEY)).not.toBeNull();

      // 旧键被侧栏 store 改写成只剩偏好之后，再读回仍是迁移出来的「全不显示」。
      persistLegacy({ changesScope: "session", showIgnored: false });
      await rehydrate();
      expect(shownWindows()).toEqual([]);
    });

    it("Given 已有新布局, When 读回, Then 旧侧栏状态被忽略", async () => {
      persistLayout({
        slotOf: DEFAULT_TOOL_WINDOW_SLOTS,
        shown: {
          rightTop: null,
          rightBottom: "directory",
          bottomLeft: null,
          bottomRight: null,
        },
      });
      persistLegacy({ open: true, activeTab: "changes" });
      await rehydrate();
      expect(shownWindows()).toEqual(["directory"]);
    });

    it("Given 旧侧栏存着「变更」档位与「显示忽略项」, When 升级, Then 两项偏好保留", async () => {
      persistLegacy({
        open: true,
        activeTab: "changes",
        changesScope: "uncommitted",
        showIgnored: true,
      });
      await rehydrate();
      await useChatSidebarStore.persist.rehydrate();
      expect(useChatSidebarStore.getState().changesScope).toBe("uncommitted");
      expect(useChatSidebarStore.getState().showIgnored).toBe(true);
    });
  });

  describe("视图模式", () => {
    it("Given 全新布局, Then 每个工具窗都是「停靠·固定」", () => {
      const { mode } = useToolWindowLayout.getState();
      for (const id of TOOL_WINDOW_IDS) expect(mode[id]).toBe("pinned");
    });

    it("Given 变更是固定模式, When 设为浮动, Then 只有变更变了，并写进本机布局", () => {
      useToolWindowLayout.getState().setMode("changes", "float");
      const { mode } = useToolWindowLayout.getState();
      expect(mode.changes).toBe("float");
      expect(mode.outline).toBe("pinned");
      const raw = localStorage.getItem(TOOL_WINDOW_LAYOUT_KEY) ?? "";
      expect(raw).toContain('"changes":"float"');
    });
  });

  describe("窄窗口覆盖（不改写保存的模式）", () => {
    it("Given 可用宽度不足, When 解析右侧槽位里停靠的工具窗, Then 以浮动 + 失焦自动收起呈现", () => {
      expect(resolvePresentation("pinned", "rightTop", true)).toEqual({
        floating: true,
        autoHide: true,
      });
      expect(resolvePresentation("autoHide", "rightBottom", true)).toEqual({
        floating: true,
        autoHide: true,
      });
    });

    it("Given 可用宽度不足, Then 底部槽位与浮动窗照旧", () => {
      expect(resolvePresentation("pinned", "bottomLeft", true)).toEqual({
        floating: false,
        autoHide: false,
      });
      expect(resolvePresentation("float", "rightTop", true)).toEqual({
        floating: true,
        autoHide: false,
      });
    });

    it("Given 宽度足够, Then 三种模式按保存的呈现", () => {
      expect(resolvePresentation("pinned", "rightTop", false)).toEqual({
        floating: false,
        autoHide: false,
      });
      expect(resolvePresentation("autoHide", "rightTop", false)).toEqual({
        floating: false,
        autoHide: true,
      });
      expect(resolvePresentation("float", "bottomLeft", false)).toEqual({
        floating: true,
        autoHide: false,
      });
    });
  });

  describe("移到槽位", () => {
    it("Given 大纲在右上显示, When 移到底部右, Then 它住进底部右并显示、右上空出、请求焦点", () => {
      useToolWindowLayout.getState().moveTo("outline", "bottomRight");
      const s = useToolWindowLayout.getState();
      expect(s.slotOf.outline).toBe("bottomRight");
      expect(s.shown.bottomRight).toBe("outline");
      expect(s.shown.rightTop).toBeNull();
      expect(s.focusRequest).toBe("outline");
    });

    it("Given 目录在右下显示, When 把未显示的变更移到右下, Then 变更替换目录", () => {
      useToolWindowLayout.getState().show("directory");
      useToolWindowLayout.getState().moveTo("changes", "rightBottom");
      const s = useToolWindowLayout.getState();
      expect(s.shown.rightBottom).toBe("changes");
      expect(s.shown.rightTop).toBe("outline");
    });

    it("Given 大纲住在右上, When 移到右上（原组）, Then 什么都不变", () => {
      const before = useToolWindowLayout.getState();
      useToolWindowLayout.getState().moveTo("outline", "rightTop");
      const after = useToolWindowLayout.getState();
      expect(after.slotOf).toBe(before.slotOf);
      expect(after.shown).toBe(before.shown);
      expect(after.focusRequest).toBeNull();
    });
  });

  describe("浮动位置与大小", () => {
    it("Given 浮动窗, When 记下位置与大小, Then 读得回来并写进本机布局", () => {
      useToolWindowLayout
        .getState()
        .setFloatRect("directory", { x: 40, y: 30, width: 320, height: 400 });
      expect(useToolWindowLayout.getState().floatRect.directory).toEqual({
        x: 40,
        y: 30,
        width: 320,
        height: 400,
      });
      const raw = localStorage.getItem(TOOL_WINDOW_LAYOUT_KEY) ?? "";
      expect(raw).toContain(
        '"directory":{"x":40,"y":30,"width":320,"height":400}',
      );
    });

    it("Given 拖得比最小尺寸还小或拖出左上角, Then 尺寸收在最小值、位置不为负", () => {
      useToolWindowLayout
        .getState()
        .setFloatRect("directory", { x: -20, y: -5, width: 10, height: 10 });
      expect(useToolWindowLayout.getState().floatRect.directory).toEqual({
        x: 0,
        y: 0,
        width: MIN_FLOAT_SIZE.width,
        height: MIN_FLOAT_SIZE.height,
      });
    });
  });

  describe("分隔比例", () => {
    it("Given 全新布局, Then 四条分隔条都是默认比例", () => {
      expect(useToolWindowLayout.getState().ratios).toEqual(
        DEFAULT_DIVIDER_RATIOS,
      );
    });

    it("Given 拖动右侧分隔条, When 设比例, Then 只改这一条并写进本机布局", () => {
      useToolWindowLayout.getState().setRatio("right", 0.4);
      const { ratios } = useToolWindowLayout.getState();
      expect(ratios.right).toBe(0.4);
      expect(ratios.bottom).toBe(DEFAULT_DIVIDER_RATIOS.bottom);
      const raw = localStorage.getItem(TOOL_WINDOW_LAYOUT_KEY) ?? "";
      expect(raw).toContain('"right":0.4');
    });

    it("Given 拖过了头, Then 比例收在这条分隔条的上下限内", () => {
      useToolWindowLayout.getState().setRatio("rightSplit", 2);
      useToolWindowLayout.getState().setRatio("bottom", -1);
      const { ratios } = useToolWindowLayout.getState();
      expect(ratios.rightSplit).toBe(DIVIDER_LIMITS.rightSplit.max);
      expect(ratios.bottom).toBe(DIVIDER_LIMITS.bottom.min);
    });
  });

  describe("专注", () => {
    it("Given 右上大纲、右下目录显示, When 进入专注, Then 全部隐藏；再按一次恢复之前的显示", () => {
      useToolWindowLayout.getState().show("directory");
      useToolWindowLayout.getState().toggleFocusMode();
      expect(shownWindows()).toEqual([]);

      useToolWindowLayout.getState().toggleFocusMode();
      expect(useToolWindowLayout.getState().shown).toEqual({
        rightTop: "outline",
        rightBottom: "directory",
        bottomLeft: null,
        bottomRight: null,
      });
    });

    it("Given 处于专注, When 打开任一工具窗, Then 退出专注：再按专注是把它隐藏，而不是恢复旧的", () => {
      useToolWindowLayout.getState().toggleFocusMode();
      useToolWindowLayout.getState().toggle("changes");
      expect(shownWindows()).toEqual(["changes"]);
      expect(useToolWindowLayout.getState().focusSnapshot).toBeNull();

      useToolWindowLayout.getState().toggleFocusMode();
      expect(shownWindows()).toEqual([]);
      useToolWindowLayout.getState().toggleFocusMode();
      expect(shownWindows()).toEqual(["changes"]);
    });

    // 窄窗口下失焦收起的工具窗在保存的布局里仍是「显示」：专注把它藏起再恢复时，
    // 恢复就是让它重新出现（与点图标显示同一口径），不能仍旧收着——否则两次按键都
    // 看不到任何变化。
    it("Given 窄窗口下大纲已失焦收起, When 进入专注再退出, Then 大纲重新出现（不再收着）", () => {
      useToolWindowLayout.getState().collapseNarrow("outline");
      expect(useToolWindowLayout.getState().narrowCollapsed.outline).toBe(true);

      useToolWindowLayout.getState().toggleFocusMode();
      useToolWindowLayout.getState().toggleFocusMode();

      expect(shownWindows()).toEqual(["outline"]);
      expect(
        useToolWindowLayout.getState().narrowCollapsed.outline,
      ).toBeUndefined();
    });

    it("Given 在专注里重启, When 读回后再按专注, Then 恢复进入专注之前的显示", async () => {
      useToolWindowLayout.getState().show("directory");
      useToolWindowLayout.getState().toggleFocusMode();
      // 重启：内存里的状态没了，只剩本机存下的布局。
      const saved = localStorage.getItem(TOOL_WINDOW_LAYOUT_KEY);
      resetToDefaults();
      localStorage.setItem(TOOL_WINDOW_LAYOUT_KEY, saved ?? "");
      await rehydrate();
      expect(shownWindows()).toEqual([]);

      useToolWindowLayout.getState().toggleFocusMode();
      expect(useToolWindowLayout.getState().shown).toEqual({
        rightTop: "outline",
        rightBottom: "directory",
        bottomLeft: null,
        bottomRight: null,
      });
    });

    it("Given 读回的专注快照非法, Then 逐项回落：坏槽位不显示，整份不可读时不在专注", async () => {
      persistLayout({
        shown: {
          rightTop: null,
          rightBottom: null,
          bottomLeft: null,
          bottomRight: null,
        },
        focusSnapshot: {
          rightTop: "outline",
          rightBottom: "nope",
          bottomLeft: "outline",
          bottomRight: null,
        },
      });
      await rehydrate();
      expect(useToolWindowLayout.getState().focusSnapshot).toEqual({
        rightTop: "outline",
        rightBottom: null,
        bottomLeft: null,
        bottomRight: null,
      });

      persistLayout({ focusSnapshot: "garbage" });
      await rehydrate();
      expect(useToolWindowLayout.getState().focusSnapshot).toBeNull();
    });
  });

  describe("最大化", () => {
    it("Given 大纲显示, When 最大化, Then 记为最大化；再来一次还原；隐藏它也会还原", () => {
      useToolWindowLayout.getState().toggleMaximize("outline");
      expect(useToolWindowLayout.getState().maximized).toBe("outline");
      useToolWindowLayout.getState().toggleMaximize("outline");
      expect(useToolWindowLayout.getState().maximized).toBeNull();

      useToolWindowLayout.getState().toggleMaximize("outline");
      useToolWindowLayout.getState().hide("outline");
      expect(useToolWindowLayout.getState().maximized).toBeNull();
    });

    it("Given 目录没显示, When 最大化目录, Then 什么都不变", () => {
      useToolWindowLayout.getState().toggleMaximize("directory");
      expect(useToolWindowLayout.getState().maximized).toBeNull();
    });
  });

  describe("恢复默认布局", () => {
    it("Given 改过槽位、模式、比例、浮动位置, When 恢复默认, Then 都回到默认，会话列表收起状态不动", () => {
      const s = useToolWindowLayout.getState();
      s.moveTo("outline", "bottomLeft");
      s.setMode("changes", "float");
      s.setRatio("right", 0.5);
      s.setFloatRect("changes", { x: 1, y: 2, width: 300, height: 300 });
      s.toggleSessionList();

      useToolWindowLayout.getState().restoreDefaults();
      const after = useToolWindowLayout.getState();
      expect(after.slotOf).toEqual(DEFAULT_TOOL_WINDOW_SLOTS);
      expect(after.mode).toEqual(DEFAULT_TOOL_WINDOW_MODES);
      expect(after.ratios).toEqual(DEFAULT_DIVIDER_RATIOS);
      expect(after.floatRect).toEqual({});
      expect(shownWindows()).toEqual(["outline"]);
      expect(after.sessionListCollapsed).toBe(true);
    });
  });

  describe("会话列表收起", () => {
    it("Given 默认展开, When 切换两次, Then 先收起再展开，并写进本机布局", () => {
      expect(useToolWindowLayout.getState().sessionListCollapsed).toBe(false);
      useToolWindowLayout.getState().toggleSessionList();
      expect(useToolWindowLayout.getState().sessionListCollapsed).toBe(true);
      expect(localStorage.getItem(TOOL_WINDOW_LAYOUT_KEY) ?? "").toContain(
        '"sessionListCollapsed":true',
      );
      useToolWindowLayout.getState().toggleSessionList();
      expect(useToolWindowLayout.getState().sessionListCollapsed).toBe(false);
    });
  });

  describe("新字段读回：非法或缺失时逐项回落", () => {
    it("Given 已存的模式、比例、浮动位置、会话列表收起, When 读回, Then 原样恢复", async () => {
      persistLayout({
        slotOf: DEFAULT_TOOL_WINDOW_SLOTS,
        shown: {
          rightTop: "outline",
          rightBottom: null,
          bottomLeft: null,
          bottomRight: null,
        },
        mode: { ...DEFAULT_TOOL_WINDOW_MODES, directory: "autoHide" },
        ratios: { right: 0.3, bottom: 0.4, rightSplit: 0.6, bottomSplit: 0.3 },
        floatRect: { changes: { x: 10, y: 20, width: 300, height: 360 } },
        sessionListCollapsed: true,
      });
      await rehydrate();
      const s = useToolWindowLayout.getState();
      expect(s.mode.directory).toBe("autoHide");
      expect(s.ratios).toEqual({
        right: 0.3,
        bottom: 0.4,
        rightSplit: 0.6,
        bottomSplit: 0.3,
      });
      expect(s.floatRect.changes).toEqual({
        x: 10,
        y: 20,
        width: 300,
        height: 360,
      });
      expect(s.sessionListCollapsed).toBe(true);
    });

    it("Given 一个工具窗的模式非法、另一个缺失, When 读回, Then 只有它们回落到固定", async () => {
      persistLayout({
        mode: { outline: "sideways", changes: "float" },
      });
      await rehydrate();
      const { mode } = useToolWindowLayout.getState();
      expect(mode.outline).toBe("pinned");
      expect(mode.changes).toBe("float");
      expect(mode.directory).toBe("pinned");
    });

    it("Given 一条比例越界、一条不是数字、一条缺失, When 读回, Then 只有这三条回落到默认", async () => {
      persistLayout({
        ratios: { right: 5, bottom: "wide", bottomSplit: 0.3 },
      });
      await rehydrate();
      expect(useToolWindowLayout.getState().ratios).toEqual({
        right: DEFAULT_DIVIDER_RATIOS.right,
        bottom: DEFAULT_DIVIDER_RATIOS.bottom,
        rightSplit: DEFAULT_DIVIDER_RATIOS.rightSplit,
        bottomSplit: 0.3,
      });
    });

    it("Given 一个浮动位置坏了、另一个好的、还有一个未知工具窗, When 读回, Then 只留下好的那个", async () => {
      persistLayout({
        floatRect: {
          changes: { x: "a", y: 0, width: 300, height: 300 },
          directory: { x: 5, y: 6, width: 300, height: 300 },
          bogus: { x: 1, y: 1, width: 300, height: 300 },
        },
      });
      await rehydrate();
      expect(useToolWindowLayout.getState().floatRect).toEqual({
        directory: { x: 5, y: 6, width: 300, height: 300 },
      });
    });

    it("Given 会话列表收起不是布尔值, When 读回, Then 回落到展开", async () => {
      persistLayout({ sessionListCollapsed: "yes" });
      await rehydrate();
      expect(useToolWindowLayout.getState().sessionListCollapsed).toBe(false);
    });

    it("Given 旧版布局（只有槽位与显示）, When 读回, Then 新字段全部取默认", async () => {
      persistLayout({
        slotOf: DEFAULT_TOOL_WINDOW_SLOTS,
        shown: {
          rightTop: "changes",
          rightBottom: null,
          bottomLeft: null,
          bottomRight: null,
        },
      });
      await rehydrate();
      const s = useToolWindowLayout.getState();
      expect(s.mode).toEqual(DEFAULT_TOOL_WINDOW_MODES);
      expect(s.ratios).toEqual(DEFAULT_DIVIDER_RATIOS);
      expect(s.floatRect).toEqual({});
      expect(s.sessionListCollapsed).toBe(false);
      expect(s.shown.rightTop).toBe("changes");
    });
  });
});
