import { describe, it, expect, vi, beforeEach } from "vitest";
import type { TerminalExit } from "@agentre-ai/agentre-ui";

vi.mock("@/../wailsjs/go/app/App", () => ({
  TerminalOpen: vi.fn().mockResolvedValue(undefined),
  TerminalWrite: vi.fn().mockResolvedValue(undefined),
  TerminalResize: vi.fn().mockResolvedValue(undefined),
  TerminalClose: vi.fn().mockResolvedValue(undefined),
}));

// 复刻 Wails v2 事件运行时的两条关键语义(见 wails v2.12 desktop/events.js):
//   - `EventsOn` 返回的取消函数**只摘自己**那一个监听者;
//   - `EventsOff(event)` 把该事件名下的**所有**监听者一起摘掉 —— 这正是适配层
//     必须封住的坑,所以替身必须忠实复刻它,否则回归用例根本测不到东西。
type Handler = (payload: never) => void;
const listeners = new Map<string, Set<Handler>>();

vi.mock("@/../wailsjs/runtime/runtime", () => ({
  EventsOn: vi.fn((event: string, handler: Handler) => {
    const bucket = listeners.get(event) ?? new Set<Handler>();
    listeners.set(event, bucket);
    bucket.add(handler);
    return () => {
      bucket.delete(handler);
      if (bucket.size === 0) listeners.delete(event);
    };
  }),
  EventsOff: vi.fn((event: string) => {
    listeners.delete(event);
  }),
}));

import * as App from "@/../wailsjs/go/app/App";
import { desktopTerminalTransport } from "../terminal-transport-desktop";

function emit(event: string, payload: unknown) {
  for (const handler of [...(listeners.get(event) ?? [])]) {
    handler(payload as never);
  }
}

function listenerCount(event: string): number {
  return listeners.get(event)?.size ?? 0;
}

function makeSubscriber() {
  return {
    onData: vi.fn<(bytes: Uint8Array) => void>(),
    onExit: vi.fn<(exit: TerminalExit) => void>(),
  };
}

// base64 of [0x68,0x69,0x20,0xe2,0x94,0x80] = "hi ─" —— 制表符 '─' 是 3 字节
// (E2 94 80),必须原样以字节抵达 xterm,不能被解成 UTF-16 字符串。
const BOX_DRAW_B64 = btoa(
  String.fromCharCode(0x68, 0x69, 0x20, 0xe2, 0x94, 0x80),
);
const BOX_DRAW_BYTES = [0x68, 0x69, 0x20, 0xe2, 0x94, 0x80];

beforeEach(() => {
  vi.clearAllMocks();
  listeners.clear();
});

