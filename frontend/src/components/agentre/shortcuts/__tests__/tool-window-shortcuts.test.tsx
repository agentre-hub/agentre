import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it } from "vitest";

import {
  DEFAULT_DIVIDER_RATIOS,
  DEFAULT_TOOL_WINDOW_MODES,
  DEFAULT_TOOL_WINDOW_SLOTS,
  useToolWindowLayout,
  selectRegionState,
  type ToolWindowId,
  type ToolWindowRegion,
} from "@/stores/tool-window-layout-store";

import type { DesktopPlatform } from "../../chrome";
import { KeyboardShortcutsPanel } from "../keyboard-shortcuts-panel";
import {
  REGISTRY,
  SESSION_LIST_TOGGLE_ID,
  TOOL_WINDOW_REGION_TOGGLE_IDS,
} from "../registry";
import { ShortcutsProvider } from "../shortcuts-provider";
import { STORAGE_KEY } from "../storage";
import { isToolWindowShortcutForTerminal } from "../tool-window-shortcuts";
import { useShortcutLabel } from "../use-shortcut-label";

function ShortcutLabels() {
  return (
    <>
      <span data-testid="label-sessionList">
        {useShortcutLabel(SESSION_LIST_TOGGLE_ID)}
      </span>
      <span data-testid="label-bottom">
        {useShortcutLabel(TOOL_WINDOW_REGION_TOGGLE_IDS.bottom)}
      </span>
      <span data-testid="label-right">
        {useShortcutLabel(TOOL_WINDOW_REGION_TOGGLE_IDS.right)}
      </span>
    </>
  );
}

function renderProvider(platform: DesktopPlatform, panel = false) {
  return render(
    <MemoryRouter>
      <ShortcutsProvider platform={platform}>
        {panel ? <KeyboardShortcutsPanel /> : null}
        <ShortcutLabels />
      </ShortcutsProvider>
    </MemoryRouter>,
  );
}

function regionShown(region: ToolWindowRegion) {
  return selectRegionState(region)(useToolWindowLayout.getState()).shown;
}

function storeOverride(id: string, chord: { mod: string; key: string }) {
  localStorage.setItem(
    STORAGE_KEY,
    JSON.stringify({ version: 1, bindings: { [id]: chord } }),
  );
}

// ⌥⌘B 在 macOS 上 key 被 Option 改写成「∫」，只有 code 还是 KeyB。
const OPT_CMD_B = { key: "∫", code: "KeyB", metaKey: true, altKey: true };
const CMD_J = { key: "j", code: "KeyJ", metaKey: true };

function isShown(id: ToolWindowId) {
  const s = useToolWindowLayout.getState();
  return s.shown[s.slotOf[id]] === id;
}

/** 返回值同 dispatchEvent：false = 按键被应用吃掉（preventDefault）。 */
function press(init: KeyboardEventInit) {
  return fireEvent.keyDown(window, init);
}

