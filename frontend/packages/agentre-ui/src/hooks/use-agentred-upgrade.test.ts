import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  useAgentredUpgrade,
  type AgentredUpgradeAcceptance,
  type AgentredUpgradePorts,
} from "./use-agentred-upgrade";

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

/** 一次受理调用的答复；不设就是「受理了，目标 0.6.0」。 */
function ports(overrides: Partial<AgentredUpgradePorts> = {}) {
  const requestUpgrade = vi.fn(
    async (): Promise<AgentredUpgradeAcceptance> => ({
      accepted: true,
      targetVersion: "0.6.0",
    }),
  );
  const readVersion = vi.fn(async (): Promise<string | null> => null);
  return { requestUpgrade, readVersion, ...overrides };
}

async function flush() {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(0);
  });
}

describe("useAgentredUpgrade", () => {
  it("点下去就进 requesting：受理判定要跑上几分钟，这段时间界面不能一声不吭", async () => {
    // daemon 把解析发布、下载、校验、替换全部跑完才应答（本端预算 5 分钟）。停在
    // idle 等于「点了没反应」，用户会再点一次，而第二次调用撞上那台机器的并发闸门
    // 拿回「已经有一次升级在跑」——那是界面自己制造出来的失败。
    let accept: (reply: AgentredUpgradeAcceptance) => void = () => {};
    const p = ports({
      requestUpgrade: vi.fn(
        () =>
          new Promise<AgentredUpgradeAcceptance>((resolve) => {
            accept = resolve;
          }),
      ),
    });
    const { result } = renderHook(() => useAgentredUpgrade("0.5.2", p));

    expect(result.current.phase.kind).toBe("idle");

    act(() => result.current.start());
    expect(result.current.phase.kind).toBe("requesting");

    // 应答回来才接上「升级中」。
    await act(async () => {
      accept({ accepted: true, targetVersion: "0.6.0" });
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(result.current.phase).toEqual({
      kind: "upgrading",
      fromVersion: "0.5.2",
      targetVersion: "0.6.0",
    });
    expect(p.requestUpgrade).toHaveBeenCalledWith(false);
  });

  it("还在 requesting 时再点：一次调用都不再发出去", async () => {
    const p = ports({
      requestUpgrade: vi.fn(
        () => new Promise<AgentredUpgradeAcceptance>(() => {}),
      ),
    });
    const { result } = renderHook(() => useAgentredUpgrade("0.5.2", p));

    act(() => result.current.start());
    act(() => result.current.start());
    act(() => result.current.confirmForce());

    expect(p.requestUpgrade).toHaveBeenCalledTimes(1);
    expect(result.current.phase.kind).toBe("requesting");
  });

  it("轮询读到的版本变了就是成功", async () => {
    const p = ports({ readVersion: vi.fn(async () => "0.6.0") });
    const { result } = renderHook(() => useAgentredUpgrade("0.5.2", p));

    act(() => result.current.start());
    await flush();
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

  it("5 分钟没等到版本变化：按超时失败收场，探测失败本身不提前判死", async () => {
    const p = ports({
      // daemon 正在重启：轮询期间取数失败是这段时间的常态。
      readVersion: vi.fn(async () => {
        throw new Error("dial failed");
      }),
    });
    const { result } = renderHook(() => useAgentredUpgrade("0.5.2", p));

    act(() => result.current.start());
    await flush();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(4 * 60_000 + 59_000);
    });
    expect(result.current.phase.kind).toBe("upgrading");

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2_000);
    });
    expect(result.current.phase.kind).toBe("timeout");
  });

  it("活跃轮次拒绝：原话照传，force 只在显式确认之后才出去", async () => {
    const wording =
      "this machine has 2 running conversation(s); upgrading would interrupt them";
    const requestUpgrade = vi
      .fn<(force: boolean) => Promise<AgentredUpgradeAcceptance>>()
      .mockResolvedValueOnce({
        accepted: false,
        rejectReason: "active_turns",
        message: wording,
        activeTurns: 2,
      })
      .mockResolvedValueOnce({ accepted: true, targetVersion: "0.6.0" });
    const p = ports({ requestUpgrade });
    const { result } = renderHook(() => useAgentredUpgrade("0.5.2", p));

    act(() => result.current.start());
    await flush();
    expect(result.current.phase).toEqual({
      kind: "active-turns",
      message: wording,
      activeTurns: 2,
      confirmOpen: false,
    });

    // 打开确认本身不发调用：重试绝不能被读成默许。
    act(() => result.current.requestForce());
    expect(requestUpgrade).toHaveBeenCalledTimes(1);
    if (result.current.phase.kind === "active-turns") {
      expect(result.current.phase.confirmOpen).toBe(true);
    }

    act(() => result.current.cancelForce());
    if (result.current.phase.kind === "active-turns") {
      expect(result.current.phase.confirmOpen).toBe(false);
    }

    await act(async () => {
      result.current.confirmForce();
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(requestUpgrade).toHaveBeenLastCalledWith(true);
    expect(result.current.phase.kind).toBe("upgrading");
  });

  it("其它拒绝与调用本身失败都落 failed，message 原样透传", async () => {
    const p = ports({
      requestUpgrade: vi.fn(async () => ({
        accepted: false,
        rejectReason: "download_failed",
        message: "resolve release: TLS handshake timeout",
      })),
    });
    const { result } = renderHook(() => useAgentredUpgrade("0.5.2", p));

    act(() => result.current.start());
    await flush();
    expect(result.current.phase).toEqual({
      kind: "failed",
      message: "resolve release: TLS handshake timeout",
    });

    const broken = ports({
      requestUpgrade: vi.fn(async () => {
        throw new Error("relay is offline");
      }),
    });
    const { result: second } = renderHook(() =>
      useAgentredUpgrade("0.5.2", broken),
    );
    act(() => second.current.start());
    await flush();
    expect(second.current.phase).toEqual({
      kind: "failed",
      message: "relay is offline",
    });
  });

  it("卸载之后不再落状态，轮询也停掉", async () => {
    const p = ports({ readVersion: vi.fn(async () => "0.6.0") });
    const { result, unmount } = renderHook(() =>
      useAgentredUpgrade("0.5.2", p),
    );

    act(() => result.current.start());
    await flush();
    unmount();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000);
    });
    expect(p.readVersion).not.toHaveBeenCalled();
  });
});
