import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import { useSessionStatusStore } from "@/stores/session-status-store";

import { useEffectiveSessionStatus } from "./use-live-session-status";

describe("useEffectiveSessionStatus", () => {
  beforeEach(() => {
    useSessionStatusStore.getState().__reset();
  });

  // store 里没该 sid 时返回 fallback。会话列表里大多数 session 没有运行时态,
  // 必须保持 DB 快照不变。
  it("returns fallback when store has no entry", () => {
    const { result } = renderHook(() =>
      useEffectiveSessionStatus(42, {
        agentStatus: "idle",
        needsAttention: false,
      }),
    );
    expect(result.current).toEqual({
      agentStatus: "idle",
      needsAttention: false,
    });
  });

  // store 有运行时态时覆盖 fallback —— 不论该值是不是真比 fallback 新, 默认
  // store 是事实, 调用方再决定怎么合并。
  it("overlays session-status-store when entry exists", () => {
    const { result, rerender } = renderHook(() =>
      useEffectiveSessionStatus(7, {
        agentStatus: "running",
        needsAttention: false,
      }),
    );
    expect(result.current.agentStatus).toBe("running");

    act(() => {
      useSessionStatusStore.getState().upsert(7, {
        agentStatus: "waiting",
        needsAttention: true,
      });
    });
    rerender();

    expect(result.current).toEqual({
      agentStatus: "waiting",
      needsAttention: true,
    });
  });

  // store 里手动 remove 后 hook 必须 fallback 回快照，避免一直挂在 waiting。
  it("falls back when entry is removed", () => {
    act(() => {
      useSessionStatusStore.getState().upsert(8, {
        agentStatus: "waiting",
        needsAttention: true,
      });
    });

    const { result, rerender } = renderHook(() =>
      useEffectiveSessionStatus(8, {
        agentStatus: "idle",
        needsAttention: false,
      }),
    );
    expect(result.current.agentStatus).toBe("waiting");

    act(() => {
      useSessionStatusStore.getState().remove(8);
    });
    rerender();

    expect(result.current).toEqual({
      agentStatus: "idle",
      needsAttention: false,
    });
  });
});
