import { renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { useTranscriptCallbacks } from "../use-transcript-callbacks";

// 子代理卡的停止进行态只认宿主回调返回的 Promise(A4);稳定代理必须把它原样交还,
// 吞掉就等于让卡片永远拿不到「失败」信号。
describe("useTranscriptCallbacks · stableOnStopSubagent", () => {
  it("Given a host stop that rejects, When the stable proxy is called, Then the same rejection reaches the card", async () => {
    const failure = new Error("stop failed");
    const { result } = renderHook(() =>
      useTranscriptCallbacks({
        onStopSubagent: () => Promise.reject(failure),
      }),
    );
    await expect(result.current.stableOnStopSubagent("toolu-1")).rejects.toBe(
      failure,
    );
  });

  it("Given no host stop, When the stable proxy is called, Then it returns nothing", () => {
    const { result } = renderHook(() => useTranscriptCallbacks({}));
    expect(result.current.stableOnStopSubagent("toolu-1")).toBeUndefined();
  });
});