describe("工具窗快捷键", () => {
  beforeEach(() => {
    localStorage.clear();
    useToolWindowLayout.setState({
      slotOf: { ...DEFAULT_TOOL_WINDOW_SLOTS },
      shown: {
        rightTop: null,
        rightBottom: null,
        bottomLeft: null,
        bottomRight: null,
      },
      focusRequest: null,
      mode: { ...DEFAULT_TOOL_WINDOW_MODES },
      floatRect: {},
      ratios: { ...DEFAULT_DIVIDER_RATIOS },
      sessionListCollapsed: false,
      regionMemory: {
        rightTop: null,
        rightBottom: null,
        bottomLeft: null,
        bottomRight: null,
      },
      narrowCollapsed: {},
      focusSnapshot: null,
      maximized: null,
      // 对话页的图标条挂着（这些用例模拟的是在对话页里按键）。
      stripMounts: 1,
    });
  });

  it("Given 页面上没有图标条（新建会话提示页 / 别的页面）, When 按 ⌃1 / ⌃` / ⇧⌘\\, Then 不动保存的布局、也不留焦点请求", () => {
    useToolWindowLayout.setState({ stripMounts: 0 });
    useToolWindowLayout.getState().show("outline");
    renderProvider("darwin");
    press({ key: "1", code: "Digit1", ctrlKey: true });
    press({ key: "`", code: "Backquote", ctrlKey: true });
    press({ key: "|", code: "Backslash", metaKey: true, shiftKey: true });
    const s = useToolWindowLayout.getState();
    expect(s.shown).toEqual({
      rightTop: "outline",
      rightBottom: null,
      bottomLeft: null,
      bottomRight: null,
    });
    expect(s.focusRequest).toBeNull();
    expect(s.focusSnapshot).toBeNull();
  });

  it("Given 页面上没有图标条, When 按 ⌘0, Then 会话列表照常收起", () => {
    useToolWindowLayout.setState({ stripMounts: 0 });
    renderProvider("darwin");
    press({ key: "0", code: "Digit0", metaKey: true });
    expect(useToolWindowLayout.getState().sessionListCollapsed).toBe(true);
  });

  it.each<[string, ToolWindowId]>([
    ["Digit1", "outline"],
    ["Digit2", "changes"],
    ["Digit3", "directory"],
    ["Digit4", "relations"],
    ["Digit5", "sessionInfo"],
    ["Digit6", "webPreview"],
  ])(
    "Given macOS, When 按 ⌃ + %s, Then 切换 %s 的显示（与点图标相同）",
    (code, id) => {
      renderProvider("darwin");
      press({ key: code.slice(-1), code, ctrlKey: true });
      expect(isShown(id)).toBe(true);
      expect(useToolWindowLayout.getState().focusRequest).toBe(id);
      press({ key: code.slice(-1), code, ctrlKey: true });
      expect(isShown(id)).toBe(false);
    },
  );

  it("Given macOS, When 按 ⌃`, Then 切换终端", () => {
    renderProvider("darwin");
    press({ key: "`", code: "Backquote", ctrlKey: true });
    expect(isShown("terminal")).toBe(true);
  });

  it("Given Windows, When 按 Alt+1 / Ctrl+`, Then 切换大纲 / 终端", () => {
    renderProvider("windows");
    press({ key: "1", code: "Digit1", altKey: true });
    expect(isShown("outline")).toBe(true);
    press({ key: "`", code: "Backquote", ctrlKey: true });
    expect(isShown("terminal")).toBe(true);
  });

  it("Given Windows, When 按 Ctrl+1（标签页切换）, Then 不动工具窗", () => {
    renderProvider("windows");
    press({ key: "1", code: "Digit1", ctrlKey: true });
    expect(isShown("outline")).toBe(false);
  });

  it("Given macOS, When 按 ⌘1（标签页切换）或 ⌃⇧1, Then 不动工具窗", () => {
    renderProvider("darwin");
    press({ key: "1", code: "Digit1", metaKey: true });
    press({ key: "!", code: "Digit1", ctrlKey: true, shiftKey: true });
    expect(isShown("outline")).toBe(false);
  });

  it("Given Windows, When 按 Ctrl+Shift+\\, Then 进入专注", () => {
    useToolWindowLayout.getState().show("outline");
    renderProvider("windows");
    press({ key: "|", code: "Backslash", ctrlKey: true, shiftKey: true });
    expect(isShown("outline")).toBe(false);
    expect(useToolWindowLayout.getState().focusSnapshot).not.toBeNull();
  });

  it("Given macOS, When 按 ⌘0 两次, Then 会话列表先收起再展开", () => {
    renderProvider("darwin");
    press({ key: "0", code: "Digit0", metaKey: true });
    expect(useToolWindowLayout.getState().sessionListCollapsed).toBe(true);
    press({ key: "0", code: "Digit0", metaKey: true });
    expect(useToolWindowLayout.getState().sessionListCollapsed).toBe(false);
  });

  it("Given Windows, When 按 Ctrl+0, Then 会话列表收起", () => {
    renderProvider("windows");
    press({ key: "0", code: "Digit0", ctrlKey: true });
    expect(useToolWindowLayout.getState().sessionListCollapsed).toBe(true);
  });

  it("Given 输入法正在合成, When 按 ⌃1, Then 不动工具窗", () => {
    renderProvider("darwin");
    press({ key: "1", code: "Digit1", ctrlKey: true, isComposing: true });
    expect(isShown("outline")).toBe(false);
  });

  it("Then 工具窗快捷键里只有两个区域开关可改绑，其余固定", () => {
    const defs = REGISTRY.filter((d) => d.scope === "toolWindows");
    // 七个工具窗切换 + 专注 + ⇧Esc + Esc + ⇧⌘' + ⌘0 + 底部 / 右侧区域开关。
    expect(defs).toHaveLength(14);
    expect(defs.filter((d) => d.rebindable).map((d) => d.id)).toEqual([
      TOOL_WINDOW_REGION_TOGGLE_IDS.bottom,
      TOOL_WINDOW_REGION_TOGGLE_IDS.right,
    ]);
  });

  describe("区域开关 ⌘J / ⌥⌘B", () => {
    it("Then 默认底部为 ⌘J、右侧为 ⌥⌘B，与 ⌘B（看板）是不同组合", () => {
      const bottom = REGISTRY.find(
        (d) => d.id === TOOL_WINDOW_REGION_TOGGLE_IDS.bottom,
      );
      const right = REGISTRY.find(
        (d) => d.id === TOOL_WINDOW_REGION_TOGGLE_IDS.right,
      );
      expect(bottom?.defaultBinding).toEqual({ mod: "primary", key: "J" });
      expect(right?.defaultBinding).toEqual({ mod: "primary-alt", key: "B" });
      const issues = REGISTRY.find((d) => d.id === "nav.issues");
      expect(issues?.defaultBinding).toEqual({ mod: "primary", key: "B" });
    });

    it("Given macOS 对话页, When 按 ⌘J 两次, Then 底部区域先显示首个工具窗（终端）再整块隐藏", () => {
      renderProvider("darwin");
      expect(press(CMD_J)).toBe(false);
      expect(isShown("terminal")).toBe(true);
      expect(regionShown("bottom")).toBe(true);
      press(CMD_J);
      expect(regionShown("bottom")).toBe(false);
    });

    it("Given macOS 对话页, When 按 ⌥⌘B, Then 右侧区域显示大纲；底部不动", () => {
      renderProvider("darwin");
      expect(press(OPT_CMD_B)).toBe(false);
      expect(isShown("outline")).toBe(true);
      expect(regionShown("bottom")).toBe(false);
      press(OPT_CMD_B);
      expect(regionShown("right")).toBe(false);
    });

    it("Given macOS 对话页, When 按 ⌘B（看板）, Then 右侧区域不动", () => {
      renderProvider("darwin");
      press({ key: "b", code: "KeyB", metaKey: true });
      expect(regionShown("right")).toBe(false);
    });

    it("Given Windows 对话页, When 按 Ctrl+J / Ctrl+Alt+B, Then 分别切换底部 / 右侧区域", () => {
      renderProvider("windows");
      press({ key: "j", code: "KeyJ", ctrlKey: true });
      expect(regionShown("bottom")).toBe(true);
      press({ key: "b", code: "KeyB", ctrlKey: true, altKey: true });
      expect(regionShown("right")).toBe(true);
    });

    it("Given 页面上没有图标条, When 按 ⌘J / ⌥⌘B, Then 布局不动、按键照常往下走", () => {
      useToolWindowLayout.setState({ stripMounts: 0 });
      renderProvider("darwin");
      expect(press(CMD_J)).toBe(true);
      expect(press(OPT_CMD_B)).toBe(true);
      expect(regionShown("bottom")).toBe(false);
      expect(regionShown("right")).toBe(false);
    });

    it("Given 用户把底部区域改绑成 ⌘⇧K, When 按 ⌘⇧K, Then 切换底部；再按 ⌘J 不再生效", () => {
      storeOverride(TOOL_WINDOW_REGION_TOGGLE_IDS.bottom, {
        mod: "primary-shift",
        key: "K",
      });
      renderProvider("darwin");
      press({ key: "K", code: "KeyK", metaKey: true, shiftKey: true });
      expect(regionShown("bottom")).toBe(true);
      press(CMD_J);
      expect(regionShown("bottom")).toBe(true);
    });

    it("Given 已保存的 ⌥⌘R 改绑, When 启动后按 ⌥⌘R, Then 切换右侧区域", () => {
      storeOverride(TOOL_WINDOW_REGION_TOGGLE_IDS.right, {
        mod: "primary-alt",
        key: "R",
      });
      renderProvider("darwin");
      press({ key: "®", code: "KeyR", metaKey: true, altKey: true });
      expect(regionShown("right")).toBe(true);
      press(OPT_CMD_B);
      expect(regionShown("right")).toBe(true);
    });

    it("Given 焦点在终端里, Then ⌘J / ⌥⌘B 交还给应用；改绑后放行的是新组合", () => {
      const first = renderProvider("darwin");
      expect(
        isToolWindowShortcutForTerminal(
          new KeyboardEvent("keydown", CMD_J),
          "darwin",
        ),
      ).toBe(true);
      expect(
        isToolWindowShortcutForTerminal(
          new KeyboardEvent("keydown", OPT_CMD_B),
          "darwin",
        ),
      ).toBe(true);
      expect(
        isToolWindowShortcutForTerminal(
          new KeyboardEvent("keydown", {
            key: "b",
            code: "KeyB",
            metaKey: true,
          }),
          "darwin",
        ),
      ).toBe(false);
      first.unmount();

      storeOverride(TOOL_WINDOW_REGION_TOGGLE_IDS.bottom, {
        mod: "primary",
        key: "K",
      });
      renderProvider("darwin");
      const cmdK = new KeyboardEvent("keydown", {
        key: "k",
        code: "KeyK",
        metaKey: true,
      });
      expect(isToolWindowShortcutForTerminal(cmdK, "darwin")).toBe(true);
      expect(
        isToolWindowShortcutForTerminal(
          new KeyboardEvent("keydown", CMD_J),
          "darwin",
        ),
      ).toBe(false);
    });

    it("Given Windows 焦点在终端里, Then Ctrl+J / Ctrl+Alt+B 交还给应用", () => {
      renderProvider("windows");
      expect(
        isToolWindowShortcutForTerminal(
          new KeyboardEvent("keydown", {
            key: "j",
            code: "KeyJ",
            ctrlKey: true,
          }),
          "windows",
        ),
      ).toBe(true);
      expect(
        isToolWindowShortcutForTerminal(
          new KeyboardEvent("keydown", {
            key: "b",
            code: "KeyB",
            ctrlKey: true,
            altKey: true,
          }),
          "windows",
        ),
      ).toBe(true);
    });

    it("Given macOS, Then 按键文案读的是当前绑定：⌘0 / ⌘J / ⌥⌘B，改绑后跟着变", () => {
      const first = renderProvider("darwin");
      expect(screen.getByTestId("label-sessionList")).toHaveTextContent("⌘0");
      expect(screen.getByTestId("label-bottom")).toHaveTextContent("⌘J");
      expect(screen.getByTestId("label-right")).toHaveTextContent("⌥⌘B");
      first.unmount();

      storeOverride(TOOL_WINDOW_REGION_TOGGLE_IDS.right, {
        mod: "primary-alt",
        key: "R",
      });
      renderProvider("darwin");
      expect(screen.getByTestId("label-right")).toHaveTextContent("⌥⌘R");
    });

    it("Given Windows, Then 按键文案为 Ctrl+0 / Ctrl+J / Ctrl+Alt+B", () => {
      renderProvider("windows");
      expect(screen.getByTestId("label-sessionList")).toHaveTextContent(
        "Ctrl+0",
      );
      expect(screen.getByTestId("label-bottom")).toHaveTextContent("Ctrl+J");
      expect(screen.getByTestId("label-right")).toHaveTextContent("Ctrl+Alt+B");
    });
  });

  describe("快捷键面板", () => {
    function toolWindowSection() {
      const heading = screen.getByRole("heading", { name: "Tool Windows" });
      return within(heading.closest("section")!);
    }

    it("Given macOS, Then 工具窗一节列出全部快捷键（固定、不可改绑）", () => {
      renderProvider("darwin", true);
      const section = toolWindowSection();

      for (const [label, chord] of [
        ["Show / Hide Outline", "⌃1"],
        ["Show / Hide Changes", "⌃2"],
        ["Show / Hide Directory", "⌃3"],
        ["Show / Hide Relations", "⌃4"],
        ["Show / Hide Session Info", "⌃5"],
        ["Show / Hide Web Preview", "⌃6"],
        ["Show / Hide Terminal", "⌃`"],
        ["Hide All Tool Windows", "⌘⇧\\"],
        ["Hide Focused Tool Window", "⇧Esc"],
        ["Back to Input Box", "Esc"],
        ["Maximize Focused Area", "⌘⇧'"],
        ["Collapse / Expand Session List", "⌘0"],
      ]) {
        const row = section
          .getByText(label)
          .closest<HTMLElement>("[data-slot=shortcut-row]")!;
        expect(within(row).getByText(chord)).toBeInTheDocument();
        expect(within(row).getByText("Fixed")).toBeInTheDocument();
      }
    });

    function rowOf(label: string) {
      return within(
        toolWindowSection()
          .getByText(label)
          .closest<HTMLElement>("[data-slot=shortcut-row]")!,
      );
    }

    it("Given macOS, Then 底部 / 右侧区域开关列为 ⌘J / ⌥⌘B，可录制改绑", () => {
      renderProvider("darwin", true);
      for (const [label, chord] of [
        ["Show / Hide Bottom Tool Windows", "⌘J"],
        ["Show / Hide Right Tool Windows", "⌥⌘B"],
      ]) {
        const row = rowOf(label);
        expect(row.getByText(chord)).toBeInTheDocument();
        expect(row.getByRole("button", { name: "Record" })).toBeInTheDocument();
        expect(row.queryByText("Fixed")).toBeNull();
      }
      // 其余工具窗快捷键仍然固定：这一节只有两个录制按钮。
      expect(
        toolWindowSection().getAllByRole("button", { name: "Record" }),
      ).toHaveLength(2);
    });

    it("Given macOS, When 给右侧区域录制 ⌥⌘R, Then 行内显示 ⌥⌘R、保存下来且按 ⌥⌘R 切换右侧", async () => {
      const user = userEvent.setup();
      renderProvider("darwin", true);
      const row = rowOf("Show / Hide Right Tool Windows");
      await user.click(row.getByRole("button", { name: "Record" }));
      press({ key: "®", code: "KeyR", metaKey: true, altKey: true });

      expect(
        rowOf("Show / Hide Right Tool Windows").getByText("⌥⌘R"),
      ).toBeInTheDocument();
      expect(screen.getByTestId("label-right")).toHaveTextContent("⌥⌘R");
      const saved = JSON.parse(localStorage.getItem(STORAGE_KEY)!) as {
        bindings: Record<string, unknown>;
      };
      expect(saved.bindings[TOOL_WINDOW_REGION_TOGGLE_IDS.right]).toEqual({
        mod: "primary-alt",
        key: "R",
      });
      press({ key: "®", code: "KeyR", metaKey: true, altKey: true });
      expect(regionShown("right")).toBe(true);
    });

    it("Given macOS, When 给右侧区域录制 ⌘B, Then 提示与「看板」冲突、绑定不变", async () => {
      const user = userEvent.setup();
      renderProvider("darwin", true);
      await user.click(
        rowOf("Show / Hide Right Tool Windows").getByRole("button", {
          name: "Record",
        }),
      );
      press({ key: "b", code: "KeyB", metaKey: true });
      expect(await screen.findByRole("alert")).toHaveTextContent(
        "Switch to Board",
      );
      expect(screen.getByTestId("label-right")).toHaveTextContent("⌥⌘B");
    });

    it("Given macOS, When 给看板录制 ⌥⌘B, Then 提示与右侧区域开关冲突", async () => {
      const user = userEvent.setup();
      renderProvider("darwin", true);
      const issuesRow = within(
        screen
          .getByText("Switch to Board")
          .closest<HTMLElement>("[data-slot=shortcut-row]")!,
      );
      await user.click(issuesRow.getByRole("button", { name: "Record" }));
      press(OPT_CMD_B);
      expect(await screen.findByRole("alert")).toHaveTextContent(
        "Show / Hide Right Tool Windows",
      );
    });

    it("Given macOS, When 给底部区域录制 ⌘Q（系统保留）, Then 提示系统保留、绑定不变", async () => {
      const user = userEvent.setup();
      renderProvider("darwin", true);
      await user.click(
        rowOf("Show / Hide Bottom Tool Windows").getByRole("button", {
          name: "Record",
        }),
      );
      press({ key: "q", code: "KeyQ", metaKey: true });
      expect(await screen.findByRole("alert")).toBeInTheDocument();
      expect(screen.getByTestId("label-bottom")).toHaveTextContent("⌘J");
    });

    it("Given Windows, Then 数字键显示为 Alt、终端为 Ctrl+`、专注为 Ctrl+Shift+\\", () => {
      renderProvider("windows", true);
      const section = toolWindowSection();
      expect(section.getByText("Alt+1")).toBeInTheDocument();
      expect(section.getByText("Ctrl+`")).toBeInTheDocument();
      expect(section.getByText("Ctrl+Shift+\\")).toBeInTheDocument();
      expect(section.getByText("Ctrl+0")).toBeInTheDocument();
      expect(section.getByText("Shift+Esc")).toBeInTheDocument();
      expect(section.getByText("Ctrl+J")).toBeInTheDocument();
      expect(section.getByText("Ctrl+Alt+B")).toBeInTheDocument();
    });
  });
});
