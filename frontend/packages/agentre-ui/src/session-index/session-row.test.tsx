import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { SessionGroup } from "./session-group";
import { SessionRow } from "./session-row";
import type { SessionRowModel } from "./types";

/**
 * 行的**导航接缝**。
 *
 * 桌面端点一行是「开一个标签页」——没有地址可言，所以行是 `<button>`。浏览器宿主
 * （agentre-server）不一样：会话有 URL，行必须是真链接，否则中键 / ⌘ 点击 /
 * 右键「复制链接地址」会**静默**失效——点得动，只是少了三件事，不会有任何报错。
 *
 * 包刻意不收 react-router-dom（见 boundary.test.ts：跳转是外壳能力），所以 SPA 宿主
 * 通过 `renderLink` 把自己的 `<Link>` 注进来；不给就退化成原生 `<a>`。
 */
function row(
  id: number,
  extra: Partial<SessionRowModel> = {},
): SessionRowModel {
  return {
    id: String(id),
    status: "idle",
    title: `session-${id}`,
    trailingLabel: "5m ago",
    ...extra,
  };
}

describe("SessionRow navigation seam", () => {
  it("Given no href, When the row renders, Then it stays a button (desktop opens a tab, there is no address)", () => {
    render(<SessionRow status="idle" title="session-1" trailingLabel="5m" />);

    expect(screen.getByRole("button", { name: /session-1/ })).toBeTruthy();
    expect(screen.queryByRole("link")).toBeNull();
  });

  it("Given an href and no renderLink, When the row renders, Then it is a real anchor carrying that href", () => {
    render(
      <SessionRow
        status="idle"
        title="session-1"
        trailingLabel="5m"
        href="/devices/7/sessions/1"
      />,
    );

    const link = screen.getByRole("link", { name: /session-1/ });
    expect(link.tagName).toBe("A");
    expect(link).toHaveAttribute("href", "/devices/7/sessions/1");
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("Given a host renderLink, When the row renders, Then the host element is used instead of a bare anchor (SPA navigation is not a full page load)", () => {
    const renderLink = vi.fn(({ href, className, children }) => (
      <a data-testid="host-link" data-to={href} className={className}>
        {children}
      </a>
    ));

    render(
      <SessionRow
        status="idle"
        title="session-1"
        trailingLabel="5m"
        href="/devices/7/sessions/1"
        renderLink={renderLink}
      />,
    );

    const link = screen.getByTestId("host-link");
    expect(link).toHaveAttribute("data-to", "/devices/7/sessions/1");
    // 行自己的样式仍然由包给出——宿主只负责「用什么元素跳」，不负责长什么样。
    expect(link.className).toContain("rounded-md");
    expect(screen.getByText("session-1")).toBeTruthy();
    expect(screen.getByText("5m")).toBeTruthy();
  });

  it("Given a selected row rendered as a link, When it renders, Then aria-current survives the link path", () => {
    render(
      <SessionRow
        status="idle"
        title="session-1"
        trailingLabel="5m"
        href="/s/1"
        selected
      />,
    );

    expect(screen.getByRole("link", { name: /session-1/ })).toHaveAttribute(
      "aria-current",
      "true",
    );
  });

  it("Given a collapsed group, When its link rows render, Then they are not reachable by keyboard (a link has no disabled attribute to lean on)", () => {
    render(
      <SessionGroup
        defaultExpanded={false}
        sessions={[row(1, { href: "/s/1" })]}
        renderHeader={() => <div data-testid="header" />}
      />,
    );

    const link = screen.getByText("session-1").closest("a");
    expect(link).not.toBeNull();
    expect(link).toHaveAttribute("aria-disabled", "true");
    expect(link).toHaveAttribute("tabindex", "-1");
  });

  it("Given rows carrying href inside a group, When a renderLink is supplied to the group, Then every row goes through it", () => {
    const renderLink = vi.fn(({ href, children, className }) => (
      <a data-testid={`host-link-${href}`} href={href} className={className}>
        {children}
      </a>
    ));

    render(
      <SessionGroup
        defaultExpanded
        sessions={[row(1, { href: "/s/1" }), row(2, { href: "/s/2" })]}
        renderLink={renderLink}
        renderHeader={() => <div data-testid="header" />}
      />,
    );

    expect(screen.getByTestId("host-link-/s/1")).toBeTruthy();
    expect(screen.getByTestId("host-link-/s/2")).toBeTruthy();
  });
});

describe("SessionRow slots", () => {
  it("Given a leading node, When the row renders, Then it sits between the status dot and the title", () => {
    render(
      <SessionRow
        status="idle"
        title="session-1"
        trailingLabel="5m"
        leading={<span data-testid="leading" />}
      />,
    );

    const button = screen.getByRole("button", { name: /session-1/ });
    const children = [...button.children];
    const leadingIndex = children.findIndex(
      (el) => el.getAttribute("data-testid") === "leading",
    );
    expect(leadingIndex).toBe(1);
  });

  it("Given no leading node, When the row renders, Then no empty slot is reserved (the other two axes must not gain a phantom gap)", () => {
    render(<SessionRow status="idle" title="session-1" trailingLabel="5m" />);

    expect(
      screen.getByRole("button", { name: /session-1/ }).children,
    ).toHaveLength(3);
  });

  it("Given a secondary label, When the row renders, Then it becomes a two-line row carrying both dimensions", () => {
    render(
      <SessionRow
        status="idle"
        title="session-1"
        trailingLabel="5m"
        secondaryLabel="设计师 · Agentre"
      />,
    );

    expect(screen.getByText("设计师 · Agentre")).toBeTruthy();
    expect(screen.getByText("session-1")).toBeTruthy();
  });

  it("Given a secondary label on a link row, When it renders, Then the link still carries both lines", () => {
    render(
      <SessionRow
        status="idle"
        title="session-1"
        trailingLabel="5m"
        secondaryLabel="row two"
        href="/s/1"
      />,
    );

    const link = screen.getByRole("link", { name: /session-1/ });
    expect(link.textContent).toContain("row two");
  });
});

/**
 * 行尾的两个插槽。**它们是两个而不是一个，原因是硬的**：`<button>` 不能嵌在
 * `<a>` 里（HTML 不允许，浏览器行为也说不清），而 agentre-server 的行尾同时有
 * 「相对时间」这种可以在链接里的东西，和「关注开关」这种必须在链接外的按钮。
 *
 * 所以按**位置**分：`trailing` 在链接/按钮内（跟着行一起跳转），`rowActions` 在它外
 * （自己是可交互元素）。
 */
describe("SessionRow trailing slots", () => {
  it("Given a trailing node on a link row, When it renders, Then it sits inside the link (clicking it navigates like the rest of the row)", () => {
    render(
      <SessionRow
        status="idle"
        title="session-1"
        href="/s/1"
        trailing={<time dateTime="2026-08-17T00:00:00Z">5m ago</time>}
      />,
    );

    const link = screen.getByRole("link", { name: /session-1/ });
    expect(link.querySelector("time")).not.toBeNull();
  });

  it("Given row actions, When the row renders, Then they sit OUTSIDE the link — a button nested in an anchor is invalid HTML", () => {
    render(
      <SessionRow
        status="idle"
        title="session-1"
        href="/s/1"
        rowActions={
          <button type="button" aria-label="Follow">
            ★
          </button>
        }
      />,
    );

    const link = screen.getByRole("link", { name: /session-1/ });
    const follow = screen.getByRole("button", { name: "Follow" });
    expect(link.contains(follow)).toBe(false);
    expect(follow).toBeTruthy();
  });

  it("Given neither slot, When the row renders, Then the DOM keeps its old single-element shape (no wrapper for hosts that never asked for one)", () => {
    const { container } = render(
      <SessionRow
        status="idle"
        title="session-1"
        trailingLabel="5m"
        href="/s/1"
      />,
    );

    // 没有 rowActions 就不该多一层 flex 容器 —— 桌面端的行是 SessionGroup 直接
    // 排列的，凭空多一层会改变它的间距。
    expect(container.firstElementChild?.tagName).toBe("A");
  });

  it("Given no trailingLabel, When the row renders, Then no empty label span is emitted", () => {
    const { container } = render(
      <SessionRow status="idle" title="session-1" />,
    );

    expect(container.textContent).toBe("session-1");
  });
});
