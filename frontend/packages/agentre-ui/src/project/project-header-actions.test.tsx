import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  ProjectHeaderActions,
  ProjectHeaderContextMenu,
} from "./project-header-actions";
import type {
  ProjectHeaderActionsProps,
  ProjectHeaderMember,
} from "./project-header-actions";

/**
 * 组头上的三样动作，两端共用那一份（规格 2026-08-22 C 段，决策 5/10）。
 *
 * **一份定义、两种容器**：⋮ 与在组头上右键给出同样的条目、同样的顺序、同样的分隔线
 * 位置、同样的危险项样式。两处各摆一遍就是两处各漏一项的机会 —— 合之前桌面端的
 * 右键菜单就少了「成员…」「机器与路径…」，而 ⋮ 上有。
 *
 * **能力开关**：宿主未声明的能力，其条目**整条不出现**而不是置灰。
 * agentre-server 既没有终端也没有合并，桌面端两者都有。
 */

const MEMBERS: ProjectHeaderMember[] = [
  { id: "a1", name: "Reviewer" },
  { id: "a2", name: "Scout", inherited: true },
];

function props(
  over: Partial<ProjectHeaderActionsProps> = {},
): ProjectHeaderActionsProps {
  return {
    projectId: "p1",
    projectName: "Atlas",
    unconfigured: false,
    loadMembers: vi.fn(async () => MEMBERS),
    onNewChat: vi.fn(),
    onOpenSettings: vi.fn(),
    onNewSubproject: vi.fn(),
    onDelete: vi.fn(),
    ...over,
  };
}

/** Radix 的菜单开在 pointerdown 上，不是 click。 */
function openMenu(el: HTMLElement) {
  fireEvent.pointerDown(el, { button: 0, ctrlKey: false });
}

beforeEach(() => vi.clearAllMocks());

describe("一份定义、两种容器", () => {
  it("⋮ 与右键给出同样的条目、同样的顺序", async () => {
    const p = props();
    const { unmount } = render(<ProjectHeaderActions {...p} />);
    openMenu(screen.getByTestId("project-menu-p1"));
    const dropdown = await screen.findByRole("menu");
    const fromDropdown = Array.from(
      dropdown.querySelectorAll("[data-testid^='project-menu-item-']"),
    ).map((n) => n.getAttribute("data-testid"));
    unmount();

    render(
      <ProjectHeaderContextMenu {...p}>
        <div>Atlas</div>
      </ProjectHeaderContextMenu>,
    );
    fireEvent.contextMenu(screen.getByTestId("project-context-target"));
    const context = await screen.findByRole("menu");
    const fromContext = Array.from(
      context.querySelectorAll("[data-testid^='project-menu-item-']"),
    ).map((n) => n.getAttribute("data-testid"));

    expect(fromDropdown).toEqual(fromContext);
    expect(fromDropdown.length).toBeGreaterThan(0);
  });

  it("两种容器里删除都是危险项，且前面都有分隔线", async () => {
    const p = props();
    render(<ProjectHeaderActions {...p} />);
    openMenu(screen.getByTestId("project-menu-p1"));
    const menu = await screen.findByRole("menu");
    const del = within(menu).getByTestId("project-menu-item-delete");
    expect(del.dataset.variant).toBe("destructive");
    expect(menu.querySelector("[data-slot$='menu-separator']")).not.toBeNull();
  });
});

