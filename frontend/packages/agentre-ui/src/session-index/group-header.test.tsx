import type * as React from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import {
  IndexGroupHeader,
  groupActionRevealClassName,
  groupActionRevealTouchClassName,
  groupGlyphClassName,
} from "./group-header";

/** 每个用例都要给的那几个必填项，省得每处重复写一遍。 */
function renderHeader(
  props: Partial<React.ComponentProps<typeof IndexGroupHeader>> = {},
) {
  return render(
    <IndexGroupHeader
      expanded
      onToggle={vi.fn()}
      label="Acme"
      testId="group-header"
      {...props}
    />,
  );
}

describe("IndexGroupHeader", () => {
  it("字形槽的尺寸只有一条阶梯：根 24px、子 16px、再往下 14px", () => {
    expect(groupGlyphClassName(0)).toContain("size-6");
    expect(groupGlyphClassName(1)).toContain("size-4");
    expect(groupGlyphClassName(2)).toContain("size-3.5");
    expect(groupGlyphClassName(5)).toContain("size-3.5");
  });

  /**
   * 回归（2026-08-22）：宿主手抄显形类里的组名，组头换外壳时就断了——agentre-server
   * 的 ＋/⋮ 在组头切进共享包后因 `group-hover/header` 对不上 `group/group-header`
   * 永远隐身。所以显形类从这儿导出、和标记同源；守卫也按**配对**写：常量里的组名
   * 必须是外壳真挂出来的那一枚，改名才不会静默分家。
   */
  it("导出的两档显形类与外壳挂的组名配得上对——分家就永远隐身", () => {
    renderHeader();
    const root = screen.getByTestId("group-header");
    const marker = root.className.match(/group\/([\w-]+)/)?.[1];
    expect(marker).toBeTruthy();
    for (const cls of [
      groupActionRevealClassName,
      groupActionRevealTouchClassName,
    ]) {
      expect(cls).toContain(`group-hover/${marker}:opacity-100`);
    }
    // 两档各自的门槛也说清楚：桌面形态光标不在就藏，触屏形态窄屏常驻、sm 起才藏。
    expect(groupActionRevealClassName).toContain("opacity-0");
    expect(groupActionRevealTouchClassName).toContain(
      "sm:group-hover/group-header:opacity-100",
    );
  });

  it("字形是渲染函数：那一档的尺寸类由外壳给，调用方只说画什么", () => {
    renderHeader({
      glyph: (className) => <span data-testid="glyph" className={className} />,
    });

    expect(screen.getByTestId("glyph").className).toContain("size-6");
  });

  it("深一层的组头换一整套尺码：字形、字号、chevron、内边距同时降档", () => {
    const { unmount } = renderHeader({
      depth: 0,
      glyph: (className) => <span data-testid="glyph" className={className} />,
    });
    expect(screen.getByTestId("glyph").className).toContain("size-6");
    expect(screen.getByTestId("group-header-label").className).toContain(
      "text-prose",
    );
    expect(
      screen.getByTestId("group-header-chevron").getAttribute("class"),
    ).toContain("size-3.5");
    unmount();

    renderHeader({
      depth: 1,
      glyph: (className) => <span data-testid="glyph" className={className} />,
    });
    expect(screen.getByTestId("glyph").className).toContain("size-4");
    expect(screen.getByTestId("group-header-label").className).toContain(
      "uppercase",
    );
    expect(
      screen.getByTestId("group-header-chevron").getAttribute("class"),
    ).toContain("size-3");
  });

  it("收起时 chevron 转 90°，aria-expanded 跟着说话", async () => {
    const user = userEvent.setup();
    const onToggle = vi.fn();
    renderHeader({ expanded: false, onToggle });

    const toggle = screen.getByRole("button", { expanded: false });
    expect(
      screen.getByTestId("group-header-chevron").getAttribute("class"),
    ).toContain("-rotate-90");

    await user.click(toggle);
    expect(onToggle).toHaveBeenCalledTimes(1);
  });

  it("动作在折叠按钮**外面**：button 不能嵌 button，点它也不该顺手折起这一组", async () => {
    const user = userEvent.setup();
    const onToggle = vi.fn();
    const onAct = vi.fn();
    renderHeader({
      onToggle,
      actions: (
        <button type="button" aria-label="More" onClick={onAct}>
          ⋮
        </button>
      ),
    });

    const action = screen.getByRole("button", { name: "More" });
    const toggle = screen.getByRole("button", { expanded: true });
    expect(toggle.contains(action)).toBe(false);

    await user.click(action);
    expect(onAct).toHaveBeenCalledTimes(1);
    expect(onToggle).not.toHaveBeenCalled();
  });

  it("角标跟着名字走，在折叠按钮里面 —— 它说的是这一组本身的事", () => {
    renderHeader({ badges: <span data-testid="badge">offline</span> });

    const toggle = screen.getByRole("button", { expanded: true });
    expect(toggle.contains(screen.getByTestId("badge"))).toBe(true);
  });

  it("需要关注的条数按最强档着色；0 条时连圆点都不摆", () => {
    const { unmount } = renderHeader({
      attentionCount: 2,
      attentionTone: "error",
    });
    const mark = screen.getByTestId("group-header-attention");
    expect(mark).toHaveTextContent("2");
    expect(mark.className).toContain("text-status-error");
    expect(mark.querySelector("span")?.className).toContain("bg-status-error");
    unmount();

    renderHeader({ attentionCount: 0, attentionTone: null });
    expect(screen.queryByTestId("group-header-attention")).toBeNull();
  });

  it("名字置灰是一档颜色，不是一种残废：组头照样展得开", async () => {
    const user = userEvent.setup();
    const onToggle = vi.fn();
    renderHeader({ labelMuted: true, onToggle });

    expect(screen.getByTestId("group-header-label").className).toContain(
      "text-muted-foreground",
    );
    await user.click(screen.getByRole("button", { expanded: true }));
    expect(onToggle).toHaveBeenCalledTimes(1);
  });
});
