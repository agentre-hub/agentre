import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import * as dndSortable from "@dnd-kit/sortable";

import { TabStrip } from "../tab-strip";
import type { SessionMeta } from "@/stores/session-meta-store";
import { useSessionMetaStore } from "@/stores/session-meta-store";
import { useChatTabsStore } from "@/stores/chat-tabs-store";

describe("TabStrip", () => {
  beforeEach(() => {
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    useSessionMetaStore.getState().__reset();
  });

  beforeEach(() => {
    // jsdom 不实现 layout, dnd-kit 的 KeyboardSensor 需要 rect 才能 announce
    Element.prototype.getBoundingClientRect = vi.fn(() => ({
      x: 0,
      y: 0,
      width: 120,
      height: 38,
      top: 0,
      left: 0,
      right: 120,
      bottom: 38,
      toJSON: () => ({}),
    })) as never;
  });

  it("空 tabs 时不渲染 chevron 菜单", () => {
    render(<TabStrip />);
    expect(screen.queryByLabelText("Open Tab menu")).not.toBeInTheDocument();
  });

  it("有 tabs 时按 sortedTabs 顺序渲染", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    render(<TabStrip />);
    const tabs = screen.getAllByRole("tab");
    expect(tabs).toHaveLength(2);
  });

  it("有 tabs 时渲染 chevron", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    render(<TabStrip />);
    expect(screen.getByLabelText("Open Tab menu")).toBeInTheDocument();
  });

  it("Given many tabs, When tab strip overflows, Then it keeps a fixed height and only scrolls horizontally", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    render(<TabStrip />);

    const tablist = screen.getByRole("tablist");
    const scroller = tablist.firstElementChild;

    expect(tablist).toHaveClass("h-[38px]", "shrink-0", "overflow-hidden");
    expect(scroller).toHaveClass(
      "h-full",
      "min-h-0",
      "overflow-x-auto",
      "overflow-y-hidden",
    );
  });

  it("Given 一个活动 tab 与一个非活动 tab, When 渲染, Then Tab 条落在外框色上不画线，活动 tab 是顶带色、上沿圆角与内容页同为 12px、底部反向圆角流进顶带，没有品牌色条", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    render(<TabStrip />);

    const tablist = screen.getByRole("tablist");
    expect(tablist).toHaveClass("bg-chrome");
    expect(tablist.className).not.toMatch(/(^|\s)border(-[tblrxy])?(\s|$)/);
    expect(tablist.querySelector('[class*="border-"]')).toBeNull();

    const [first, second] = screen.getAllByRole("tab");
    const active = first.dataset.active === "true" ? first : second;
    const inactive = active === first ? second : first;
    expect(active).toHaveAttribute("data-active", "true");
    // 活动 tab 下面连着的是对话页顶带，不是转录：同色才连得上。
    expect(active).toHaveClass("bg-sheet-header", "rounded-t-xl", "tab-flare");
    expect(active).not.toHaveClass("bg-sheet");
    expect(active).not.toHaveClass("tab-hover-sheet");
    expect(active).not.toHaveClass("bg-background");
    expect(active.querySelector(".bg-primary")).toBeNull();
    expect(inactive).not.toHaveClass("bg-sheet", "bg-sheet-header");
    expect(inactive).not.toHaveClass("hover:bg-card");
    // 非活动 tab 的悬停底和活动 tab 同轮廓；不盖住活动 tab 的反向圆角由层叠保证
    // （见 tab-strip-layers.test.tsx）。
    expect(inactive).toHaveClass("tab-hover-sheet");
    expect(inactive).not.toHaveClass("tab-flare", "hover:bg-button-fill");
  });

  it("Given 第一个 tab 是活动的, When 渲染, Then Tab 条左侧让出内容页圆角 + 反向圆角（12 + 8 = 20px），反向圆角不落在内容页的圆角上", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    render(<TabStrip />);

    const scroller = screen.getByRole("tablist").firstElementChild;
    expect(scroller).toHaveClass("pl-5", "pr-2");
  });

  it("Tab tooltip/context trigger 使用真实布局盒作为浮窗锚点", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    render(<TabStrip />);

    const trigger = screen.getByRole("tab").parentElement;

    expect(trigger).toHaveClass("inline-flex");
    expect(trigger).not.toHaveClass("contents");
  });

  it("Given a tab in the clipped strip, When keyboard focus lands on it, Then the focus stop draws its ring inward and above the tab it wraps", () => {
    // 键盘停的不是 role="tab" 那一层，而是包在外面的可排序外壳（dnd-kit 给它
    // role="button" + tabIndex=0）。外壳填满会裁剪的标签条，默认向外的环三边被切掉
    // （真机上只剩右边一条竖线），所以环由外壳自己向内画。
    useChatTabsStore.getState().openSessionInNewTab(1);
    render(<TabStrip />);

    const tab = screen.getByRole("tab");
    const focusStop = tab.closest<HTMLElement>('[tabindex="0"]');

    expect(focusStop).not.toBeNull();
    expect(focusStop).not.toBe(tab);
    expect(focusStop).toHaveClass("focus-visible:focus-outline-inset");
    expect(focusStop).not.toHaveClass("focus-visible:focus-outline");
    // 向内的环落在外壳的盒子里，而里面的标签页是带底色的定位元素——定位的子元素画在
    // 父元素的 outline 之上，激活的标签页会把环整块盖住。标签页沉到 Tab 条这一层叠上下文
    // 的负层，顺序才是「标签页 → 环」；Tab 条不自成层叠上下文的话，标签页会掉到标签条
    // 底色后面。
    expect(screen.getByRole("tablist").firstElementChild).toHaveClass(
      "isolate",
    );
    expect(tab).toHaveClass("-z-[9]");
  });

  it("右键 Tab 弹出菜单, 含置顶/关闭/关闭其他/关闭右侧", async () => {
    const user = userEvent.setup();
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    render(<TabStrip />);
    const firstTab = screen.getAllByRole("tab")[0];
    await user.pointer({ keys: "[MouseRight]", target: firstTab });
    expect(screen.getByRole("menuitem", { name: /Pin/ })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Close" })).toBeInTheDocument();
    expect(
      screen.getByRole("menuitem", { name: "Close Others" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("menuitem", { name: "Close Tabs to the Right" }),
    ).toBeInTheDocument();
  });

  it("点菜单「置顶」: 目标 tab 翻 pinned", async () => {
    const user = userEvent.setup();
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    useSessionMetaStore.getState().setMeta(1, mkMeta("Session 1"));
    useSessionMetaStore.getState().setMeta(2, mkMeta("Session 2"));
    render(<TabStrip />);
    const targetTab = screen.getByRole("tab", { name: /Session 1/ });
    await user.pointer({ keys: "[MouseRight]", target: targetTab });
    await user.click(screen.getByRole("menuitem", { name: /Pin/ }));
    // 右键命中的 tab 在 store 里被 pin。
    const t = useChatTabsStore
      .getState()
      .tabs.find((x) => x.meta.kind === "session" && x.meta.sessionId === 1);
    expect(t?.isPinned).toBe(true);
  });

  it("点菜单「关闭其他」: 仅保留目标 tab", async () => {
    const user = userEvent.setup();
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    useChatTabsStore.getState().openSessionInNewTab(3);
    render(<TabStrip />);
    const secondTab = screen.getAllByRole("tab")[1];
    await user.pointer({ keys: "[MouseRight]", target: secondTab });
    await user.click(screen.getByRole("menuitem", { name: "Close Others" }));
    expect(useChatTabsStore.getState().tabs).toHaveLength(1);
  });

  it("最右 tab 的右键菜单, 「关闭右侧」被禁用", async () => {
    const user = userEvent.setup();
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    render(<TabStrip />);
    const lastTab = screen.getAllByRole("tab")[1];
    await user.pointer({ keys: "[MouseRight]", target: lastTab });
    expect(
      screen.getByRole("menuitem", { name: "Close Tabs to the Right" }),
    ).toHaveAttribute("data-disabled");
  });

  // NOTE: dnd-kit's KeyboardSensor does not fire onDragEnd in jsdom even with
  // getBoundingClientRect mocked — the sensor cannot resolve sibling positions
  // and silently no-ops the drop. Falling back to direct store-action assertions;
  // browser-level DnD interaction remains covered manually.
  it("键盘拖拽降级: moveTab 把第 1 个 tab 移到第 2 位, id 顺序翻转", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    const firstId = useChatTabsStore.getState().tabs[0].id;
    const secondId = useChatTabsStore.getState().tabs[1].id;
    render(<TabStrip />);

    // 直接调 store action 模拟拖拽完成；真实 DnD 由浏览器级验证覆盖。
    useChatTabsStore.getState().moveTab(0, 1);

    const ids = useChatTabsStore.getState().tabs.map((t) => t.id);
    expect(ids).toEqual([secondId, firstId]);
  });

  // Regression: TabTooltip(TooltipTrigger asChild) merges Radix Tooltip 自己的
  // onPointerDown 进入 SortableTab 的 props(...rest)。若在 ContextMenuTrigger 上
  // 把 {...listeners}({...rest} 之前)spread,rest.onPointerDown 会顶掉 dnd-kit
  // PointerSensor 的 onPointerDown,鼠标拖拽彻底失效。listeners 必须在 rest 之后
  // spread,确保 dnd-kit 的激活监听器赢。
  it("dnd-kit 的 onPointerDown 不被 Tooltip 注入的 onPointerDown 覆盖", () => {
    const pointerDownSpy = vi.fn();
    const real = dndSortable.useSortable;
    const spy = vi
      .spyOn(dndSortable, "useSortable")
      .mockImplementation((args) => {
        const r = real(args);
        return { ...r, listeners: { onPointerDown: pointerDownSpy } };
      });

    useChatTabsStore.getState().openSessionInNewTab(1);
    render(<TabStrip />);

    const tab = screen.getByRole("tab");
    fireEvent.pointerDown(tab);

    expect(pointerDownSpy).toHaveBeenCalled();
    spy.mockRestore();
  });

  it("拖拽后 store.tabs 顺序反映新位置", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    render(<TabStrip />);

    // 直接调 store action 模拟拖拽完成；真实 DnD 由浏览器级验证覆盖。
    useChatTabsStore.getState().moveTab(0, 1);

    expect(
      useChatTabsStore
        .getState()
        .tabs.map((t) => (t.meta as { sessionId: number }).sessionId),
    ).toEqual([2, 1]);
  });
});

function mkMeta(title: string): SessionMeta {
  return {
    agentId: 1,
    agentName: "Agent",
    agentColor: "agent-1",
    title,
    lastMessageAt: 0,
  };
}
