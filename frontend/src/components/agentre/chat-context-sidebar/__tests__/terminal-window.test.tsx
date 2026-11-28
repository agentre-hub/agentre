import "@testing-library/jest-dom/vitest";

import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/../wailsjs/runtime/runtime", async () => ({
  ...(await vi.importActual("@/../wailsjs/runtime/runtime")),
  EventsOn: vi.fn(() => () => {}),
  EventsOff: vi.fn(),
}));

vi.mock("@/../wailsjs/go/app/App", () => ({
  WorkspaceFsWorkRoots: () => Promise.resolve([]),
  WorkspaceFsGitState: () => Promise.resolve({ notARepo: true }),
  WorkspaceFsGitChanges: () =>
    Promise.resolve({ notARepo: true, changes: [], baseRef: "" }),
  WorkspaceFsListDir: () =>
    Promise.resolve({ path: "/repo", entries: [], truncated: false }),
  GetAppSetting: () => Promise.reject(new Error("nf")),
  TerminalOpenForSession: vi.fn().mockResolvedValue(undefined),
  TerminalClose: vi.fn().mockResolvedValue(undefined),
}));
vi.mock("../../../../../wailsjs/go/app/App", () => ({
  GetAppSetting: () => Promise.reject(new Error("nf")),
  UpdateAppSettings: vi.fn(),
}));

import { useSessionTerminalsStore } from "@/stores/session-terminals-store";

import { ChatContextToolWindows } from "../index";

import { showOnly, stripButton } from "./tool-window-helpers";

function renderWindows(sessionId = 1) {
  return render(
    <ChatContextToolWindows
      sessionId={sessionId}
      messages={[]}
      activeMessageId={null}
      onJumpToMessage={() => {}}
      cwd="/repo"
    />,
  );
}

describe("终端工具窗的注册", () => {
  beforeEach(() => {
    localStorage.clear();
    showOnly();
    useSessionTerminalsStore.setState({ bySession: {}, viewing: {} });
  });

  // Given 本会话没有终端进程, Then 图标条上有「终端」图标但没有圆点。
  it("puts a Terminal icon on the strip, dotless without a live process", () => {
    renderWindows();
    expect(
      within(stripButton("Terminal")).queryByTestId("tool-window-running-dot"),
    ).toBeNull();
  });

  // Given 本会话有存活终端, Then 图标上出现运行中圆点；别的会话的终端不算。
  it("shows the running dot while this session has a live terminal", () => {
    renderWindows(1);
    act(() => {
      useSessionTerminalsStore.getState().openInCurrentRoot(2);
    });
    expect(
      within(stripButton("Terminal")).queryByTestId("tool-window-running-dot"),
    ).toBeNull();

    act(() => {
      useSessionTerminalsStore.getState().openInCurrentRoot(1);
    });
    expect(
      within(stripButton("Terminal")).getByTestId("tool-window-running-dot"),
    ).toBeTruthy();
  });

  // Given 终端进程已退出, Then 圆点熄灭。
  it("drops the dot once the process exited", () => {
    renderWindows(1);
    let id = "";
    act(() => {
      id = useSessionTerminalsStore.getState().openInCurrentRoot(1);
    });
    act(() => useSessionTerminalsStore.getState().markExited(1, id));
    expect(
      within(stripButton("Terminal")).queryByTestId("tool-window-running-dot"),
    ).toBeNull();
  });

  // When 点图标, Then 终端窗在底部显示,里面能新建终端。
  it("opens the terminal window from its icon", async () => {
    renderWindows();
    await userEvent.setup().click(stripButton("Terminal"));
    const region = screen.getByRole("region", { name: "Terminal" });
    expect(
      within(region).getByRole("button", { name: "New terminal" }),
    ).toBeTruthy();
  });
});
