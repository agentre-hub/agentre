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

  it("Given a selected row, When it renders, Then selection is not carried by color alone", () => {
    render(
      <SessionRow
        status="idle"
        title="session-1"
        trailingLabel="5m"
        href="/s/1"
        selected
      />,
    );

    // WCAG 1.4.1：选中不能只靠一种颜色成立。此前担这个责的是行左边一条 3px 竖条；
    // 竖条撤掉后由三样共同承担，这里钉后两样（第一样是 --sidebar-selected-bg 与
    // hover 面**反向**偏离静止面，由 tokens.test.ts 的「选中面比 hover 面更强」守）：
    // 标题字重，以及 aria-current。
    const link = screen.getByRole("link", { name: /session-1/ });
    expect(link.getAttribute("aria-current")).toBe("true");
    expect(screen.getByText("session-1").parentElement?.className).toMatch(
      /font-medium/,
    );
  });

  it("Given a selected row, When the pointer hovers it, Then the selected fill is not overridden by the generic hover fill", () => {
    render(
      <SessionRow
        status="idle"
        title="session-1"
        trailingLabel="5m"
        href="/s/1"
        selected
      />,
    );

    // 这条钉的是一个真实存在过的 bug：`hover:bg-sidebar-active-bg` 与 `bg-primary-soft`
    // 同时挂在行上时，前者在编译产物里排在后面且多一个伪类，级联上必胜——鼠标一停，
    // 选中底色整块消失，只剩那条竖条。竖条撤掉后这个洞会直接露出来，所以选中行
    // **不再发出**那条通用 hover 类，而不是靠调色去压它。
    const className = screen.getByRole("link", { name: /session-1/ }).className;
    expect(className).toMatch(/bg-sidebar-selected-bg/);
    expect(className).not.toMatch(/hover:bg-sidebar-active-bg/);
    expect(className).not.toMatch(/before:w-\[3px\]/);
  });

  it("Given a selected two-line row, When it renders, Then the quiet second line uses the muted role of the selected surface", () => {
    render(
      <SessionRow
        status="idle"
        title="session-1"
        trailingLabel="5m"
        href="/s/1"
        secondaryLabel="研究员 · 支付网关"
        selected
      />,
    );

    // --muted-foreground 是配 sidebar 调的，落到选中面上暗色只有 3.45。
    // 换了脚下的面，弱化文字就得换值（同 --status-*-text 的理由）。
    expect(screen.getByText("研究员 · 支付网关").className).toMatch(
      /text-sidebar-selected-muted/,
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

  it("Given a secondary label built from nodes, When the row renders, Then the host can hang glyphs on the second line", () => {
    // 桌面端「按时间」档的第二行是 `〔头像〕agent · 〔文件夹〕项目` —— 两维各自带
    // 着和其它档同一个字形。只收字符串的话宿主只能退回纯文字，同一条会话在三个
    // 档之间就长出三种样子。
    render(
      <SessionRow
        status="idle"
        title="session-1"
        trailingLabel="5m"
        secondaryLabel={
          <span data-testid="second-line">
            <span data-testid="agent-mark" aria-hidden="true" />
            设计师 · Agentre
          </span>
        }
      />,
    );

    expect(screen.getByTestId("agent-mark")).toBeTruthy();
    expect(screen.getByTestId("second-line").textContent).toContain("Agentre");
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

/**
 * 标题列的第三行。
 *
 * agentre-server 的移动端行是三行：Agent 名（自己一行，在标题**之上**）、状态点 +
 * 标题 + 时间、设备 · 后端。第一行不能用 `leading` —— 那是**行内**槽，在窄屏上会把
 * 标题挤没，而设计稿（48b / 屏 20）当初让它独占一行正是为了避免这件事。
 */
describe("SessionRow overline", () => {
  it("Given an overline, When the row renders, Then it sits above the title inside the title column, not inline with the status dot", () => {
    const { container } = render(
      <SessionRow
        status="idle"
        title="session-1"
        overline={<span data-testid="agent-line">后端 Agent</span>}
        secondaryLabel="调试机 · claude_code"
      />,
    );

    const agentLine = screen.getByTestId("agent-line");
    const title = screen.getByText("session-1");
    // 同一个标题列里，且排在标题前面。
    expect(agentLine.parentElement).toBe(title.parentElement);
    expect(
      agentLine.compareDocumentPosition(title) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    // 行内槽是另一个位置：overline 不该跑到状态点旁边去。
    const dot = container.querySelector('[aria-label$="status"]');
    expect(dot?.nextElementSibling).not.toBe(agentLine);
  });

  it("Given all three lines, When the row renders, Then they read overline → title → secondary in order", () => {
    render(
      <SessionRow
        status="idle"
        title="session-1"
        overline="后端 Agent"
        secondaryLabel="调试机 · claude_code"
      />,
    );

    const column = screen.getByText("session-1").parentElement!;
    expect(column.textContent).toBe(
      "后端 Agent" + "session-1" + "调试机 · claude_code",
    );
  });

  it("Given no overline, When the row renders, Then no empty line is reserved", () => {
    render(<SessionRow status="idle" title="session-1" />);

    expect(screen.getByText("session-1").parentElement!.childElementCount).toBe(
      1,
    );
  });
});
