import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ProjectHeaderActions } from "../project/project-header-actions";
import type { ProjectHeaderActionsProps } from "../project/project-header-actions";

import { ImportLocalSessionMenu } from "./import-menu";

/**
 * 「导入本地会话…」的入口（规格 2026-08-26，决策 13）。
 *
 * 四条轴的组头共用**同一份**条目定义：项目组头把它插进自己那份菜单，其余三条轴
 * 用这一件独立的 ⋮。宿主没声明这个能力时**整条不出现**（沿用能力开关约定，不置灰）
 * —— 置灰在说「你以后可以」，而没有这个 port 的宿主永远不会有本地磁盘会话。
 */
function openMenu(el: HTMLElement) {
  fireEvent.pointerDown(el, { button: 0, ctrlKey: false });
}

describe("组头上的导入入口", () => {
  it("Given 宿主声明了导入能力, When 打开组头的 ⋮, Then 「导入本地会话…」在列且点它把入口预填交回宿主", async () => {
    const onImport = vi.fn();
    render(<ImportLocalSessionMenu onImport={onImport} label="Build box" />);

    openMenu(screen.getByTestId("import-menu-trigger"));
    const menu = await screen.findByRole("menu");
    const item = within(menu).getByTestId("import-menu-item");
    expect(item.textContent).toContain("Import local session");

    fireEvent.click(item);
    expect(onImport).toHaveBeenCalledTimes(1);
  });

  it("Given 宿主没有声明导入能力, When 组头渲染, Then 连 ⋮ 都不摆（整条不出现，而不是置灰）", () => {
    const { container } = render(<ImportLocalSessionMenu label="Build box" />);

    expect(container.innerHTML).toBe("");
    expect(screen.queryByTestId("import-menu-trigger")).toBeNull();
  });
});

/**
 * 项目组头本来就有一份 ⋮，导入这一条插进那份全集里 —— 与其余三条轴**同一份定义**
 * （同样的文案、同样的图标），不是各写一句自己的话。
 */
describe("项目组头那份菜单里的导入条目", () => {
  function projectProps(over: Partial<ProjectHeaderActionsProps> = {}) {
    return {
      projectId: "p1",
      projectName: "Atlas",
      unconfigured: false,
      loadMembers: vi.fn(async () => []),
      onNewChat: vi.fn(),
      onOpenSettings: vi.fn(),
      onNewSubproject: vi.fn(),
      onDelete: vi.fn(),
      ...over,
    } as ProjectHeaderActionsProps;
  }

  it("Given 宿主声明了导入能力, When 打开项目组头的 ⋮, Then 「导入本地会话…」是中间那一组的头一条、且与独立 ⋮ 同一句文案", async () => {
    const onImportLocalSession = vi.fn();
    render(
      <ProjectHeaderActions {...projectProps({ onImportLocalSession })} />,
    );
    fireEvent.pointerDown(screen.getByTestId("project-menu-p1"), {
      button: 0,
      ctrlKey: false,
    });
    const menu = await screen.findByRole("menu");
    const ids = Array.from(
      menu.querySelectorAll("[data-testid^='project-menu-item-']"),
    ).map((n) => n.getAttribute("data-testid"));
    // 「成员…」「机器与路径…」已经并回「项目设置…」（2026-08-27）：弹窗一屏放得下，
    // 菜单不再为它们各留一条深链。导入因此成了中间那一组的头一条。
    expect(ids).toEqual([
      "project-menu-item-settings",
      "project-menu-item-new-subproject",
      "project-menu-item-import-local-session",
      "project-menu-item-delete",
    ]);
    expect(
      within(menu).getByTestId("project-menu-item-import-local-session")
        .textContent,
    ).toContain("Import local session");

    fireEvent.click(
      within(menu).getByTestId("project-menu-item-import-local-session"),
    );
    expect(onImportLocalSession).toHaveBeenCalledWith("p1");
  });

  it("Given 宿主没有声明导入能力, When 打开项目组头的 ⋮, Then 那一条整条不出现", async () => {
    render(<ProjectHeaderActions {...projectProps()} />);
    fireEvent.pointerDown(screen.getByTestId("project-menu-p1"), {
      button: 0,
      ctrlKey: false,
    });
    const menu = await screen.findByRole("menu");
    expect(
      menu.querySelector(
        "[data-testid='project-menu-item-import-local-session']",
      ),
    ).toBeNull();
  });
});
