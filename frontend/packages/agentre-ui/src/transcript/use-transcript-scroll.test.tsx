/**
 * use-transcript-scroll.test.tsx —— 转录滚动几何与效果组的包内用例。
 *
 * 这些行为原本长在桌面宿主的 chat-panel.tsx 里,搬进包时逐字保留语义;宿主那边
 * chat-panel.test.tsx 仍以整面板为口径盯着同一批场景(等价基线)。这里只用一个
 * 最小宿主壳(Harness)把 hook 单独钉住,不牵扯 Wails / store / 路由。
 */

import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  COLLAPSED_RESTORE_GUARD_MS,
  __resetChatPanelScrollStateForTesting,
  computeTopVisibleAnchor,
  isCollapsedBelowGuard,
  isTranscriptAtBottom,
  loadTranscriptScrollState,
  readScrollMetrics,
} from "./chat-panel-scroll-state";
import type { TurnMessage } from "./transcript-turns";
import { useTranscriptScroll } from "./use-transcript-scroll";

afterEach(() => {
  __resetChatPanelScrollStateForTesting();
});

// ─── 纯几何 ────────────────────────────────────────────────────────────────

describe("readScrollMetrics / isTranscriptAtBottom / isCollapsedBelowGuard", () => {
  function metricsEl(
    clientHeight: number,
    scrollHeight: number,
    scrollTop: number,
  ): HTMLElement {
    return { clientHeight, scrollHeight, scrollTop } as unknown as HTMLElement;
  }

  it("Given a scroller, Then maxScrollTop is the non-negative slack between content and viewport", () => {
    expect(readScrollMetrics(metricsEl(480, 4_000, 300))).toEqual({
      clientHeight: 480,
      maxScrollTop: 3_520,
      scrollHeight: 4_000,
      scrollTop: 300,
    });
    // 内容比视口还矮(虚拟化高度塌陷时会这样)→ 不允许出负数
    expect(readScrollMetrics(metricsEl(480, 200, 0)).maxScrollTop).toBe(0);
  });

  it("Given the distance to the bottom, Then 32px is the inclusive at-bottom tolerance", () => {
    expect(
      isTranscriptAtBottom(readScrollMetrics(metricsEl(480, 4_000, 3_488))),
    ).toBe(true);
    expect(
      isTranscriptAtBottom(readScrollMetrics(metricsEl(480, 4_000, 3_487))),
    ).toBe(false);
  });

  it("Given a collapsed-restore guard, Then a below-guard height counts only until the deadline passes", () => {
    const guard = {
      atBottom: true,
      key: "tab",
      minMaxScrollTop: 7_880,
      scrollTop: 7_912,
      until: Date.now() + 10_000,
    };
    const collapsed = readScrollMetrics(metricsEl(480, 1_096, 896));
    const recovered = readScrollMetrics(metricsEl(480, 8_392, 896));
    expect(isCollapsedBelowGuard(collapsed, guard)).toBe(true);
    expect(isCollapsedBelowGuard(recovered, guard)).toBe(false);
    expect(
      isCollapsedBelowGuard(collapsed, { ...guard, until: Date.now() - 1 }),
    ).toBe(false);
  });
});

