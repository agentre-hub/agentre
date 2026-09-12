// frontend/src/components/agentre/__tests__/sessions-popover.test.tsx
//
// 「查看全部 N」弹层的**翻页**行为。这里只测「下一页什么时候被拉」这一件事：
// 行的渲染、状态灯与筛选各有自己的家。
//
// 背景：弹层一次只拉 PAGE_SIZE 条（会话表可以有上万行，一次全取会把主进程
// 与渲染一起拖住），此前要看到第 21 条得手点一次「加载更多」。滚到底就该继续拉。
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Popover, PopoverAnchor } from "@agentre-hub/agentre-ui";
import { describe, expect, it, vi } from "vitest";

import { SessionsPopover, type SessionsPopoverPage } from "../sessions-popover";

/** 一页会话。id 连号，标题带上 offset，便于断言拉的是哪一页。 */
function page(offset: number, limit: number, total: number) {
  const end = Math.min(offset + limit, total);
  return {
    sessions: Array.from({ length: Math.max(0, end - offset) }, (_, i) => ({
      id: offset + i + 1,
      title: `session ${offset + i + 1}`,
      status: "idle",
      lastMessageAt: 0,
    })),
    total,
    hasMore: end < total,
  } satisfies SessionsPopoverPage;
}

function renderPopover(total: number) {
  const loader = vi.fn(({ offset, limit }: { offset: number; limit: number }) =>
    Promise.resolve(page(offset, limit, total)),
  );
  render(
    <Popover open>
      <PopoverAnchor />
      <SessionsPopover
        header={{ name: "Eng" }}
        loader={loader}
        onClose={() => {}}
        onSelectSession={() => {}}
      />
    </Popover>,
  );
  return loader;
}

/**
 * jsdom 不排版，滚动容器的三个尺寸都是 0 —— 不钉住它们，任何「到底了没」的判断
 * 都恒为真。这里把「已滚到底」这一刻手工摆出来。
 */
function scrollToBottom(el: HTMLElement) {
  Object.defineProperty(el, "scrollHeight", {
    value: 1000,
    configurable: true,
  });
  Object.defineProperty(el, "clientHeight", { value: 400, configurable: true });
  el.scrollTop = 600;
  fireEvent.scroll(el);
}

describe("SessionsPopover paging", () => {
  it("Given more sessions than one page, When the list is scrolled to the bottom, Then the next page is fetched and appended", async () => {
    const loader = renderPopover(44);
    expect(await screen.findByText("session 20")).toBeInTheDocument();
    expect(screen.queryByText("session 21")).toBeNull();

    scrollToBottom(screen.getByTestId("sessions-popover-list"));

    expect(await screen.findByText("session 21")).toBeInTheDocument();
    // 首屏 + 滚到底那一次，一次一页 —— 滚动不该退化成「把剩下的全取回来」。
    expect(loader).toHaveBeenCalledTimes(2);
    expect(loader).toHaveBeenLastCalledWith({ offset: 20, limit: 20 });
    // 第一页还在，追加而不是替换。
    expect(screen.getByText("session 1")).toBeInTheDocument();
  });

  it("Given the last page is loaded, When the list is scrolled to the bottom again, Then no further fetch is issued", async () => {
    const loader = renderPopover(20);
    expect(await screen.findByText("session 20")).toBeInTheDocument();

    scrollToBottom(screen.getByTestId("sessions-popover-list"));

    await waitFor(() => expect(loader).toHaveBeenCalledTimes(1));
  });

  it("Given a page is still in flight, When the list is scrolled again, Then the same page is not fetched twice", async () => {
    let release: (p: SessionsPopoverPage) => void = () => {};
    const loader = vi.fn(({ offset }: { offset: number; limit: number }) =>
      offset === 0
        ? Promise.resolve(page(0, 20, 44))
        : new Promise<SessionsPopoverPage>((resolve) => {
            release = resolve;
          }),
    );
    render(
      <Popover open>
        <PopoverAnchor />
        <SessionsPopover
          header={{ name: "Eng" }}
          loader={loader}
          onClose={() => {}}
          onSelectSession={() => {}}
        />
      </Popover>,
    );
    await screen.findByText("session 20");

    const list = screen.getByTestId("sessions-popover-list");
    scrollToBottom(list);
    scrollToBottom(list);

    await waitFor(() => expect(loader).toHaveBeenCalledTimes(2));
    release(page(20, 20, 44));
    expect(await screen.findByText("session 21")).toBeInTheDocument();
  });
});