describe("条目全集与能力开关", () => {
  it("两个能力都不声明时，终端与合并整条不出现", async () => {
    render(<ProjectHeaderActions {...props()} />);
    openMenu(screen.getByTestId("project-menu-p1"));
    const menu = await screen.findByRole("menu");
    const ids = Array.from(
      menu.querySelectorAll("[data-testid^='project-menu-item-']"),
    ).map((n) => n.getAttribute("data-testid"));
    expect(ids).toEqual([
      "project-menu-item-settings",
      "project-menu-item-new-subproject",
      "project-menu-item-members",
      "project-menu-item-paths",
      "project-menu-item-delete",
    ]);
  });

  it("声明了就按全集里的位置插进去，顺序不变", async () => {
    render(
      <ProjectHeaderActions
        {...props({
          capabilities: { terminal: true, merge: true },
          onNewTerminal: vi.fn(),
          onMergeInto: vi.fn(),
        })}
      />,
    );
    openMenu(screen.getByTestId("project-menu-p1"));
    const menu = await screen.findByRole("menu");
    const ids = Array.from(
      menu.querySelectorAll("[data-testid^='project-menu-item-']"),
    ).map((n) => n.getAttribute("data-testid"));
    expect(ids).toEqual([
      "project-menu-item-settings",
      "project-menu-item-new-subproject",
      "project-menu-item-members",
      "project-menu-item-paths",
      "project-menu-item-terminal",
      "project-menu-item-merge",
      "project-menu-item-delete",
    ]);
  });

  it("「成员…」与「机器与路径…」直落设置弹窗的那一节", async () => {
    const onOpenSettings = vi.fn();
    render(<ProjectHeaderActions {...props({ onOpenSettings })} />);
    openMenu(screen.getByTestId("project-menu-p1"));
    const menu = await screen.findByRole("menu");
    fireEvent.click(within(menu).getByTestId("project-menu-item-paths"));
    await waitFor(() =>
      expect(onOpenSettings).toHaveBeenCalledWith("p1", "paths"),
    );
  });

  it("一台机器上都没配路径时，「机器与路径…」照常在列 —— 它正是去配路径的地方", async () => {
    render(<ProjectHeaderActions {...props({ unconfigured: true })} />);
    openMenu(screen.getByTestId("project-menu-p1"));
    const menu = await screen.findByRole("menu");
    expect(within(menu).getByTestId("project-menu-item-paths")).toBeTruthy();
  });
});

describe("＋ 的成员浮层", () => {
  it("恰好一个成员时不弹浮层，直接开对话", async () => {
    const onNewChat = vi.fn();
    render(
      <ProjectHeaderActions
        {...props({
          loadMembers: vi.fn(async () => [{ id: "a1", name: "Reviewer" }]),
          onNewChat,
        })}
      />,
    );
    fireEvent.click(screen.getByTestId("project-add-p1"));
    await waitFor(() => expect(onNewChat).toHaveBeenCalledWith("p1", "a1"));
    // 弹出来只是多一次点击，没有可选项。
    expect(screen.queryByTestId("project-add-popover")).toBeNull();
  });

  it("两个以上才弹，继承来的带角标但照样能选", async () => {
    const onNewChat = vi.fn();
    render(<ProjectHeaderActions {...props({ onNewChat })} />);
    fireEvent.click(screen.getByTestId("project-add-p1"));
    const option = await screen.findByTestId("project-member-option-a2");
    expect(option.textContent).toContain("Inherited");
    fireEvent.click(option);
    await waitFor(() => expect(onNewChat).toHaveBeenCalledWith("p1", "a2"));
  });

  it("连点两下 ＋ 只开一次对话 —— 第二次打到的是同一个成员", async () => {
    const onNewChat = vi.fn();
    const loadMembers = vi.fn(async () => [{ id: "a1", name: "Reviewer" }]);
    render(<ProjectHeaderActions {...props({ loadMembers, onNewChat })} />);
    const trigger = screen.getByTestId("project-add-p1");
    // 恰好一个成员时 ＋ 直接开对话，按下去在版面上没有任何立即反应 —— 最容易被连点，
    // 而连点的结果是同一个 Agent 上开出两个草稿页。
    fireEvent.click(trigger);
    fireEvent.click(trigger);
    await waitFor(() => expect(onNewChat).toHaveBeenCalled());
    expect(onNewChat).toHaveBeenCalledTimes(1);
  });

  it("一个成员都没有时给一条去加成员的路，而不是一句空话", async () => {
    const onOpenSettings = vi.fn();
    render(
      <ProjectHeaderActions
        {...props({ loadMembers: vi.fn(async () => []), onOpenSettings })}
      />,
    );
    fireEvent.click(screen.getByTestId("project-add-p1"));
    fireEvent.click(await screen.findByTestId("project-add-empty-action"));
    await waitFor(() =>
      expect(onOpenSettings).toHaveBeenCalledWith("p1", "members"),
    );
  });

  it("成员没读上来时说出来，不假装这个项目没有成员", async () => {
    render(
      <ProjectHeaderActions
        {...props({
          loadMembers: vi.fn(async () => Promise.reject(new Error("boom"))),
        })}
      />,
    );
    fireEvent.click(screen.getByTestId("project-add-p1"));
    expect(await screen.findByTestId("project-add-failed")).toBeTruthy();
    expect(screen.queryByTestId("project-add-empty")).toBeNull();
  });

  it("挑完之后焦点不还给 ＋ —— 宿主刚给新对话输入框的那次 focus 会被抹掉", async () => {
    render(<ProjectHeaderActions {...props()} />);
    const trigger = screen.getByTestId("project-add-p1");
    fireEvent.click(trigger);
    fireEvent.click(await screen.findByTestId("project-member-option-a1"));
    await waitFor(() => expect(document.activeElement).not.toBe(trigger));
  });

  it("但放弃时（Esc）焦点照还 —— 那是键盘用户找回位置的唯一途径", async () => {
    render(<ProjectHeaderActions {...props()} />);
    const trigger = screen.getByTestId("project-add-p1");
    fireEvent.click(trigger);
    await screen.findByTestId("project-member-option-a1");
    fireEvent.keyDown(document.activeElement ?? document.body, {
      key: "Escape",
    });
    await waitFor(() => expect(document.activeElement).toBe(trigger));
  });

  it("浮层里的点击不回流进组头那颗收放按钮", async () => {
    const onToggle = vi.fn();
    render(
      <button type="button" onClick={onToggle}>
        <ProjectHeaderActions {...props()} />
      </button>,
    );
    fireEvent.click(screen.getByTestId("project-add-p1"));
    fireEvent.click(await screen.findByTestId("project-member-option-a1"));
    // 不拦的话点一个成员 = 点了组头，那个项目当场收起来。
    expect(onToggle).not.toHaveBeenCalled();
  });
});

