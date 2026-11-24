import { fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it } from "vitest";

import {
  DEFAULT_DIVIDER_RATIOS,
  DEFAULT_TOOL_WINDOW_MODES,
  DEFAULT_TOOL_WINDOW_SLOTS,
  useToolWindowLayout,
  type ToolWindowId,
} from "@/stores/tool-window-layout-store";

import type { DesktopPlatform } from "../../chrome";
import { KeyboardShortcutsPanel } from "../keyboard-shortcuts-panel";
import { REGISTRY } from "../registry";
import { ShortcutsProvider } from "../shortcuts-provider";

function renderProvider(platform: DesktopPlatform, panel = false) {
  return render(
    <MemoryRouter>
      <ShortcutsProvider platform={platform}>
        {panel ? <KeyboardShortcutsPanel /> : null}
      </ShortcutsProvider>
    </MemoryRouter>,
  );
}

function isShown(id: ToolWindowId) {
  const s = useToolWindowLayout.getState();
  return s.shown[s.slotOf[id]] === id;
}

function press(init: KeyboardEventInit) {
  fireEvent.keyDown(window, init);
}

describe("工具窗快捷键", () => {
  beforeEach(() => {
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
      focusSnapshot: null,
      maximized: null,
    });
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

  it("Then 工具窗快捷键都登记为不可改绑", () => {
    const defs = REGISTRY.filter((d) => d.scope === "toolWindows");
    // 七个工具窗切换 + 专注 + ⇧Esc + Esc + ⇧⌘' + ⌘0。
    expect(defs).toHaveLength(12);
    expect(defs.every((d) => !d.rebindable)).toBe(true);
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
      expect(section.queryByRole("button", { name: "Record" })).toBeNull();
    });

    it("Given Windows, Then 数字键显示为 Alt、终端为 Ctrl+`、专注为 Ctrl+Shift+\\", () => {
      renderProvider("windows", true);
      const section = toolWindowSection();
      expect(section.getByText("Alt+1")).toBeInTheDocument();
      expect(section.getByText("Ctrl+`")).toBeInTheDocument();
      expect(section.getByText("Ctrl+Shift+\\")).toBeInTheDocument();
      expect(section.getByText("Ctrl+0")).toBeInTheDocument();
      expect(section.getByText("Shift+Esc")).toBeInTheDocument();
    });
  });
});
