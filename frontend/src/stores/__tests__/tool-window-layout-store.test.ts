import { beforeEach, describe, expect, it } from "vitest";

import { useChatSidebarStore } from "../chat-sidebar-store";
import {
  DEFAULT_TOOL_WINDOW_SLOTS,
  TOOL_WINDOW_IDS,
  TOOL_WINDOW_LAYOUT_KEY,
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
});
