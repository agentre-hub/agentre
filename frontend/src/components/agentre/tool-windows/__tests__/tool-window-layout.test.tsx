import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { FolderTree, GitCompare, ListTree, Terminal } from "lucide-react";
import { beforeEach, describe, expect, it } from "vitest";

import { TooltipProvider } from "@agentre-hub/agentre-ui";
import {
  DEFAULT_TOOL_WINDOW_SLOTS,
  useToolWindowLayout,
} from "@/stores/tool-window-layout-store";

import { ToolWindowLayout } from "../tool-window-layout";
import type { ToolWindowDefinition } from "../types";

function windows(changesCount: number | null = 3): ToolWindowDefinition[] {
  return [
    {
      id: "outline",
      label: "Outline",
      icon: ListTree,
      content: <div>outline body</div>,
    },
    {
      id: "changes",
      label: "Changes",
      icon: GitCompare,
      count: changesCount,
      content: <div>changes body</div>,
    },
    {
      id: "directory",
      label: "Directory",
      icon: FolderTree,
      content: <div>directory body</div>,
    },
  ];
}

function renderLayout(
  defs: ToolWindowDefinition[] = windows(),
  hidden = false,
) {
  return render(
    <TooltipProvider>
      <ToolWindowLayout windows={defs} hidden={hidden}>
        <main>editor</main>
      </ToolWindowLayout>
    </TooltipProvider>,
  );
}

function strip() {
  return screen.getByRole("toolbar", { name: "Tool windows" });
}

function stripButton(name: string) {
  return within(strip()).getByRole("button", { name });
}

