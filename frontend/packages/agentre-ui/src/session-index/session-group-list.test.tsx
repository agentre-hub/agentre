import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { SessionGroupList } from "./session-group-list";

/**
 * 组列表容器。
 *
 * 它是控制区与组集合之间的**唯一列表边界**：控制区到整份列表的间距归宿主，列表内
 * 相邻顶层组之间不额外加纵向间距。此前 server 宿主把 12px 的控制区间距当作组间距
 * 重复落在每个空组之间，desktop 与 server 因此可能对「连续空组有多密」各说各的。
 *
 * 容器只接 `children` 与 `className`：它不判断有哪些组、不排序、不画页面级空态，
 * 也不认识 DnD —— 那些是宿主的产品动作。
 */
describe("SessionGroupList", () => {
  it("Given host-projected children, When the list renders, Then it renders exactly them without taking over the group structure", () => {
    render(
      <SessionGroupList>
        <article data-testid="group-a" />
        <article data-testid="group-b" />
      </SessionGroupList>,
    );

    const list = document.querySelector('[data-slot="session-group-list"]');
    expect(list).not.toBeNull();
    expect(list!.contains(screen.getByTestId("group-a"))).toBe(true);
    expect(list!.contains(screen.getByTestId("group-b"))).toBe(true);
    expect(list!.children).toHaveLength(2);
  });

  it("Given adjacent groups, When the list renders, Then the container adds no vertical spacing between them (the control area's spacing must not repeat per group)", () => {
    render(
      <SessionGroupList>
        <article data-testid="group-a" />
        <article data-testid="group-b" />
      </SessionGroupList>,
    );

    const list = document.querySelector(
      '[data-slot="session-group-list"]',
    ) as HTMLElement;
    expect(list.className).toContain("flex-col");
    expect(list.className).not.toMatch(/(^|\s)gap-/);
    expect(list.className).not.toMatch(/space-y-/);
  });

  it("Given a host className, When the list renders, Then the host keeps its scroll and padding on the same boundary", () => {
    render(
      <SessionGroupList className="min-h-0 flex-1 overflow-auto px-2 py-3">
        <article />
      </SessionGroupList>,
    );

    const list = document.querySelector(
      '[data-slot="session-group-list"]',
    ) as HTMLElement;
    expect(list.className).toContain("min-h-0");
    expect(list.className).toContain("flex-1");
    expect(list.className).toContain("overflow-auto");
    expect(list.className).toContain("px-2");
    expect(list.className).toContain("py-3");
  });
});
