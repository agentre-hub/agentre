import { act, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { FolderTree, GitCompare, ListTree, Terminal } from "lucide-react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { TooltipProvider } from "@agentre-hub/agentre-ui";
import {
  DEFAULT_DIVIDER_RATIOS,
  DEFAULT_TOOL_WINDOW_MODES,
  DEFAULT_TOOL_WINDOW_SLOTS,
  useToolWindowLayout,
} from "@/stores/tool-window-layout-store";

import { ShortcutsProvider } from "../../shortcuts/shortcuts-provider";
import { ToolWindowLayout } from "../tool-window-layout";
import type { ToolWindowDefinition } from "../types";

const WINDOWS: ToolWindowDefinition[] = [
  {
    id: "outline",
    label: "Outline",
    icon: ListTree,
    content: <button type="button">outline item</button>,
  },
  {
    id: "changes",
    label: "Changes",
    icon: GitCompare,
    content: <div>changes body</div>,
  },
  {
    id: "directory",
    label: "Directory",
    icon: FolderTree,
    content: <button type="button">directory item</button>,
  },
  {
    id: "terminal",
    label: "Terminal",
    icon: Terminal,
    content: <div>terminal body</div>,
  },
];

function layoutTree({ hidden = false }: { hidden?: boolean } = {}) {
  return (
    <MemoryRouter>
      <ShortcutsProvider platform="darwin">
        <TooltipProvider>
          <ToolWindowLayout windows={WINDOWS} hidden={hidden}>
            <main>
              <div
                role="textbox"
                aria-label="Message"
                contentEditable="true"
                tabIndex={0}
                suppressContentEditableWarning
              />
              <button type="button">editor action</button>
            </main>
          </ToolWindowLayout>
        </TooltipProvider>
      </ShortcutsProvider>
    </MemoryRouter>
  );
}

function renderLayout(options?: { hidden?: boolean }) {
  return render(layoutTree(options));
}

function layout() {
  return useToolWindowLayout.getState();
}

function strip() {
  return screen.getByRole("toolbar", { name: "Tool windows" });
}

function stripButton(name: string) {
  return within(strip()).getByRole("button", { name });
}

function region(name: string) {
  return screen.getByRole("region", { name });
}

function stubWidth(width: number) {
  return vi
    .spyOn(HTMLElement.prototype, "getBoundingClientRect")
    .mockReturnValue({
      x: 0,
      y: 0,
      left: 0,
      top: 0,
      width,
      height: 800,
      right: width,
      bottom: 800,
      toJSON: () => ({}),
    } as DOMRect);
}

describe("工具窗行为", () => {
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
      mode: { ...DEFAULT_TOOL_WINDOW_MODES },
      floatRect: {},
      ratios: { ...DEFAULT_DIVIDER_RATIOS },
      sessionListCollapsed: false,
      focusSnapshot: null,
      maximized: null,
      narrowCollapsed: {},
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("⋯ 菜单", () => {
    it("Given 大纲显示, When 打开它的 ⋯ 菜单, Then 列出视图模式与移到（当前项打勾）、最大化、隐藏、恢复默认布局", async () => {
      const user = userEvent.setup();
      renderLayout();

      await user.click(
        within(region("Outline")).getByRole("button", {
          name: "Outline options",
        }),
      );

      const menu = await screen.findByRole("menu");
      const radio = (name: string) =>
        within(menu).getByRole("menuitemradio", { name });
      expect(radio("Docked · Pinned")).toHaveAttribute("aria-checked", "true");
      expect(radio("Docked · Auto-hide on blur")).toHaveAttribute(
        "aria-checked",
        "false",
      );
      expect(radio("Float")).toHaveAttribute("aria-checked", "false");
      expect(radio("Right top")).toHaveAttribute("aria-checked", "true");
      expect(radio("Right bottom")).toHaveAttribute("aria-checked", "false");
      expect(radio("Bottom left")).toHaveAttribute("aria-checked", "false");
      expect(radio("Bottom right")).toHaveAttribute("aria-checked", "false");
      expect(
        within(menu).getByRole("menuitem", { name: /Maximize/ }),
      ).toBeInTheDocument();
      expect(
        within(menu).getByRole("menuitem", { name: /Hide/ }),
      ).toBeInTheDocument();
      expect(
        within(menu).getByRole("menuitem", { name: "Restore default layout" }),
      ).toBeInTheDocument();
    });

    it("Given ⋯ 菜单, When 选浮动, Then 大纲改为浮动并浮在编辑区上方", async () => {
      const user = userEvent.setup();
      renderLayout();

      await user.click(
        within(region("Outline")).getByRole("button", {
          name: "Outline options",
        }),
      );
      await user.click(
        await screen.findByRole("menuitemradio", { name: "Float" }),
      );

      expect(layout().mode.outline).toBe("float");
      expect(region("Outline")).toHaveAttribute("data-floating", "true");
      expect(
        screen.queryByRole("complementary", { name: "Right tool windows" }),
      ).toBeNull();
    });

    it("Given ⋯ 菜单, When 移到底部右, Then 大纲出现在底部区域", async () => {
      const user = userEvent.setup();
      renderLayout();

      await user.click(
        within(region("Outline")).getByRole("button", {
          name: "Outline options",
        }),
      );
      await user.click(
        await screen.findByRole("menuitemradio", { name: "Bottom right" }),
      );

      expect(layout().slotOf.outline).toBe("bottomRight");
      const bottom = screen.getByRole("complementary", {
        name: "Bottom tool windows",
      });
      expect(
        within(bottom).getByRole("region", { name: "Outline" }),
      ).toBeInTheDocument();
    });

    it("Given 布局被改过, When 选恢复默认布局, Then 回到默认", async () => {
      const user = userEvent.setup();
      act(() => {
        layout().setMode("outline", "autoHide");
        layout().moveTo("directory", "bottomLeft");
      });
      renderLayout();

      await user.click(
        within(region("Outline")).getByRole("button", {
          name: "Outline options",
        }),
      );
      await user.click(
        await screen.findByRole("menuitem", { name: "Restore default layout" }),
      );

      expect(layout().slotOf).toEqual(DEFAULT_TOOL_WINDOW_SLOTS);
      expect(layout().mode).toEqual(DEFAULT_TOOL_WINDOW_MODES);
    });
  });

  describe("图标右键菜单", () => {
    it("Given 目录未显示, When 右键目录图标, Then 弹出同一份菜单（右下打勾）；选底部左后目录移过去并显示", async () => {
      const user = userEvent.setup();
      renderLayout();

      fireEvent.contextMenu(stripButton("Directory"));

      const menu = await screen.findByRole("menu");
      expect(
        within(menu).getByRole("menuitemradio", { name: "Right bottom" }),
      ).toHaveAttribute("aria-checked", "true");
      expect(
        within(menu).getByRole("menuitemradio", { name: "Docked · Pinned" }),
      ).toHaveAttribute("aria-checked", "true");

      await user.click(
        within(menu).getByRole("menuitemradio", { name: "Bottom left" }),
      );
      expect(layout().slotOf.directory).toBe("bottomLeft");
      expect(layout().shown.bottomLeft).toBe("directory");
    });
  });

  describe("失焦自动收起", () => {
    it("Given 大纲是「失焦自动收起」, When 焦点移到编辑区, Then 大纲收起", async () => {
      const user = userEvent.setup();
      act(() => layout().setMode("outline", "autoHide"));
      renderLayout();

      await user.click(screen.getByRole("button", { name: "outline item" }));
      expect(region("Outline")).toBeInTheDocument();

      await user.click(screen.getByRole("button", { name: "editor action" }));
      expect(screen.queryByRole("region", { name: "Outline" })).toBeNull();
    });

    it("Given 大纲是「失焦自动收起」, When 点开目录（焦点移到其它工具窗）, Then 大纲收起、目录留下", async () => {
      const user = userEvent.setup();
      act(() => layout().setMode("outline", "autoHide"));
      renderLayout();

      await user.click(stripButton("Directory"));

      expect(screen.queryByRole("region", { name: "Outline" })).toBeNull();
      expect(region("Directory")).toHaveFocus();
    });

    it("Given 大纲是「停靠·固定」, When 焦点移到编辑区, Then 大纲不收起", async () => {
      const user = userEvent.setup();
      renderLayout();

      await user.click(screen.getByRole("button", { name: "editor action" }));
      expect(region("Outline")).toBeInTheDocument();
    });

    it("Given 大纲是浮动窗, When 焦点移到编辑区, Then 浮动窗不收起", async () => {
      const user = userEvent.setup();
      act(() => layout().setMode("outline", "float"));
      renderLayout();

      await user.click(screen.getByRole("button", { name: "outline item" }));
      await user.click(screen.getByRole("button", { name: "editor action" }));
      expect(region("Outline")).toBeInTheDocument();
    });
  });

  describe("拖动图标换槽", () => {
    function startDrag(name: string) {
      const button = stripButton(name);
      fireEvent.pointerDown(button, {
        button: 0,
        pointerId: 1,
        clientX: 10,
        clientY: 10,
      });
      fireEvent.pointerMove(button, { pointerId: 1, clientX: 10, clientY: 30 });
      return button;
    }

    function group(name: string) {
      return within(strip()).getByRole("group", { name });
    }

    it("Given 拖动目录图标, When 拖到底部右一组, Then 目标槽位高亮并标出槽位名、图标条上出现落点；松手后目录移过去并显示", () => {
      renderLayout();

      startDrag("Directory");
      const target = group("Bottom right");
      fireEvent.pointerMove(target, {
        pointerId: 1,
        clientX: 10,
        clientY: 300,
      });

      expect(
        screen.getByTestId("tool-window-slot-highlight"),
      ).toHaveTextContent("Bottom right");
      expect(
        within(target).getByTestId("tool-window-drop-marker"),
      ).toBeInTheDocument();

      fireEvent.pointerUp(target, { pointerId: 1, clientX: 10, clientY: 300 });

      expect(layout().slotOf.directory).toBe("bottomRight");
      expect(layout().shown.bottomRight).toBe("directory");
      expect(screen.queryByTestId("tool-window-slot-highlight")).toBeNull();
      expect(
        within(
          screen.getByRole("complementary", { name: "Bottom tool windows" }),
        ).getByRole("region", { name: "Directory" }),
      ).toBeInTheDocument();
    });

    it("Given 拖动目录图标, When 拖回原组松手, Then 什么都不变，松手后的点击也不切换显示", () => {
      renderLayout();

      const button = startDrag("Directory");
      const own = group("Right bottom");
      fireEvent.pointerMove(own, { pointerId: 1, clientX: 10, clientY: 60 });
      expect(screen.queryByTestId("tool-window-slot-highlight")).toBeNull();
      fireEvent.pointerUp(own, { pointerId: 1, clientX: 10, clientY: 60 });
      fireEvent.click(button);

      expect(layout().slotOf.directory).toBe("rightBottom");
      expect(layout().shown.rightBottom).toBeNull();
    });

    it("Given 拖动目录图标, When 拖出图标条松手, Then 什么都不变", () => {
      renderLayout();

      startDrag("Directory");
      const editor = screen.getByRole("main");
      fireEvent.pointerMove(editor, {
        pointerId: 1,
        clientX: 400,
        clientY: 60,
      });
      fireEvent.pointerUp(editor, { pointerId: 1, clientX: 400, clientY: 60 });

      expect(layout().slotOf).toEqual(DEFAULT_TOOL_WINDOW_SLOTS);
      expect(layout().shown.rightBottom).toBeNull();
    });
  });

  describe("窄窗口", () => {
    it("Given 可用宽度 1000px 且右上的大纲停靠显示、焦点在输入框里, When 工具窗区域出现（新建会话发出首条消息）, Then 没有工具窗盖在编辑区上方", () => {
      stubWidth(1000);
      const { rerender } = renderLayout({ hidden: true });
      act(() => screen.getByRole("textbox", { name: "Message" }).focus());

      rerender(layoutTree());

      expect(screen.getByRole("textbox", { name: "Message" })).toHaveFocus();

      expect(
        within(screen.getByTestId("tool-window-overlay-layer")).queryByRole(
          "region",
        ),
      ).toBeNull();
      expect(screen.queryByRole("region", { name: "Outline" })).toBeNull();
    });

    it("Given 可用宽度 1000px, When 启动时右上的大纲是停靠显示, Then 它收起而不盖住对话，保存的显示状态与模式不变", () => {
      stubWidth(1000);
      renderLayout();

      expect(screen.queryByRole("region", { name: "Outline" })).toBeNull();
      expect(stripButton("Outline")).toHaveAttribute("aria-pressed", "false");
      expect(layout().shown.rightTop).toBe("outline");
      expect(layout().mode.outline).toBe("pinned");
    });

    it("Given 可用宽度 1000px 且大纲已收起, When 点图标条上的大纲, Then 它以浮动 + 失焦自动收起盖在对话上方并获得焦点", async () => {
      const user = userEvent.setup();
      stubWidth(1000);
      renderLayout();

      await user.click(stripButton("Outline"));

      const outline = region("Outline");
      expect(outline).toHaveFocus();
      expect(outline).toHaveAttribute("data-floating", "true");
      expect(outline).toHaveAttribute("data-auto-hide", "true");
      expect(stripButton("Outline")).toHaveAttribute("aria-pressed", "true");
      expect(layout().mode.outline).toBe("pinned");
    });

    it("Given 可用宽度 1000px 且大纲已收起, When 按 ⌃1, Then 它盖上来并获得焦点", () => {
      stubWidth(1000);
      renderLayout();

      fireEvent.keyDown(window, { key: "1", code: "Digit1", ctrlKey: true });

      expect(region("Outline")).toHaveFocus();
    });

    it("Given 窄窗口下大纲盖在对话上方且有焦点, When 焦点移到编辑区, Then 它收起，保存的显示状态与模式不变", async () => {
      const user = userEvent.setup();
      stubWidth(1000);
      renderLayout();
      await user.click(stripButton("Outline"));

      await user.click(screen.getByRole("button", { name: "editor action" }));

      expect(screen.queryByRole("region", { name: "Outline" })).toBeNull();
      expect(stripButton("Outline")).toHaveAttribute("aria-pressed", "false");
      expect(layout().shown.rightTop).toBe("outline");
      expect(layout().mode.outline).toBe("pinned");
    });

    it("Given 窄窗口下大纲盖在对话上方且有焦点, When 按 Esc, Then 焦点回到输入框、大纲随之收起", async () => {
      const user = userEvent.setup();
      stubWidth(1000);
      renderLayout();
      await user.click(stripButton("Outline"));

      await user.keyboard("{Escape}");

      expect(screen.getByRole("textbox", { name: "Message" })).toHaveFocus();
      expect(screen.queryByRole("region", { name: "Outline" })).toBeNull();
    });

    it("Given 窄窗口下大纲被收起, When 窗口恢复到 1400px, Then 它照用户原来的样子停靠并显示", async () => {
      const user = userEvent.setup();
      vi.stubGlobal("ResizeObserver", undefined);
      try {
        stubWidth(1000);
        renderLayout();
        await user.click(stripButton("Outline"));
        await user.click(screen.getByRole("button", { name: "editor action" }));
        expect(screen.queryByRole("region", { name: "Outline" })).toBeNull();

        stubWidth(1400);
        act(() => {
          window.dispatchEvent(new Event("resize"));
        });

        const outline = region("Outline");
        expect(outline).toHaveAttribute("data-floating", "false");
        expect(stripButton("Outline")).toHaveAttribute("aria-pressed", "true");
        expect(layout().mode.outline).toBe("pinned");
      } finally {
        vi.unstubAllGlobals();
      }
    });

    it("Given 可用宽度 1000px 且大纲是用户选的浮动, When 焦点移到编辑区, Then 浮动窗不收起", async () => {
      const user = userEvent.setup();
      stubWidth(1000);
      act(() => layout().setMode("outline", "float"));
      renderLayout();

      expect(region("Outline")).toHaveAttribute("data-auto-hide", "false");
      await user.click(screen.getByRole("button", { name: "outline item" }));
      await user.click(screen.getByRole("button", { name: "editor action" }));

      expect(region("Outline")).toBeInTheDocument();
      expect(layout().mode.outline).toBe("float");
    });

    it("Given 可用宽度 1400px, Then 大纲照常停靠", () => {
      stubWidth(1400);
      renderLayout();
      expect(region("Outline")).toHaveAttribute("data-floating", "false");
    });

    it("Given 可用宽度 1000px, Then 底部槽位里的工具窗照常停靠", () => {
      stubWidth(1000);
      act(() => layout().show("terminal"));
      renderLayout();
      expect(region("Terminal")).toHaveAttribute("data-floating", "false");
    });
  });

  describe("分隔条", () => {
    it("Given 右侧有工具窗, When 聚焦编辑区与右侧之间的分隔条按 ⌃⇧←, Then 右侧变宽且比例记进布局", async () => {
      const user = userEvent.setup();
      renderLayout();

      const sep = screen.getByRole("separator", {
        name: "Resize right tool windows",
      });
      expect(sep).toHaveAttribute("aria-valuenow", "25");
      sep.focus();
      await user.keyboard("{Control>}{Shift>}{ArrowLeft}{/Shift}{/Control}");

      expect(layout().ratios.right).toBeCloseTo(0.27);
      expect(sep).toHaveAttribute("aria-valuenow", "27");
    });

    it("Given 右上右下都有工具窗, Then 两者之间有分隔条，⌃⇧↓ 让右上变高", async () => {
      const user = userEvent.setup();
      act(() => layout().show("directory"));
      renderLayout();

      const sep = screen.getByRole("separator", {
        name: "Resize right top and bottom",
      });
      sep.focus();
      await user.keyboard("{Control>}{Shift>}{ArrowDown}{/Shift}{/Control}");
      expect(layout().ratios.rightSplit).toBeCloseTo(0.52);
    });

    it("Given 底部左右都有工具窗, Then 编辑区与底部之间、底部左右之间各有一条分隔条", async () => {
      const user = userEvent.setup();
      act(() => {
        layout().show("terminal");
        layout().moveTo("directory", "bottomRight");
      });
      renderLayout();

      const bottom = screen.getByRole("separator", {
        name: "Resize bottom tool windows",
      });
      bottom.focus();
      await user.keyboard("{Control>}{Shift>}{ArrowUp}{/Shift}{/Control}");
      expect(layout().ratios.bottom).toBeCloseTo(0.34);

      const split = screen.getByRole("separator", {
        name: "Resize bottom left and right",
      });
      split.focus();
      await user.keyboard("{Control>}{Shift>}{ArrowRight}{/Shift}{/Control}");
      expect(layout().ratios.bottomSplit).toBeCloseTo(0.52);
    });

    it("Given 宽 1600px 的工具窗区域, When 把右侧分隔条拖到 x=1120, Then 右侧比例为 0.3", () => {
      stubWidth(1600);
      renderLayout();

      const sep = screen.getByRole("separator", {
        name: "Resize right tool windows",
      });
      fireEvent.pointerDown(sep, { button: 0, pointerId: 1, clientX: 1200 });
      fireEvent.pointerMove(document, { pointerId: 1, clientX: 1120 });
      fireEvent.pointerUp(document, { pointerId: 1, clientX: 1120 });

      expect(layout().ratios.right).toBeCloseTo(0.3);
    });
  });

  describe("焦点所在工具窗的按键", () => {
    it("Given 焦点在大纲里, When 按 ⇧Esc, Then 大纲被隐藏", async () => {
      const user = userEvent.setup();
      renderLayout();

      await user.click(screen.getByRole("button", { name: "outline item" }));
      await user.keyboard("{Shift>}{Escape}{/Shift}");

      expect(screen.queryByRole("region", { name: "Outline" })).toBeNull();
    });

    it("Given 焦点在固定的大纲里, When 按 Esc, Then 焦点回到输入框、大纲留下", async () => {
      const user = userEvent.setup();
      renderLayout();

      await user.click(screen.getByRole("button", { name: "outline item" }));
      await user.keyboard("{Escape}");

      expect(screen.getByRole("textbox", { name: "Message" })).toHaveFocus();
      expect(region("Outline")).toBeInTheDocument();
    });

    it("Given 焦点在「失焦自动收起」的大纲里, When 按 Esc, Then 焦点回到输入框、大纲随之收起", async () => {
      const user = userEvent.setup();
      act(() => layout().setMode("outline", "autoHide"));
      renderLayout();

      await user.click(screen.getByRole("button", { name: "outline item" }));
      await user.keyboard("{Escape}");

      expect(screen.getByRole("textbox", { name: "Message" })).toHaveFocus();
      expect(screen.queryByRole("region", { name: "Outline" })).toBeNull();
    });

    it("Given 焦点在浮动的大纲里, When 按 Esc, Then 焦点回到输入框、浮动窗不收起", async () => {
      const user = userEvent.setup();
      act(() => layout().setMode("outline", "float"));
      renderLayout();

      await user.click(screen.getByRole("button", { name: "outline item" }));
      await user.keyboard("{Escape}");

      expect(screen.getByRole("textbox", { name: "Message" })).toHaveFocus();
      expect(region("Outline")).toBeInTheDocument();
    });

    it("Given 焦点在大纲里, When 按 ⇧⌘', Then 大纲最大化；再按一次还原", async () => {
      const user = userEvent.setup();
      renderLayout();

      await user.click(screen.getByRole("button", { name: "outline item" }));
      await user.keyboard("{Meta>}{Shift>}[Quote]{/Shift}{/Meta}");
      expect(layout().maximized).toBe("outline");
      expect(region("Outline")).toHaveAttribute("data-maximized", "true");
      // 换到最大化的位置重新挂载后，焦点仍在它里面。
      expect(region("Outline")).toHaveFocus();

      await user.keyboard("{Meta>}{Shift>}[Quote]{/Shift}{/Meta}");
      expect(layout().maximized).toBeNull();
      expect(region("Outline")).toHaveAttribute("data-maximized", "false");
    });
  });

  describe("专注", () => {
    it("Given 大纲与目录显示, When 按 ⇧⌘\\, Then 全部隐藏、图标条保留且没有已打开高亮；再按一次恢复", () => {
      act(() => layout().show("directory"));
      renderLayout();

      fireEvent.keyDown(window, {
        key: "|",
        code: "Backslash",
        metaKey: true,
        shiftKey: true,
      });
      expect(screen.queryAllByRole("region")).toHaveLength(0);
      expect(strip()).toBeInTheDocument();
      for (const b of within(strip()).getAllByRole("button")) {
        expect(b).toHaveAttribute("aria-pressed", "false");
      }

      fireEvent.keyDown(window, {
        key: "|",
        code: "Backslash",
        metaKey: true,
        shiftKey: true,
      });
      expect(region("Outline")).toBeInTheDocument();
      expect(region("Directory")).toBeInTheDocument();
    });
  });

  describe("焦点描边", () => {
    const ring = (name: string) =>
      region(name).querySelector<HTMLElement>("[data-tool-window-focus-ring]");

    it("Given 大纲里有焦点, When 渲染, Then 描边层是工具窗的最后一个子元素（盖在内容之上）、不接指针、继承圆角", async () => {
      const user = userEvent.setup();
      renderLayout();

      await user.click(screen.getByRole("button", { name: "outline item" }));

      const layer = ring("Outline");
      expect(layer).not.toBeNull();
      expect(region("Outline").lastElementChild).toBe(layer);
      expect(layer).toHaveClass("pointer-events-none", "absolute", "inset-0");
      expect(layer).toHaveClass("rounded-[inherit]");
      expect(layer).toHaveAttribute("aria-hidden", "true");
    });

    it("Given 隐藏着的大纲, When 从图标条打开（窗口挂载时即拿焦点）, Then 描边随之出现", async () => {
      const user = userEvent.setup();
      act(() =>
        useToolWindowLayout.setState({
          shown: { ...layout().shown, rightTop: null },
        }),
      );
      renderLayout();

      await user.click(stripButton("Outline"));
      expect(region("Outline")).toHaveFocus();
      expect(ring("Outline")).not.toBeNull();
    });

    it("Given 大纲有描边, When 焦点回到输入框, Then 描边消失", async () => {
      const user = userEvent.setup();
      renderLayout();

      await user.click(screen.getByRole("button", { name: "outline item" }));
      expect(ring("Outline")).not.toBeNull();

      act(() => screen.getByRole("textbox", { name: "Message" }).focus());
      expect(ring("Outline")).toBeNull();
    });

    it("Given 大纲与变更同时显示, When 焦点从大纲移到变更, Then 同一时刻只有一个工具窗带描边", async () => {
      const user = userEvent.setup();
      act(() =>
        useToolWindowLayout.setState({
          shown: { ...layout().shown, rightBottom: "changes" },
        }),
      );
      renderLayout();

      await user.click(screen.getByRole("button", { name: "outline item" }));
      expect(
        document.querySelectorAll("[data-tool-window-focus-ring]"),
      ).toHaveLength(1);
      expect(ring("Outline")).not.toBeNull();

      act(() => region("Changes").focus());
      expect(
        document.querySelectorAll("[data-tool-window-focus-ring]"),
      ).toHaveLength(1);
      expect(ring("Changes")).not.toBeNull();
      expect(ring("Outline")).toBeNull();
    });

    it("Given 工具窗内部的焦点在子元素间移动, Then 描边不闪灭", async () => {
      const user = userEvent.setup();
      act(() =>
        useToolWindowLayout.setState({
          shown: { ...layout().shown, rightBottom: "changes" },
        }),
      );
      renderLayout();

      await user.click(screen.getByRole("button", { name: "outline item" }));
      // 只派发 focusout（目标仍在窗口内），不跟一个 focusin：描边不能靠随后的
      // focusin 重新点亮来掩盖被误灭。
      act(() => {
        screen.getByRole("button", { name: "outline item" }).dispatchEvent(
          new FocusEvent("focusout", {
            bubbles: true,
            relatedTarget: region("Outline"),
          }),
        );
      });
      expect(ring("Outline")).not.toBeNull();
    });

    it("Given 大纲里拿着焦点的元素被移除（焦点落回 body、不发 focusout）, When 焦点随后落到输入框, Then 描边消失", async () => {
      renderLayout();
      const transient = document.createElement("button");
      region("Outline").appendChild(transient);
      act(() => transient.focus());
      expect(ring("Outline")).not.toBeNull();

      act(() => transient.remove());
      act(() => screen.getByRole("textbox", { name: "Message" }).focus());
      expect(ring("Outline")).toBeNull();
    });

    describe("焦点进入嵌入页面（iframe）", () => {
      // 浏览器把焦点交给（跨源）iframe 时，父文档不发 focusin：父窗口收到一个 blur，
      // document.activeElement 变成那个 iframe 元素（常常要到 blur 之后才变）。
      let active: Element | null = null;
      beforeEach(() => {
        active = null;
        Object.defineProperty(document, "activeElement", {
          configurable: true,
          get: () => active ?? document.body,
        });
      });
      afterEach(() => {
        delete (document as { activeElement?: unknown }).activeElement;
      });

      const embed = (name: string) => {
        const frame = document.createElement("iframe");
        region(name).appendChild(frame);
        return frame;
      };

      it("Given 变更带描边, When 焦点进入大纲里的嵌入页面（只有窗口 blur、activeElement 为该 iframe）, Then 大纲带描边、变更的描边消失；焦点回到输入框后描边消失", () => {
        act(() =>
          useToolWindowLayout.setState({
            shown: { ...layout().shown, rightBottom: "changes" },
          }),
        );
        renderLayout();
        const frame = embed("Outline");
        act(() => {
          active = region("Changes");
          active.dispatchEvent(new FocusEvent("focusin", { bubbles: true }));
        });
        expect(ring("Changes")).not.toBeNull();

        act(() => {
          active = frame;
          window.dispatchEvent(new FocusEvent("blur"));
        });
        expect(ring("Outline")).not.toBeNull();
        expect(ring("Changes")).toBeNull();
        expect(
          document.querySelectorAll("[data-tool-window-focus-ring]"),
        ).toHaveLength(1);

        const composer = screen.getByRole("textbox", { name: "Message" });
        act(() => {
          active = composer;
          window.dispatchEvent(new FocusEvent("focus"));
          composer.dispatchEvent(new FocusEvent("focusin", { bubbles: true }));
        });
        expect(ring("Outline")).toBeNull();
        expect(
          document.querySelectorAll("[data-tool-window-focus-ring]"),
        ).toHaveLength(0);
      });

      it("Given 窗口 blur 时 activeElement 还没变成 iframe, When 它随后才指向大纲里的 iframe, Then 描边随之出现", async () => {
        renderLayout();
        const frame = embed("Outline");

        act(() => {
          window.dispatchEvent(new FocusEvent("blur"));
          active = frame;
        });
        await act(async () => {
          await new Promise((resolve) => setTimeout(resolve, 0));
        });
        expect(ring("Outline")).not.toBeNull();
      });

      it("Given 大纲带描边, When 整个应用失焦（activeElement 仍是大纲里的按钮、先发 focusout）, Then 描边照旧消失", async () => {
        renderLayout();
        const item = screen.getByRole("button", { name: "outline item" });
        act(() => {
          active = item;
          item.dispatchEvent(new FocusEvent("focusin", { bubbles: true }));
        });
        expect(ring("Outline")).not.toBeNull();

        act(() => {
          item.dispatchEvent(
            new FocusEvent("focusout", { bubbles: true, relatedTarget: null }),
          );
          window.dispatchEvent(new FocusEvent("blur"));
        });
        await act(async () => {
          await new Promise((resolve) => setTimeout(resolve, 0));
        });
        expect(ring("Outline")).toBeNull();
      });
    });

    it("Given 停靠的大纲, Then 用对话卡的圆角与 --sheet-shadow、不带浮层阴影", () => {
      renderLayout();
      const el = region("Outline");
      expect(el).toHaveClass("rounded-xl", "shadow-[var(--sheet-shadow)]");
      expect(el).not.toHaveClass("shadow-overlay");
    });

    it("Given 大纲是浮动窗, Then 圆角与对话卡相同、保持浮层阴影、不用 --sheet-shadow", () => {
      act(() => layout().setMode("outline", "float"));
      renderLayout();
      const el = region("Outline");
      expect(el).toHaveClass("shadow-overlay", "rounded-xl");
      expect(el).not.toHaveClass("shadow-[var(--sheet-shadow)]");
    });

    it("Given 窄窗口里盖在对话上的大纲, Then 圆角与对话卡相同、保持浮层阴影、不用 --sheet-shadow", async () => {
      const user = userEvent.setup();
      stubWidth(1000);
      renderLayout();
      await user.click(stripButton("Outline"));
      const el = region("Outline");
      expect(el).toHaveAttribute("data-auto-hide", "true");
      expect(el).toHaveClass("shadow-overlay", "rounded-xl");
      expect(el).not.toHaveClass("shadow-[var(--sheet-shadow)]");
    });

    it("Given 大纲最大化, Then 圆角与对话卡相同、保持浮层阴影、不用 --sheet-shadow", () => {
      act(() => layout().toggleMaximize("outline"));
      renderLayout();
      const el = region("Outline");
      expect(el).toHaveAttribute("data-maximized", "true");
      expect(el).toHaveClass("shadow-overlay", "rounded-xl");
      expect(el).not.toHaveClass("shadow-[var(--sheet-shadow)]");
    });
  });
});