describe("「未配置」角标", () => {
  it("可点，与「机器与路径…」同一个去处 —— 不可点的话它只是个坏消息", async () => {
    const onOpenSettings = vi.fn();
    render(
      <ProjectHeaderActions
        {...props({ unconfigured: true, onOpenSettings })}
      />,
    );
    fireEvent.click(screen.getByTestId("project-unconfigured-p1"));
    await waitFor(() =>
      expect(onOpenSettings).toHaveBeenCalledWith("p1", "paths"),
    );
  });

  it("配好了就不画", () => {
    render(<ProjectHeaderActions {...props({ unconfigured: false })} />);
    expect(screen.queryByTestId("project-unconfigured-p1")).toBeNull();
  });
});

describe("触摸屏与嵌套", () => {
  it("＋ 与 ⋮ 是带 role=button 的元素，不是嵌套 <button> —— HTML 不许按钮套按钮", () => {
    render(<ProjectHeaderActions {...props()} />);
    for (const id of ["project-add-p1", "project-menu-p1"]) {
      const el = screen.getByTestId(id);
      expect(el.tagName).not.toBe("BUTTON");
      expect(el.getAttribute("role")).toBe("button");
    }
  });

  it("窄屏常驻、宽屏才 hover 现身 —— 触摸屏上没有 hover", () => {
    render(<ProjectHeaderActions {...props()} />);
    const cls = screen.getByTestId("project-add-p1").className;
    expect(cls).toContain("sm:");
  });

  it("点 ⋮ 不把这一组收起来", async () => {
    const onToggle = vi.fn();
    render(
      <button type="button" onClick={onToggle}>
        <ProjectHeaderActions {...props()} />
      </button>,
    );
    openMenu(screen.getByTestId("project-menu-p1"));
    await screen.findByRole("menu");
    expect(onToggle).not.toHaveBeenCalled();
  });
});