describe("computeTopVisibleAnchor", () => {
  function fakeRow(id: string, top: number, bottom: number): HTMLElement {
    return {
      getAttribute: (name: string) => (name === "data-message-id" ? id : null),
      getBoundingClientRect: () => ({ top, bottom }) as DOMRect,
    } as unknown as HTMLElement;
  }
  function fakeContainer(top: number, rows: HTMLElement[]): HTMLElement {
    return {
      getBoundingClientRect: () => ({ top }) as DOMRect,
      querySelectorAll: () => rows as unknown as NodeListOf<HTMLElement>,
    } as unknown as HTMLElement;
  }

  it("Given rows straddling the viewport top, Then it anchors to the first row whose bottom crosses the top and records the overscroll px", () => {
    const el = fakeContainer(100, [
      fakeRow("1", 0, 50), // 完全在视口上方 (bottom 50 ≤ 100) → 跳过
      fakeRow("2", 60, 140), // 第一条底边越过视口顶 → 命中
      fakeRow("3", 140, 300),
    ]);
    expect(computeTopVisibleAnchor(el)).toEqual({
      anchorId: 2,
      anchorOffset: 40,
    });
  });

  it("Given the top-visible row starts below the viewport top, Then anchorOffset clamps to 0", () => {
    const el = fakeContainer(100, [fakeRow("7", 120, 300)]);
    expect(computeTopVisibleAnchor(el)).toEqual({
      anchorId: 7,
      anchorOffset: 0,
    });
  });

  it("Given rows carry data-row-key, Then the anchor includes the row key for row-precise restore", () => {
    // 行级虚拟化下一条长消息会拆成多行;只记 anchorId 的话,恢复会塌到消息首行,
    // 偏差可达整条消息的高度。data-row-key 让恢复端精确钉回同一行。
    const row = {
      getAttribute: (name: string) =>
        name === "data-message-id"
          ? "1"
          : name === "data-row-key"
            ? "message:1:tool:tool:toolu-120"
            : null,
      getBoundingClientRect: () => ({ top: 60, bottom: 140 }) as DOMRect,
    } as unknown as HTMLElement;
    expect(computeTopVisibleAnchor(fakeContainer(100, [row]))).toEqual({
      anchorId: 1,
      anchorOffset: 40,
      anchorRowKey: "message:1:tool:tool:toolu-120",
    });
  });

  it("Given no message rows, Then it returns null", () => {
    expect(computeTopVisibleAnchor(fakeContainer(100, []))).toBeNull();
  });

  it("Given every row sits entirely above the viewport top, Then it returns null", () => {
    const el = fakeContainer(100, [fakeRow("1", 0, 40), fakeRow("2", 40, 90)]);
    expect(computeTopVisibleAnchor(el)).toBeNull();
  });
});

// ─── hook ─────────────────────────────────────────────────────────────────

const CLIENT_HEIGHT = 480;

// user/assistant 交替 —— countTurnsAfterMessage 按 user 开轮计数。
// 常量而非每次现造:effect 依赖里有 messages,新数组身份会让效果每渲染都重跑。
const MESSAGES: TurnMessage[] = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10].map((id) => ({
  blocks: [],
  id,
  role: id % 2 === 1 ? "user" : "assistant",
}));

type HarnessProps = {
  active?: boolean;
  liveRevision?: unknown;
  messages?: TurnMessage[];
  sessionId?: number;
  tabKey?: string;
};

function Harness(props: HarnessProps) {
  const messages = props.messages ?? MESSAGES;
  const {
    followBottom,
    onScroll,
    scrollToBottom,
    setScrollElement,
    showBackToBottom,
    turnsBelow,
  } = useTranscriptScroll({
    active: props.active ?? true,
    liveRevision: props.liveRevision,
    messages,
    sessionId: props.sessionId ?? 42,
    tabKey: props.tabKey,
  });
  return (
    <section data-testid="scroller" onScroll={onScroll} ref={setScrollElement}>
      {messages.map((m) => (
        <article data-message-id={m.id} key={m.id} />
      ))}
      {/* 「发送」那条强制跟随的入口:宿主在 send/compact/重跑/编辑 里调它。 */}
      <button data-testid="force-follow" onClick={followBottom} type="button">
        force-follow
      </button>
      {showBackToBottom ? (
        <button onClick={scrollToBottom} type="button">
          back-to-bottom · {turnsBelow}
        </button>
      ) : null}
    </section>
  );
}

function scroller(
  container: HTMLElement,
  scrollHeight: () => number = () => 4_000,
): HTMLElement {
  const el = container.querySelector("section");
  if (!el) throw new Error("scroller not found");
  Object.defineProperty(el, "clientHeight", {
    configurable: true,
    get: () => CLIENT_HEIGHT,
  });
  Object.defineProperty(el, "scrollHeight", {
    configurable: true,
    get: scrollHeight,
  });
  // happy-dom 不排版,所有 rect 都是 0 —— 顶部锚点算法会因此认为每行都在视口
  // 上方。给容器与消息行铺一套确定的几何:容器占 [0, 480),第 n 行占 [100n, 100n+100)
  // —— 于是视口顶那条是 id 1,视口下沿那条是 id 5(id 6 的顶边 500 已越过 480)。
  el.getBoundingClientRect = () =>
    ({ bottom: CLIENT_HEIGHT, top: 0 }) as DOMRect;
  el.querySelectorAll<HTMLElement>("[data-message-id]").forEach((row, i) => {
    row.getBoundingClientRect = () =>
      ({ bottom: i * 100 + 100, top: i * 100 }) as DOMRect;
  });
  return el as HTMLElement;
}

