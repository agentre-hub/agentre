import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { TerminalTransport } from "./transport";
import {
  TerminalTransportProvider,
  useOptionalTerminalTransport,
  useTerminalTransport,
} from "./transport-context";

function makeTransport(
  overrides: Partial<TerminalTransport> = {},
): TerminalTransport {
  return {
    subscribe: vi.fn(() => () => {}),
    open: vi.fn(async () => {}),
    close: vi.fn(async () => {}),
    write: vi.fn(async () => {}),
    resize: vi.fn(async () => {}),
    ...overrides,
  };
}

function TransportConsumer() {
  const transport = useTerminalTransport();
  return <span>{typeof transport.subscribe}</span>;
}

function CapabilityProbe() {
  const transport = useOptionalTerminalTransport();
  return <span>{transport ? "available" : "unavailable"}</span>;
}

describe("TerminalTransportProvider", () => {
  it("Given a host that wired a terminal transport, When a terminal view mounts, Then it reads that transport from context", () => {
    render(
      <TerminalTransportProvider transport={makeTransport()}>
        <TransportConsumer />
      </TerminalTransportProvider>,
    );

    expect(screen.getByText("function")).toBeTruthy();
  });

  it("Given no transport was provided, When a terminal view mounts anyway, Then it fails loudly at mount instead of showing a dead terminal", () => {
    // 没有「空实现」可选:no-op transport 会开出一个永不吐字节、也永不退出的
    // 终端,和卡死无法区分。装配漏接必须在挂载期炸。
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});

    expect(() => render(<TransportConsumer />)).toThrow(
      /TerminalTransportProvider/,
    );

    spy.mockRestore();
  });

  it("Given a host without terminal capability, When the capability is probed, Then it reports unavailable so the entry point is never rendered", () => {
    // 能力探测语义,对齐 useOptionalPort:宿主没有终端(agentre-server 首版)时
    // 调用方不渲染入口,而不是渲染出来点了没反应。
    render(<CapabilityProbe />);

    expect(screen.getByText("unavailable")).toBeTruthy();
  });

  it("Given a host with terminal capability, When the capability is probed, Then it reports available", () => {
    render(
      <TerminalTransportProvider transport={makeTransport()}>
        <CapabilityProbe />
      </TerminalTransportProvider>,
    );

    expect(screen.getByText("available")).toBeTruthy();
  });
});