describe("ToolWindowLayout", () => {
  beforeEach(() => {
    useToolWindowLayout.setState({
      slotOf: { ...DEFAULT_TOOL_WINDOW_SLOTS },
      shown: {
        rightTop: "outline",
        rightBottom: null,
        bottomLeft: null,
        bottomRight: null,
      },
      focusRequest: null,
    });
  });

  describe("图标条", () => {
    it("Given 三个已注册的工具窗, When 渲染, Then 图标条是竖向工具栏，按槽位分组：右上一组（大纲、变更）、右下一组（目录）", () => {
      renderLayout();

      const toolbar = strip();
      expect(toolbar).toHaveAttribute("aria-orientation", "vertical");
      const groups = within(toolbar).getAllByRole("group");
      expect(groups).toHaveLength(2);
      expect(
        within(groups[0])
          .getAllByRole("button")
          .map((b) => b.getAttribute("aria-label")),
      ).toEqual(["Outline", "Changes"]);
      expect(
        within(groups[1])
          .getAllByRole("button")
          .map((b) => b.getAttribute("aria-label")),
      ).toEqual(["Directory"]);
    });

    it("Given 布局里有七个工具窗但只注册了三个, When 渲染, Then 图标条只出现已注册的那三个", () => {
      renderLayout();
      expect(within(strip()).getAllByRole("button")).toHaveLength(3);
    });

    it("Given 已注册底部的工具窗, When 渲染, Then 它落在图标条下端自己的一组", () => {
      renderLayout([
        ...windows(),
        {
          id: "terminal",
          label: "Terminal",
          icon: Terminal,
          content: <div>terminal body</div>,
        },
      ]);
      const groups = within(strip()).getAllByRole("group");
      expect(groups).toHaveLength(3);
      expect(
        within(groups[2]).getByRole("button", { name: "Terminal" }),
      ).toBeInTheDocument();
    });

    it("Given 每个图标, Then 它是报告是否已打开的切换按钮", () => {
      renderLayout();
      expect(stripButton("Outline")).toHaveAttribute("aria-pressed", "true");
      expect(stripButton("Changes")).toHaveAttribute("aria-pressed", "false");
      expect(stripButton("Directory")).toHaveAttribute("aria-pressed", "false");
    });

    it("Given 目录未显示, When 点目录图标, Then 目录作为带名称的区域出现并拿到焦点，图标转为已打开", async () => {
      const user = userEvent.setup();
      renderLayout();

      await user.click(stripButton("Directory"));

      const region = screen.getByRole("region", { name: "Directory" });
      expect(region).toHaveTextContent("directory body");
      expect(region).toHaveFocus();
      expect(stripButton("Directory")).toHaveAttribute("aria-pressed", "true");
      // 不同槽位，大纲仍在。
      expect(
        screen.getByRole("region", { name: "Outline" }),
      ).toBeInTheDocument();
    });

    it("Given 大纲在右上显示, When 点同槽位的变更图标, Then 变更替换大纲", async () => {
      const user = userEvent.setup();
      renderLayout();

      await user.click(stripButton("Changes"));

      expect(screen.queryByRole("region", { name: "Outline" })).toBeNull();
      expect(
        screen.getByRole("region", { name: /^Changes/ }),
      ).toBeInTheDocument();
      expect(stripButton("Outline")).toHaveAttribute("aria-pressed", "false");
    });

    it("Given 大纲已显示, When 再点大纲图标, Then 它被隐藏，没有工具窗时右侧区域整个收起", async () => {
      const user = userEvent.setup();
      renderLayout();

      await user.click(stripButton("Outline"));

      expect(screen.queryByRole("region", { name: "Outline" })).toBeNull();
      expect(
        screen.queryByRole("complementary", { name: "Right tool windows" }),
      ).toBeNull();
      expect(stripButton("Outline")).toHaveAttribute("aria-pressed", "false");
    });

    it("Given 变更有 3 个文件, Then 变更图标带角标 3", () => {
      renderLayout(windows(3));
      expect(stripButton("Changes")).toHaveAccessibleDescription("3");
      expect(
        within(stripButton("Changes")).getByTestId("tool-window-badge"),
      ).toHaveTextContent("3");
    });

    it.each([
      ["为 0", 0],
      ["未就绪", null],
    ])("Given 变更数%s, Then 不显示角标", (_label, count) => {
      renderLayout(windows(count));
      expect(
        within(stripButton("Changes")).queryByTestId("tool-window-badge"),
      ).toBeNull();
    });
  });

  describe("工具窗标题栏", () => {
    it("Given 变更窗显示, Then 标题栏显示名称与数量；大纲只显示名称", async () => {
      const user = userEvent.setup();
      renderLayout(windows(5));
      expect(
        within(screen.getByRole("region", { name: "Outline" })).getByRole(
          "heading",
        ),
      ).toHaveTextContent(/^Outline$/);

      await user.click(stripButton("Changes"));
      const heading = within(
        screen.getByRole("region", { name: /^Changes/ }),
      ).getByRole("heading");
      expect(heading).toHaveTextContent("Changes");
      expect(heading).toHaveTextContent("5");
    });

    it("Given 工具窗显示, When 点标题栏的隐藏, Then 它被隐藏", async () => {
      const user = userEvent.setup();
      renderLayout();

      const region = screen.getByRole("region", { name: "Outline" });
      await user.click(within(region).getByRole("button", { name: "Hide" }));

      expect(screen.queryByRole("region", { name: "Outline" })).toBeNull();
      expect(useToolWindowLayout.getState().shown.rightTop).toBeNull();
    });

    it("Given 同侧两个槽位都有工具窗, Then 两个区域都在右侧区域里上下排列", () => {
      act(() => useToolWindowLayout.getState().show("directory"));
      renderLayout();
      const right = screen.getByRole("complementary", {
        name: "Right tool windows",
      });
      const regions = within(right).getAllByRole("region");
      expect(regions.map((r) => r.getAttribute("aria-label"))).toEqual([
        "Outline",
        "Directory",
      ]);
    });
  });

  describe("隐藏态（新建会话提示页）", () => {
    it("Given hidden, When 渲染, Then 只渲染编辑区，没有图标条也没有工具窗", () => {
      renderLayout(windows(), true);
      expect(screen.getByRole("main")).toHaveTextContent("editor");
      expect(screen.queryByRole("toolbar")).toBeNull();
      expect(screen.queryByRole("region")).toBeNull();
    });
  });
});
