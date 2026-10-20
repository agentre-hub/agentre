import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useChatTabsStore } from "@/stores/chat-tabs-store";

import { TabStrip } from "../tab-strip";
import type { TabView } from "../use-tabs-view";

const views: { current: TabView[] } = { current: [] };
vi.mock("../use-tabs-view", () => ({ useTabsView: () => views.current }));

function view(id: string, over: Partial<TabView> = {}): TabView {
  return {
    id,
    title: `会话 ${id}`,
    kind: "session",
    avatar: { letter: "c", color: "var(--agent-1)" },
    isPreview: false,
    isPinned: false,
    status: "idle",
    projectColor: null,
    worktree: false,
    pillText: null,
    sessionId: Number(id.replace(/\D/g, "")) || 1,
    projectChain: null,
    worktreeBranch: null,
    lastMessageAt: 0,
    ...over,
  };
}

/** 第 i 个 Tab 的可排序外壳（键盘停的那一层）。 */
const wrapperOf = (tab: HTMLElement) => tab.parentElement as HTMLElement;
const lineOf = (tab: HTMLElement) =>
  wrapperOf(tab).querySelector<HTMLElement>('[data-testid="tab-project-line"]');

describe("TabStrip · 层叠、悬停面与项目色线", () => {
  beforeEach(() => {
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

  function renderStrip(list: TabView[], activeId: string) {
    views.current = list;
    useChatTabsStore.setState({ tabs: [], activeTabId: activeId });
    render(<TabStrip />);
    return screen.getAllByRole("tab");
  }

  it("Given 中间的 Tab 是活动的, When 渲染, Then 所有 Tab 共用 Tab 条这一个层叠上下文，活动 Tab 叠在相邻 Tab 之上", () => {
    const [left, active, right] = renderStrip(
      [view("t1"), view("t2"), view("t3")],
      "t2",
    );
    const scroller = screen.getByRole("tablist").firstElementChild;

    // 层叠上下文在 Tab 条上，不在每个外壳上：外壳各自成层时，后面那格的悬停底会
    // 画在前面活动 Tab 的反向圆角之上，把它盖掉。
    expect(scroller).toHaveClass("isolate");
    for (const tab of [left, active, right]) {
      expect(wrapperOf(tab)).not.toHaveClass("isolate");
      expect(wrapperOf(tab)).toHaveClass("relative");
    }
    expect(left).toHaveClass("-z-10");
    expect(right).toHaveClass("-z-10");
    expect(active).toHaveClass("-z-[9]");
    expect(active).not.toHaveClass("-z-10");
  });

  it("Given 非活动 Tab, When 渲染, Then 悬停面是和活动 Tab 同轮廓的那块，不再是内缩胶囊", () => {
    const [active, inactive] = renderStrip([view("t1"), view("t2")], "t1");

    expect(inactive).toHaveClass("tab-hover-sheet");
    expect(inactive).not.toHaveClass("tab-hover-pill");
    expect(active).not.toHaveClass("tab-hover-sheet");
  });

  it("Given 相邻 Tab 都带项目色、中间那个是活动的, When 渲染, Then 色线画在外壳上、叠在所有 Tab 之上，活动 Tab 底下也有，整段连续", () => {
    const [a, b, c] = renderStrip(
      [
        view("t1", { projectColor: "rgb(1, 2, 3)" }),
        view("t2", { projectColor: "rgb(4, 5, 6)" }),
        view("t3", { projectColor: "rgb(4, 5, 6)", worktree: true }),
      ],
      "t2",
    );

    for (const tab of [a, b, c]) {
      // 不在 role="tab" 里：那里面的东西出不了 Tab 自己的层，会被活动 Tab 的反向圆角咬掉。
      expect(tab.querySelector('[data-testid="tab-project-line"]')).toBeNull();
      const line = lineOf(tab);
      expect(line).not.toBeNull();
      expect(line).toHaveClass("absolute", "bottom-0", "inset-x-0", "-z-[8]");
    }
    expect(lineOf(a)).toHaveStyle({ backgroundColor: "rgb(1, 2, 3)" });
    expect(lineOf(b)).toHaveStyle({ backgroundColor: "rgb(4, 5, 6)" });
    expect(lineOf(c)).toHaveStyle({ opacity: "0.4" });
    // 只有整段的两头是圆头，中间接缝是直的。
    expect(lineOf(a)).toHaveClass("rounded-l-full");
    expect(lineOf(a)).not.toHaveClass("rounded-r-full");
    expect(lineOf(b)).not.toHaveClass("rounded-l-full", "rounded-r-full");
    expect(lineOf(c)).toHaveClass("rounded-r-full");
  });

  it("Given 中间夹着一个没有项目的 Tab, When 渲染, Then 它下面没有线，两侧的段在它这里各自收成圆头", () => {
    const [a, b, c] = renderStrip(
      [
        view("t1", { projectColor: "rgb(1, 2, 3)" }),
        view("t2"),
        view("t3", { projectColor: "rgb(1, 2, 3)" }),
      ],
      "t1",
    );

    expect(lineOf(b)).toBeNull();
    expect(lineOf(a)).toHaveClass("rounded-r-full");
    expect(lineOf(c)).toHaveClass("rounded-l-full");
  });
});