function pill(): HTMLElement {
  return screen.getByRole("button", { name: /back-to-bottom/ });
}

function queryPill(): HTMLElement | null {
  return screen.queryByRole("button", { name: /back-to-bottom/ });
}

describe("useTranscriptScroll", () => {
  it("Given the user scrolls up, Then it saves an anchored snapshot, raises the back-to-bottom pill and counts the turns below", () => {
    const view = render(<Harness tabKey="tab-a" />);
    const el = scroller(view.container);

    act(() => {
      el.scrollTop = 3_520;
      fireEvent.scroll(el);
    });
    expect(queryPill()).toBeNull();

    act(() => {
      el.scrollTop = 300;
      fireEvent.scroll(el);
    });

    expect(loadTranscriptScrollState("tab-a")).toEqual({
      anchorId: 1,
      anchorOffset: 0,
      atBottom: false,
      scrollTop: 300,
    });
    expect(pill().textContent).toContain("back-to-bottom · 2");
  });

  it("Given the pill is up, When it is clicked, Then it returns to the real bottom and stands down", () => {
    const view = render(<Harness tabKey="tab-b" />);
    const el = scroller(view.container);

    act(() => {
      el.scrollTop = 3_520;
      fireEvent.scroll(el);
    });
    act(() => {
      el.scrollTop = 300;
      fireEvent.scroll(el);
    });
    fireEvent.click(pill());

    expect(el.scrollTop).toBe(3_520);
    expect(queryPill()).toBeNull();
    expect(loadTranscriptScrollState("tab-b")).toEqual({
      atBottom: true,
      scrollTop: 3_520,
    });
  });

  it("Given a send forces follow, Then the pill stands down without a scroll event", () => {
    const view = render(<Harness tabKey="tab-send" />);
    const el = scroller(view.container);
    act(() => {
      el.scrollTop = 3_520;
      fireEvent.scroll(el);
    });
    act(() => {
      el.scrollTop = 300;
      fireEvent.scroll(el);
    });
    expect(pill()).toBeTruthy();

    fireEvent.click(screen.getByTestId("force-follow"));
    expect(queryPill()).toBeNull();
  });

  it("Given content grew past the at-bottom tolerance, When a streaming increment lands, Then the follow intent survives and it pins to the freshly measured bottom", () => {
    // 位置式 atBottom 在流式增长快过滚动时会掉出 32px 容差;跟随意图必须免疫 ——
    // 否则整轮转录区冻结、最新输出沉到折叠线下(回归 bug)。这里刻意让最后一次滚动
    // 落在「非贴底且是往下滚」上:位置说不贴底,意图必须仍为真。
    let height = 4_000;
    const view = render(<Harness liveRevision={0} tabKey="tab-follow" />);
    const el = scroller(view.container, () => height);

    act(() => {
      el.scrollTop = 3_520; // 贴底
      fireEvent.scroll(el);
    });
    act(() => {
      height = 9_000; // 流式内容把底部推远
      el.scrollTop = 4_000; // 往下滚了一点,但离底还有 4_520px
      fireEvent.scroll(el);
    });
    expect(loadTranscriptScrollState("tab-follow")?.atBottom).toBe(false);

    act(() => {
      view.rerender(<Harness liveRevision={1} tabKey="tab-follow" />);
    });

    expect(el.scrollTop).toBe(8_520);
  });

  it("Given the user scrolled up, When a streaming increment lands, Then it does not yank the view back to the bottom", () => {
    let height = 4_000;
    const view = render(<Harness liveRevision={0} tabKey="tab-nofollow" />);
    const el = scroller(view.container, () => height);

    act(() => {
      el.scrollTop = 3_520;
      fireEvent.scroll(el);
    });
    act(() => {
      el.scrollTop = 300;
      fireEvent.scroll(el);
    });

    act(() => {
      height = 9_000;
      view.rerender(<Harness liveRevision={1} tabKey="tab-nofollow" />);
    });

    expect(el.scrollTop).toBe(300);
  });

  it("Given a tab resumes while the virtualized height is collapsed, Then it suppresses paint, ignores the collapsed scroll and restores once the height recovers", async () => {
    let height = 8_392;
    const view = render(<Harness tabKey="tab-collapse" />);
    const el = scroller(view.container, () => height);

    act(() => {
      el.scrollTop = 7_912;
      fireEvent.scroll(el);
    });
    expect(loadTranscriptScrollState("tab-collapse")).toEqual({
      atBottom: true,
      scrollTop: 7_912,
    });

    view.rerender(<Harness active={false} tabKey="tab-collapse" />);
    act(() => {
      height = 1_096;
      el.scrollTop = 0;
    });
    view.rerender(<Harness tabKey="tab-collapse" />);

    expect(el.style.visibility).toBe("hidden");

    // 塌陷高度上的滚动事件不许覆盖存好的位置。
    act(() => {
      el.scrollTop = 896;
      fireEvent.scroll(el);
    });
    expect(loadTranscriptScrollState("tab-collapse")).toEqual({
      atBottom: true,
      scrollTop: 7_912,
    });

    act(() => {
      height = 8_392;
    });
    await waitFor(() => {
      expect(el.scrollTop).toBe(7_912);
    });
    expect(el.style.visibility).toBe("");
  });

  it("Given the transcript is suppressed while requestAnimationFrame never fires, When the guard deadline elapses, Then it stops suppressing without waiting for a user scroll", async () => {
    // 窗口被遮挡 / 不在前台时 WebView 会整段停掉 rAF(本地实测 Chromium 停过 6.4s),
    // 而 setTimeout 只被钳到 ~1s。期限只在 rAF 循环里被比较时,转录区就会无限期
    // 停在 visibility:hidden,只有用户滚一下才解除。
    vi.useFakeTimers();
    let height = 8_392;
    const view = render(<Harness tabKey="tab-raf-starved" />);
    const el = scroller(view.container, () => height);

    act(() => {
      el.scrollTop = 7_912;
      fireEvent.scroll(el);
    });

    vi.stubGlobal("requestAnimationFrame", () => 1);
    vi.stubGlobal("cancelAnimationFrame", () => {});

    view.rerender(<Harness active={false} tabKey="tab-raf-starved" />);
    act(() => {
      height = 200;
      el.scrollTop = 0;
    });
    view.rerender(<Harness tabKey="tab-raf-starved" />);

    expect(el.style.visibility).toBe("hidden");

    await act(async () => {
      await vi.advanceTimersByTimeAsync(COLLAPSED_RESTORE_GUARD_MS + 100);
    });

    expect(el.style.visibility).toBe("");
    vi.clearAllTimers();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("Given a saved non-bottom snapshot with an anchor, When the panel remounts, Then it hands the anchor to the transcript instead of restoring raw pixels", () => {
    const view = render(<Harness tabKey="tab-anchor" />);
    const el = scroller(view.container);
    act(() => {
      el.scrollTop = 3_520;
      fireEvent.scroll(el);
    });
    act(() => {
      el.scrollTop = 300;
      fireEvent.scroll(el);
    });
    view.unmount();

    const anchors: Array<[number, number, string | undefined]> = [];
    function AnchorHarness() {
      const { setScrollElement } = useTranscriptScroll({
        active: true,
        messages: MESSAGES,
        scrollToAnchor: (id, offset, rowKey) => {
          anchors.push([id, offset, rowKey]);
          return true;
        },
        sessionId: 42,
        tabKey: "tab-anchor",
      });
      return <section data-testid="scroller" ref={setScrollElement} />;
    }
    render(<AnchorHarness />);

    expect(anchors).toEqual([[1, 0, undefined]]);
  });

  it("Given a different tab key, When the same session opens in another tab, Then the old tab position is not restored", () => {
    const first = render(<Harness tabKey="tab-1" />);
    const el = scroller(first.container);
    act(() => {
      el.scrollTop = 3_520;
      fireEvent.scroll(el);
    });
    act(() => {
      el.scrollTop = 1_240;
      fireEvent.scroll(el);
    });
    first.unmount();

    const second = render(<Harness tabKey="tab-2" />);
    expect(scroller(second.container).scrollTop).toBe(0);
  });
});
