import * as React from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type * as DndCore from "@dnd-kit/core";
import * as dndSortable from "@dnd-kit/sortable";

import { TabStrip } from "../tab-strip";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useSessionMetaStore } from "@/stores/session-meta-store";

// 拖拽时的几何由 dnd-kit 在真实布局里算，happy-dom 跑不出一次真拖拽；这里抓住 Tab 条
// 交给 DndContext 的配置（modifiers、拖拽回调），在它们的边界上断言行为。
const dndProps: { current: DndCore.DndContextProps | null } = {
  current: null,
};
const overlayProps: {
  current: React.ComponentProps<typeof DndCore.DragOverlay> | null;
} = { current: null };
vi.mock("@dnd-kit/core", async (importOriginal) => {
  const actual = await importOriginal<typeof DndCore>();
  function RecordingDndContext(props: DndCore.DndContextProps) {
    dndProps.current = props;
    return React.createElement(actual.DndContext, props);
  }
  function RecordingDragOverlay(
    props: React.ComponentProps<typeof actual.DragOverlay>,
  ) {
    overlayProps.current = props;
    return React.createElement(actual.DragOverlay, props);
  }
  return {
    ...actual,
    DndContext: RecordingDndContext,
    DragOverlay: RecordingDragOverlay,
  };
});

// 数每个 Tab 本体渲染了几次：拖拽每一帧 dnd-kit 都会让所有可排序外壳重渲染，Tab 本体
// （连同它的右键菜单）在拖拽里不变，不该跟着重来——Tab 一多，拿起和拖动就开始掉帧。
const tabRenders = vi.hoisted(() => ({ count: 0 }));
vi.mock("../tab", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../tab")>();
  const CountingTab = React.forwardRef<
    HTMLDivElement,
    React.ComponentProps<typeof actual.Tab>
  >(function CountingTab(props, ref) {
    tabRenders.count += 1;
    return React.createElement(actual.Tab, { ...props, ref });
  });
  return { ...actual, Tab: CountingTab };
});

function dnd(): DndCore.DndContextProps {
  if (!dndProps.current) throw new Error("TabStrip did not render DndContext");
  return dndProps.current;
}

/** happy-dom 没有布局；dnd-kit 拿起时要量各格的矩形。 */
function mockLayout() {
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
}

/** 键盘拿起（dnd-kit 的 KeyboardSensor：在外壳上按空格）。 */
function pickUp(shell: HTMLElement) {
  fireEvent.keyDown(shell, { key: " ", code: "Space" });
}

function meta(title: string) {
  return {
    agentId: 1,
    agentName: "Agent",
    agentColor: "agent-1",
    title,
    lastMessageAt: 0,
  };
}

function hoverCard() {
  return document.querySelector('[data-slot="tab-hover-card"]');
}

