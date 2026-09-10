import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { renderHook, act } from "@testing-library/react";

vi.mock("../../../../wailsjs/go/app/App", () => ({
  RemoteDeviceUpgrade: vi.fn(),
  RemoteDeviceGet: vi.fn(),
}));

import {
  RemoteDeviceUpgrade,
  RemoteDeviceGet,
} from "../../../../wailsjs/go/app/App";
import { useDeviceUpgrade } from "./use-device-upgrade";

const mockUpgrade = RemoteDeviceUpgrade as unknown as ReturnType<typeof vi.fn>;
const mockGet = RemoteDeviceGet as unknown as ReturnType<typeof vi.fn>;

beforeEach(() => {
  mockUpgrade.mockReset();
  mockGet.mockReset();
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

// 状态机本身（requesting/upgrading/success/timeout 的迁移、闸门、attempt 作废）归
// 共享包的 use-agentred-upgrade.test.ts —— 这里只证明桌面端这一侧接对了：调用发给
// 哪个绑定、带什么参数，版本从远端快照的哪个字段上读。
describe("useDeviceUpgrade", () => {
  it("enters the upgrading state once the daemon accepts the call", async () => {
    mockUpgrade.mockResolvedValue({ accepted: true, targetVersion: "0.6.0" });
    const { result } = renderHook(() => useDeviceUpgrade(42, "0.5.2"));

    expect(result.current.phase.kind).toBe("idle");

    await act(async () => {
      result.current.start();
      await vi.advanceTimersByTimeAsync(0);
    });

    expect(result.current.phase.kind).toBe("upgrading");
    expect(mockUpgrade).toHaveBeenCalledWith(42, "", false);
  });

  it("resolves to success once a poll reports a different version", async () => {
    mockUpgrade.mockResolvedValue({ accepted: true, targetVersion: "0.6.0" });
    mockGet.mockResolvedValue({ daemonVersion: "0.6.0" });
    const { result } = renderHook(() => useDeviceUpgrade(42, "0.5.2"));

    await act(async () => {
      result.current.start();
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(result.current.phase.kind).toBe("upgrading");

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5_000);
    });

    expect(result.current.phase).toEqual({
      kind: "success",
      fromVersion: "0.5.2",
      toVersion: "0.6.0",
    });
  });

  it("on active-turns rejection, keeps the action available and only sends force after an explicit confirm", async () => {
    mockUpgrade.mockResolvedValueOnce({
      accepted: false,
      rejectReason: "active_turns",
      message:
        "this machine has 2 running conversation(s); upgrading would interrupt them",
      activeTurns: 2,
    });
    const { result } = renderHook(() => useDeviceUpgrade(42, "0.5.2"));

    await act(async () => {
      result.current.start();
      await vi.advanceTimersByTimeAsync(0);
    });

    expect(result.current.phase).toEqual({
      kind: "active-turns",
      message:
        "this machine has 2 running conversation(s); upgrading would interrupt them",
      activeTurns: 2,
      confirmOpen: false,
    });
    // 尚未确认:不该有第二次调用带着 force 出去。
    expect(mockUpgrade).toHaveBeenCalledTimes(1);

    act(() => result.current.requestForce());
    expect(result.current.phase.kind).toBe("active-turns");
    if (result.current.phase.kind === "active-turns") {
      expect(result.current.phase.confirmOpen).toBe(true);
    }
    // 打开确认对话框本身不触发调用。
    expect(mockUpgrade).toHaveBeenCalledTimes(1);

    mockUpgrade.mockResolvedValueOnce({
      accepted: true,
      targetVersion: "0.6.0",
    });
    await act(async () => {
      result.current.confirmForce();
      await vi.advanceTimersByTimeAsync(0);
    });

    expect(mockUpgrade).toHaveBeenCalledTimes(2);
    expect(mockUpgrade).toHaveBeenLastCalledWith(42, "", true);
    expect(result.current.phase.kind).toBe("upgrading");
  });
});