describe("desktopTerminalTransport", () => {
  it("Given two views subscribed to one terminal, When stdout arrives, Then both get the same decoded bytes through a single Wails listener", () => {
    const a = makeSubscriber();
    const b = makeSubscriber();
    const offA = desktopTerminalTransport.subscribe("multi-1", a);
    const offB = desktopTerminalTransport.subscribe("multi-1", b);

    // 一个 Wails 监听者扇出给 N 个订阅者 —— 每个订阅者各注册一次的话,
    // 任何一次 EventsOff 都会把别人的监听一并摘掉。
    expect(listenerCount("terminal:multi-1:data")).toBe(1);

    emit("terminal:multi-1:data", { data: BOX_DRAW_B64 });

    for (const sub of [a, b]) {
      expect(sub.onData).toHaveBeenCalledTimes(1);
      const bytes = sub.onData.mock.calls[0][0];
      expect(bytes).toBeInstanceOf(Uint8Array);
      expect(Array.from(bytes)).toEqual(BOX_DRAW_BYTES);
    }

    offA();
    offB();
  });

  it("Given two subscribers on one terminal, When one unsubscribes, Then the other keeps receiving data", () => {
    const a = makeSubscriber();
    const b = makeSubscriber();
    const offA = desktopTerminalTransport.subscribe("multi-2", a);
    const offB = desktopTerminalTransport.subscribe("multi-2", b);

    offA();
    emit("terminal:multi-2:data", { data: BOX_DRAW_B64 });

    expect(a.onData).not.toHaveBeenCalled();
    expect(b.onData).toHaveBeenCalledTimes(1);

    offB();
  });

  it("Given every subscriber left, When the last one unsubscribes, Then the Wails listeners are released", () => {
    const a = makeSubscriber();
    const off = desktopTerminalTransport.subscribe("multi-3", a);

    expect(listenerCount("terminal:multi-3:data")).toBe(1);
    expect(listenerCount("terminal:multi-3:exit")).toBe(1);

    off();

    expect(listenerCount("terminal:multi-3:data")).toBe(0);
    expect(listenerCount("terminal:multi-3:exit")).toBe(0);
  });

  it("Given a foreign listener on the same terminal event, When the transport's last subscriber unsubscribes, Then the foreign listener survives", () => {
    // 回归用例:`EventsOff(event)` 会摘掉该事件的全部监听者,包括 chat-panel
    // 自己注册的那一对。端口的退订必须只摘自己,否则本地命令卡片的输出会
    // 因为某个终端面板卸载而突然断流。
    const foreign = vi.fn();
    listeners.set("terminal:shared:data", new Set([foreign]));

    const mine = makeSubscriber();
    const off = desktopTerminalTransport.subscribe("shared", mine);
    off();

    emit("terminal:shared:data", { data: BOX_DRAW_B64 });

    expect(foreign).toHaveBeenCalledTimes(1);
    expect(mine.onData).not.toHaveBeenCalled();
  });

  it("Given a subscriber already unsubscribed, When its handle is called again, Then the remaining subscribers are untouched", () => {
    const a = makeSubscriber();
    const b = makeSubscriber();
    const offA = desktopTerminalTransport.subscribe("multi-4", a);
    desktopTerminalTransport.subscribe("multi-4", b);

    offA();
    offA();

    emit("terminal:multi-4:data", { data: BOX_DRAW_B64 });
    expect(b.onData).toHaveBeenCalledTimes(1);
  });

  it("Given the PTY exits, When the exit event arrives, Then every subscriber is told", () => {
    const a = makeSubscriber();
    const b = makeSubscriber();
    desktopTerminalTransport.subscribe("multi-5", a);
    desktopTerminalTransport.subscribe("multi-5", b);

    emit("terminal:multi-5:exit", { code: 2, reason: "natural" });

    for (const sub of [a, b]) {
      expect(sub.onExit).toHaveBeenCalledWith({ code: 2, reason: "natural" });
    }
  });

  it("Given a fresh subscription after the last one left, When stdout arrives, Then it is delivered again", () => {
    const first = makeSubscriber();
    const off = desktopTerminalTransport.subscribe("multi-6", first);
    off();

    const second = makeSubscriber();
    desktopTerminalTransport.subscribe("multi-6", second);
    emit("terminal:multi-6:data", { data: BOX_DRAW_B64 });

    expect(second.onData).toHaveBeenCalledTimes(1);
  });

  it("Given a terminal request, When the transport is driven, Then each action proxies to its Wails binding", async () => {
    await desktopTerminalTransport.open({
      terminalId: "t1",
      projectId: 7,
      deviceId: "",
      cols: 80,
      rows: 24,
    });
    await desktopTerminalTransport.write("t1", "ls\n");
    await desktopTerminalTransport.resize("t1", 100, 40);
    await desktopTerminalTransport.close("t1");

    expect(App.TerminalOpen).toHaveBeenCalledWith("t1", 7, "", 80, 24);
    expect(App.TerminalWrite).toHaveBeenCalledWith("t1", "ls\n");
    expect(App.TerminalResize).toHaveBeenCalledWith("t1", 100, 40);
    expect(App.TerminalClose).toHaveBeenCalledWith("t1");
  });
});