describe("TabStrip 拖拽排序", () => {
  beforeEach(() => {
    dndProps.current = null;
    overlayProps.current = null;
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    useSessionMetaStore.getState().__reset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("Given 被拖的 Tab 落在一个更窄的 Tab 上（dnd-kit 给出 scaleX 0.5）, When 渲染, Then 外壳只平移、不被压扁", () => {
    const real = dndSortable.useSortable;
    vi.spyOn(dndSortable, "useSortable").mockImplementation((args) => ({
      ...real(args),
      transform: { x: 30, y: 0, scaleX: 0.5, scaleY: 1 },
    }));
    useChatTabsStore.getState().openSessionInNewTab(1);
    render(<TabStrip />);

    const shell = screen.getByRole("tab").parentElement as HTMLElement;

    expect(shell.style.transform).toBe("translate3d(30px, 0px, 0)");
  });

  it("Given 拿起一个 Tab, When 拖着它, Then 它的一份拷贝浮在浮层那一级跟着走（压在所有 Tab 之上），原位置只留一个看不见的空位", () => {
    mockLayout();
    useSessionMetaStore.getState().setMeta(1, meta("dragged one"));
    for (const sid of [1, 2]) {
      useChatTabsStore.getState().openSessionInNewTab(sid);
    }
    render(<TabStrip />);
    const shell = screen.getAllByRole("tab")[0].parentElement as HTMLElement;

    pickUp(shell);

    const preview = document.querySelector<HTMLElement>(
      '[data-testid="tab-drag-preview"]',
    );
    expect(preview).not.toBeNull();
    expect(preview).toHaveAttribute("aria-hidden", "true");
    expect(preview).toHaveTextContent("dragged one");
    expect(preview?.closest("[role=tablist]")).toBeNull();
    expect(overlayProps.current?.zIndex).toBe(50);
    expect(shell.style.opacity).toBe("0");
  });

  it("Given 拖着 Tab, When 松手, Then 拷贝用 200ms ease-out 滑进它的落点", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    render(<TabStrip />);

    expect(overlayProps.current?.dropAnimation).toMatchObject({
      duration: 200,
      easing: "ease-out",
    });
  });

  it("Given 系统设置了减少动态效果, When 松手, Then 直接落位，不播滑入动画", () => {
    vi.spyOn(window, "matchMedia").mockImplementation(
      (query: string) =>
        ({
          matches: query === "(prefers-reduced-motion: reduce)",
          media: query,
          addEventListener: () => {},
          removeEventListener: () => {},
        }) as unknown as MediaQueryList,
    );
    useChatTabsStore.getState().openSessionInNewTab(1);
    render(<TabStrip />);

    expect(overlayProps.current?.dropAnimation).toBeNull();
  });

  it("Given 一排 Tab, When 拿起其中一个并拖动, Then Tab 条里的 Tab 本体都不重渲染，浮着的拷贝只在拿起时渲染一次", () => {
    mockLayout();
    for (const sid of [1, 2, 3]) {
      useChatTabsStore.getState().openSessionInNewTab(sid);
    }
    render(<TabStrip />);
    const shell = screen.getAllByRole("tab")[0].parentElement as HTMLElement;
    tabRenders.count = 0;

    pickUp(shell);
    fireEvent.keyDown(document, { key: "ArrowRight", code: "ArrowRight" });
    fireEvent.keyDown(document, { key: "ArrowRight", code: "ArrowRight" });

    expect(shell).toHaveAttribute("aria-pressed", "true");
    expect(tabRenders.count).toBe(1);
  });

  it("Given 拖着 Tab, When 指针纵向偏离 Tab 条, Then 被拖的 Tab 只沿 Tab 条横向移动", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    render(<TabStrip />);

    const moved = (dnd().modifiers ?? []).reduce(
      (transform, modifier) =>
        modifier({ transform } as Parameters<DndCore.Modifier>[0]),
      { x: 40, y: 23, scaleX: 1, scaleY: 1 },
    );

    expect(moved).toEqual({ x: 40, y: 0, scaleX: 1, scaleY: 1 });
  });

  it("Given 正在拖 Tab, When 指针扫过其他 Tab, Then 悬停卡不弹出；放下之后悬停照常弹出", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    render(<TabStrip />);
    const [first, second] = screen.getAllByRole("tab");
    const firstId = useChatTabsStore.getState().tabs[0].id;
    const active = { id: firstId } as DndCore.Active;

    act(() => dnd().onDragStart?.({ active } as DndCore.DragStartEvent));
    await user.hover(second);
    await act(() => new Promise((r) => setTimeout(r, 600)));

    expect(hoverCard()).toBeNull();

    act(() =>
      dnd().onDragEnd?.({
        active,
        over: null,
      } as unknown as DndCore.DragEndEvent),
    );
    await user.unhover(second);
    await user.hover(first);
    await act(() => new Promise((r) => setTimeout(r, 600)));

    expect(hoverCard()).not.toBeNull();
  });
});
