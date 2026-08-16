import { describe, it, expect, vi, beforeEach } from "vitest";
import { createElement, type ReactNode } from "react";
import { renderHook, act } from "@testing-library/react";
import type { Terminal } from "@xterm/xterm";
import {
  TerminalTransportProvider,
  type TerminalTransport,
} from "@agentre-ai/agentre-ui";

import { useAttachedTerminal } from "../use-attached-terminal";
import { useLocalCommandsStore } from "@/stores/local-commands-store";

// 端口替身:attach 模式不该开 / 关 PTY,也不该订阅 —— 那条 PTY 的所有权在起它的
// 那一方(本地命令卡片),这里只是搭个视图上去看。
const transport: TerminalTransport = {
  subscribe: vi.fn(() => () => {}),
  open: vi.fn(async () => {}),
  close: vi.fn(async () => {}),
  write: vi.fn(async () => {}),
  resize: vi.fn(async () => {}),
};

const wrapper = ({ children }: { children: ReactNode }) =>
  createElement(TerminalTransportProvider, { transport, children });

const write = vi.fn();
const xtermRef = {
  current: { write } as unknown as Terminal,
} as React.RefObject<Terminal | null>;

function startCommand(id: string) {
  useLocalCommandsStore
    .getState()
    .start({ id, sessionId: 1, command: "sleep 30", createdAt: 1 });
}

beforeEach(() => {
  vi.clearAllMocks();
  useLocalCommandsStore.setState({ entries: {} });
});

describe("useAttachedTerminal", () => {
  it("Given attach mode, When it mounts, Then it neither subscribes nor opens a PTY", () => {
    startCommand("t1");
    renderHook(
      () => useAttachedTerminal({ terminalID: "t1", xtermRef, enabled: true }),
      { wrapper },
    );

    expect(transport.subscribe).not.toHaveBeenCalled();
    expect(transport.open).not.toHaveBeenCalled();
  });

  it("Given the card already collected output, When the view attaches, Then it seeds the backlog once and afterwards writes only the delta", () => {
    startCommand("t1");
    act(() => useLocalCommandsStore.getState().appendOutput("t1", "boot\n"));

    renderHook(
      () => useAttachedTerminal({ terminalID: "t1", xtermRef, enabled: true }),
      { wrapper },
    );
    expect(write).toHaveBeenCalledTimes(1);
    expect(write).toHaveBeenCalledWith("boot\n");

    act(() => useLocalCommandsStore.getState().appendOutput("t1", "more\n"));

    expect(write).toHaveBeenCalledTimes(2);
    expect(write).toHaveBeenLastCalledWith("more\n");
  });

  it("Given the user types into an attached terminal, When write() is called, Then it goes through the transport", async () => {
    startCommand("t1");
    const { result } = renderHook(
      () => useAttachedTerminal({ terminalID: "t1", xtermRef, enabled: true }),
      { wrapper },
    );

    await act(async () => {
      await result.current.write("ls\n");
    });

    expect(transport.write).toHaveBeenCalledWith("t1", "ls\n");
  });

  it("Given the attached view refits, When resize() is called, Then it goes through the transport", async () => {
    startCommand("t1");
    const { result } = renderHook(
      () => useAttachedTerminal({ terminalID: "t1", xtermRef, enabled: true }),
      { wrapper },
    );

    await act(async () => {
      await result.current.resize(100, 40);
    });

    expect(transport.resize).toHaveBeenCalledWith("t1", 100, 40);
  });

  it("Given the hook is disabled, When the card appends output, Then nothing reaches xterm", () => {
    startCommand("t1");
    renderHook(
      () => useAttachedTerminal({ terminalID: "t1", xtermRef, enabled: false }),
      { wrapper },
    );

    act(() => useLocalCommandsStore.getState().appendOutput("t1", "boot\n"));

    expect(write).not.toHaveBeenCalled();
  });
});
