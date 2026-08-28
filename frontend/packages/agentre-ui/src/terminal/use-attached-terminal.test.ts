import { describe, it, expect, vi, beforeEach } from "vitest";
import { createElement, type ReactNode } from "react";
import { renderHook, act } from "@testing-library/react";
import type { Terminal } from "@xterm/xterm";

import { LocalCommandsProvider } from "../transcript/local-command/access";
import {
  createFakeLocalCommands,
  type FakeLocalCommands,
} from "../transcript/local-command/__testing__/fake-local-commands";

import type { TerminalTransport } from "./transport";
import { TerminalTransportProvider } from "./transport-context";
import { useAttachedTerminal } from "./use-attached-terminal";

// 端口替身:attach 模式不该开 / 关 PTY,也不该订阅传输 —— 那条 PTY 的所有权在起它的
// 那一方(本地命令卡片),这里只是搭个视图上去看。
const transport: TerminalTransport = {
  subscribe: vi.fn(() => () => {}),
  open: vi.fn(async () => {}),
  close: vi.fn(async () => {}),
  write: vi.fn(async () => {}),
  resize: vi.fn(async () => {}),
};

let commands: FakeLocalCommands;

const wrapper = ({ children }: { children: ReactNode }) =>
  createElement(TerminalTransportProvider, {
    transport,
    children: createElement(LocalCommandsProvider, {
      access: commands.access,
      children,
    }),
  });

const write = vi.fn();
const xtermRef = {
  current: { write } as unknown as Terminal,
} as React.RefObject<Terminal | null>;

beforeEach(() => {
  vi.clearAllMocks();
  commands = createFakeLocalCommands();
});

describe("useAttachedTerminal", () => {
  it("Given attach mode, When it mounts, Then it neither subscribes the transport nor opens a PTY", () => {
    commands.start({ id: "t1", command: "sleep 30" });
    renderHook(
      () => useAttachedTerminal({ terminalID: "t1", xtermRef, enabled: true }),
      { wrapper },
    );

    expect(transport.subscribe).not.toHaveBeenCalled();
    expect(transport.open).not.toHaveBeenCalled();
  });

  it("Given the card already collected output, When the view attaches, Then it seeds the backlog once and afterwards writes only the delta", () => {
    commands.start({ id: "t1", command: "sleep 30" });
    act(() => commands.appendOutput("t1", "boot\n"));

    renderHook(
      () => useAttachedTerminal({ terminalID: "t1", xtermRef, enabled: true }),
      { wrapper },
    );
    expect(write).toHaveBeenCalledTimes(1);
    expect(write).toHaveBeenCalledWith("boot\n");

    act(() => commands.appendOutput("t1", "more\n"));

    expect(write).toHaveBeenCalledTimes(2);
    expect(write).toHaveBeenLastCalledWith("more\n");
  });

  it("Given the user types into an attached terminal, When write() is called, Then it goes through the transport", async () => {
    commands.start({ id: "t1", command: "sleep 30" });
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
    commands.start({ id: "t1", command: "sleep 30" });
    const { result } = renderHook(
      () => useAttachedTerminal({ terminalID: "t1", xtermRef, enabled: true }),
      { wrapper },
    );

    await act(async () => {
      await result.current.resize(100, 40);
    });

    expect(transport.resize).toHaveBeenCalledWith("t1", 100, 40);
  });

  it("Given a host without local-command capability, When a live terminal mounts with attach off, Then it needs no local-command seam at all", () => {
    // live 模式的 PTY 与本地命令无关。若这条数据源用会炸的取用口,一个没有本地
    // 命令能力的宿主(agentre-server)连开个普通终端都会在挂载期炸。
    expect(() =>
      renderHook(
        () =>
          useAttachedTerminal({ terminalID: "t1", xtermRef, enabled: false }),
        {
          wrapper: ({ children }: { children: ReactNode }) =>
            createElement(TerminalTransportProvider, { transport, children }),
        },
      ),
    ).not.toThrow();
  });

  it("Given the hook is disabled, When the card appends output, Then nothing reaches xterm", () => {
    commands.start({ id: "t1", command: "sleep 30" });
    renderHook(
      () => useAttachedTerminal({ terminalID: "t1", xtermRef, enabled: false }),
      { wrapper },
    );

    act(() => commands.appendOutput("t1", "boot\n"));

    expect(write).not.toHaveBeenCalled();
  });
});
