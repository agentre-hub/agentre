import { describe, it, expect, vi, beforeEach } from "vitest";
import { createElement, type ReactNode } from "react";
import { renderHook, act } from "@testing-library/react";
import {
  TerminalTransportProvider,
  type TerminalSubscriber,
  type TerminalTransport,
} from "@agentre-ai/agentre-ui";
import { useTerminal } from "../use-terminal";

// 端口替身:记录订阅者并允许用例直接投喂字节/退出,不碰 Wails。
// 传输细节(base64、EventsOn/EventsOff 的扇出)属于桌面适配层,由
// terminal-transport-desktop.test.ts 覆盖。
const subscribers = new Map<string, Set<TerminalSubscriber>>();
const unsubscribeSpy = vi.fn();
let openResult: () => Promise<void> = () => Promise.resolve();

const transport: TerminalTransport = {
  subscribe: vi.fn((terminalId: string, subscriber: TerminalSubscriber) => {
    const bucket = subscribers.get(terminalId) ?? new Set<TerminalSubscriber>();
    subscribers.set(terminalId, bucket);
    bucket.add(subscriber);
    return () => {
      bucket.delete(subscriber);
      unsubscribeSpy(terminalId);
    };
  }),
  open: vi.fn(() => openResult()),
  close: vi.fn(async () => {}),
  write: vi.fn(async () => {}),
  resize: vi.fn(async () => {}),
};

const wrapper = ({ children }: { children: ReactNode }) =>
  createElement(TerminalTransportProvider, { transport, children });

function emitData(terminalId: string, bytes: Uint8Array) {
  for (const subscriber of [...(subscribers.get(terminalId) ?? [])]) {
    subscriber.onData(bytes);
  }
}

function emitExit(
  terminalId: string,
  exit: { code: number; reason: "natural"; msg?: string },
) {
  for (const subscriber of [...(subscribers.get(terminalId) ?? [])]) {
    subscriber.onExit(exit);
  }
}

beforeEach(() => {
  vi.clearAllMocks();
  subscribers.clear();
  openResult = () => Promise.resolve();
});

describe("useTerminal", () => {
  it("Given a live terminal mounts, When the PTY opens, Then it subscribes first and opens with the requested geometry", async () => {
    const { result } = renderHook(
      () =>
        useTerminal({
          terminalID: "t1",
          projectId: 7,
          deviceId: "",
          cols: 80,
          rows: 24,
        }),
      { wrapper },
    );
    await act(async () => {
      await Promise.resolve();
    });

    expect(transport.open).toHaveBeenCalledWith({
      terminalId: "t1",
      projectId: 7,
      deviceId: "",
      cols: 80,
      rows: 24,
    });
    // 先订阅再 open:PTY 可能在 open 的 promise 兑现前就吐出第一帧。
    const subscribeOrder = vi.mocked(transport.subscribe).mock
      .invocationCallOrder[0];
    const openOrder = vi.mocked(transport.open).mock.invocationCallOrder[0];
    expect(subscribeOrder).toBeLessThan(openOrder);
    expect(result.current.state).toBe("open");
  });

  it("Given the PTY emits stdout, When the transport delivers bytes, Then onData receives them unchanged", async () => {
    const onData = vi.fn();
    renderHook(
      () =>
        useTerminal({
          terminalID: "t1",
          projectId: 7,
          deviceId: "",
          cols: 80,
          rows: 24,
          onData,
        }),
      { wrapper },
    );
    await act(async () => {
      await Promise.resolve();
    });

    // 端口按契约交付原始字节;解码是宿主适配层的事,hook 不再插手。
    const bytes = new Uint8Array([0x68, 0x69, 0x20, 0xe2, 0x94, 0x80]);
    act(() => emitData("t1", bytes));

    expect(onData).toHaveBeenCalledTimes(1);
    expect(onData.mock.calls[0][0]).toBe(bytes);
  });

  it("Given the PTY exits, When the exit arrives, Then it reports the exit and goes idle", async () => {
    const onExit = vi.fn();
    const { result } = renderHook(
      () =>
        useTerminal({
          terminalID: "t1",
          projectId: 7,
          deviceId: "",
          cols: 80,
          rows: 24,
          onExit,
        }),
      { wrapper },
    );
    await act(async () => {
      await Promise.resolve();
    });

    act(() => emitExit("t1", { code: 0, reason: "natural" }));

    expect(onExit).toHaveBeenCalledWith({ code: 0, reason: "natural" });
    expect(result.current.state).toBe("idle");
  });

  it("Given a mounted terminal, When it unmounts, Then it drops only its own subscription and closes the PTY", async () => {
    const { unmount } = renderHook(
      () =>
        useTerminal({
          terminalID: "t1",
          projectId: 7,
          deviceId: "",
          cols: 80,
          rows: 24,
        }),
      { wrapper },
    );
    await act(async () => {
      await Promise.resolve();
    });

    unmount();

    expect(transport.close).toHaveBeenCalledWith("t1");
    expect(unsubscribeSpy).toHaveBeenCalledWith("t1");
    expect(subscribers.get("t1")?.size ?? 0).toBe(0);
  });

  it("Given the PTY fails to open, When the rejection lands, Then it surfaces an error exit and goes idle", async () => {
    const onExit = vi.fn();
    openResult = () => Promise.reject(new Error("no such cwd"));
    const { result } = renderHook(
      () =>
        useTerminal({
          terminalID: "t1",
          projectId: 7,
          deviceId: "",
          cols: 80,
          rows: 24,
          onExit,
        }),
      { wrapper },
    );
    await act(async () => {
      await Promise.resolve();
    });

    expect(result.current.state).toBe("idle");
    expect(onExit).toHaveBeenCalledWith(
      expect.objectContaining({ code: -1, reason: "error" }),
    );
  });

  it("Given attach mode, When the hook mounts, Then it neither subscribes nor opens a PTY", async () => {
    renderHook(
      () =>
        useTerminal({
          terminalID: "t1",
          projectId: 7,
          deviceId: "",
          cols: 80,
          rows: 24,
          enabled: false,
        }),
      { wrapper },
    );
    await act(async () => {
      await Promise.resolve();
    });

    expect(transport.subscribe).not.toHaveBeenCalled();
    expect(transport.open).not.toHaveBeenCalled();
  });

  it("Given user keystrokes, When write() is called, Then it proxies to the transport", async () => {
    const { result } = renderHook(
      () =>
        useTerminal({
          terminalID: "t1",
          projectId: 7,
          deviceId: "",
          cols: 80,
          rows: 24,
        }),
      { wrapper },
    );
    await act(async () => {
      await Promise.resolve();
    });
    await act(async () => {
      await result.current.write("ls\n");
    });

    expect(transport.write).toHaveBeenCalledWith("t1", "ls\n");
  });

  it("Given the panel refits, When resize() is called, Then it proxies to the transport", async () => {
    const { result } = renderHook(
      () =>
        useTerminal({
          terminalID: "t1",
          projectId: 7,
          deviceId: "",
          cols: 80,
          rows: 24,
        }),
      { wrapper },
    );
    await act(async () => {
      await Promise.resolve();
    });
    await act(async () => {
      await result.current.resize(100, 40);
    });

    expect(transport.resize).toHaveBeenCalledWith("t1", 100, 40);
  });
});
