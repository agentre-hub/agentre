import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import { ResizableSidebar } from "./resizable-sidebar";
import {
  SIDEBAR_DEFAULT_WIDTH,
  SIDEBAR_MAX_WIDTH,
  SIDEBAR_MIN_WIDTH,
  SIDEBAR_WIDTH_KEY_PREFIX,
} from "./sidebar-width-state";

/**
 * 可拖拽调宽的侧栏容器。
 *
 * 它原来只住在桌面宿主（`src/components/agentre/resizable-sidebar.tsx`），因为
 * 只有桌面端有会话索引那一列。`agentre-server` 的对话页现在也是「左列索引 +
 * 右栏详情」，同一个形态要在两端渲染，按跨端所有权规则它就得进包。
 *
 * 桌面那份从来没有组件级用例——拖拽逻辑（document 级监听、边的方向、结束才落盘）
 * 一直只靠 `sidebar-width-state` 的纯函数用例侧面覆盖。搬进包顺手把这几条钉住：
 * 它们各自对着一个真实会犯的错。
 */
describe("ResizableSidebar", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  function renderSidebar(
    props: Partial<React.ComponentProps<typeof ResizableSidebar>> = {},
  ) {
    return render(
      <ResizableSidebar persistenceKey="chat" ariaLabel="Sessions" {...props}>
        <p>list</p>
      </ResizableSidebar>,
    );
  }

  /** 拖拽发生在 document 上：离开手柄不该丢拖拽，所以移动/抬起都往 document 发。 */
  function drag(handle: Element, from: number, to: number) {
    fireEvent.pointerDown(handle, { button: 0, clientX: from });
    fireEvent(
      document,
      new MouseEvent("pointermove", { bubbles: true, clientX: to }),
    );
    fireEvent(document, new MouseEvent("pointerup", { bubbles: true }));
  }

  it("首屏用默认宽度，手柄报得出自己的量程", () => {
    renderSidebar();

    const aside = screen.getByRole("complementary", { name: "Sessions" });
    expect(aside.style.width).toBe(`${SIDEBAR_DEFAULT_WIDTH}px`);

    const handle = screen.getByRole("separator");
    expect(handle.getAttribute("aria-orientation")).toBe("vertical");
    expect(handle.getAttribute("aria-valuemin")).toBe(
      String(SIDEBAR_MIN_WIDTH),
    );
    expect(handle.getAttribute("aria-valuemax")).toBe(
      String(SIDEBAR_MAX_WIDTH),
    );
    expect(handle.getAttribute("aria-valuenow")).toBe(
      String(SIDEBAR_DEFAULT_WIDTH),
    );
    // 可访问名要说清「调的是谁」：一页上可能有两条这样的手柄。
    expect(handle.getAttribute("aria-label")).toContain("Sessions");
  });

  it("首屏读得回上次拖到的宽度", () => {
    localStorage.setItem(`${SIDEBAR_WIDTH_KEY_PREFIX}chat`, "420");
    renderSidebar();

    expect(
      screen.getByRole("complementary", { name: "Sessions" }).style.width,
    ).toBe("420px");
  });

  it("往右拖变宽，抬起时才落盘", () => {
    renderSidebar();
    const handle = screen.getByRole("separator");

    fireEvent.pointerDown(handle, { button: 0, clientX: 320 });
    fireEvent(
      document,
      new MouseEvent("pointermove", { bubbles: true, clientX: 380 }),
    );
    // 拖拽过程中宽度已经跟手，但还没打 localStorage：每 px 写一次是白费的。
    expect(
      screen.getByRole("complementary", { name: "Sessions" }).style.width,
    ).toBe("380px");
    expect(localStorage.getItem(`${SIDEBAR_WIDTH_KEY_PREFIX}chat`)).toBeNull();

    fireEvent(document, new MouseEvent("pointerup", { bubbles: true }));
    expect(localStorage.getItem(`${SIDEBAR_WIDTH_KEY_PREFIX}chat`)).toBe("380");
  });

  it("拖过头也停在量程边界上，不会把详情挤没", () => {
    renderSidebar();
    const handle = screen.getByRole("separator");
    const aside = screen.getByRole("complementary", { name: "Sessions" });

    drag(handle, 320, 4000);
    expect(aside.style.width).toBe(`${SIDEBAR_MAX_WIDTH}px`);

    drag(screen.getByRole("separator"), 0, -4000);
    expect(aside.style.width).toBe(`${SIDEBAR_MIN_WIDTH}px`);
  });

  it("右侧栏（edge=left）方向相反：往左拖才变宽", () => {
    renderSidebar({ edge: "left", defaultWidth: 300 });
    const aside = screen.getByRole("complementary", { name: "Sessions" });

    drag(screen.getByRole("separator"), 300, 250);
    expect(aside.style.width).toBe("350px");
  });

  it("非主键按下不起拖：右键菜单不该把侧栏拽走", () => {
    renderSidebar();
    const handle = screen.getByRole("separator");

    fireEvent.pointerDown(handle, { button: 2, clientX: 320 });
    fireEvent(
      document,
      new MouseEvent("pointermove", { bubbles: true, clientX: 500 }),
    );
    expect(
      screen.getByRole("complementary", { name: "Sessions" }).style.width,
    ).toBe(`${SIDEBAR_DEFAULT_WIDTH}px`);
  });

  it("宿主给得了 testId：两端的用例都得认得出这一列", () => {
    renderSidebar({ testId: "chat-list-col" });

    expect(screen.getByTestId("chat-list-col").tagName).toBe("ASIDE");
  });
});
