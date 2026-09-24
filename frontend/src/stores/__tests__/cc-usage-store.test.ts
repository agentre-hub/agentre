import { act } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import type { UsageState } from "../cc-usage-store";
import { useCCUsageStore } from "../cc-usage-store";

const sample = (overrides: Partial<{ fiveHourPercent: number }> = {}) =>
  ({
    reason: "ok",
    data: {
      fiveHourPercent: 42,
      weeklyPercent: 18,
      ...overrides,
    },
    stale: false,
    fetchedAtMs: 1_000_000,
  }) as UsageState;

describe("cc-usage-store", () => {
  beforeEach(() => {
    useCCUsageStore.getState().__reset();
  });

  it("upsert 写入 / Get 读出 同一 deviceKey", () => {
    act(() => {
      useCCUsageStore.getState().upsert("local", sample());
    });
    const got = useCCUsageStore.getState().byDevice.get("local");
    expect(got?.reason).toBe("ok");
    expect(got?.data?.fiveHourPercent).toBe(42);
  });

  it("不同 deviceKey 各自独立", () => {
    act(() => {
      useCCUsageStore
        .getState()
        .upsert("local", sample({ fiveHourPercent: 1 }));
      useCCUsageStore
        .getState()
        .upsert("remote:7", sample({ fiveHourPercent: 99 }));
    });
    const m = useCCUsageStore.getState().byDevice;
    expect(m.get("local")?.data?.fiveHourPercent).toBe(1);
    expect(m.get("remote:7")?.data?.fiveHourPercent).toBe(99);
  });

  it("同值短路: 写入语义相同的 state 不换 Map 引用", () => {
    act(() => {
      useCCUsageStore.getState().upsert("local", sample());
    });
    const before = useCCUsageStore.getState().byDevice;
    act(() => {
      // 引用不同但 deep-equal 相同
      useCCUsageStore.getState().upsert("local", sample());
    });
    const after = useCCUsageStore.getState().byDevice;
    expect(after).toBe(before);
  });

  it("同值短路: fetchedAtMs 变了但数值全同, 仍不换 Map 引用", () => {
    // 真实场景:60s ticker 每次 probe 都写新的 fetchedAtMs(manager.go 里 nowMs),
    // 但配额百分比常常一动不动。此时不该让所有订阅者集体 re-render。
    act(() => {
      useCCUsageStore.getState().upsert("local", sample());
    });
    const before = useCCUsageStore.getState().byDevice;
    act(() => {
      useCCUsageStore
        .getState()
        .upsert("local", { ...sample(), fetchedAtMs: 1_060_000 } as UsageState);
    });
    const after = useCCUsageStore.getState().byDevice;
    expect(after).toBe(before);
  });

  it("不同值时换 Map 引用", () => {
    act(() => {
      useCCUsageStore
        .getState()
        .upsert("local", sample({ fiveHourPercent: 10 }));
    });
    const before = useCCUsageStore.getState().byDevice;
    act(() => {
      useCCUsageStore
        .getState()
        .upsert("local", sample({ fiveHourPercent: 11 }));
    });
    const after = useCCUsageStore.getState().byDevice;
    expect(after).not.toBe(before);
  });

  it("同值短路: 模型周配额数组引用不同但内容相同, 不换 Map 引用", () => {
    const withFable = (percent: number) =>
      ({
        ...sample(),
        data: {
          ...sample().data,
          modelWeekly: [
            { model: "Fable", percent, resetsAt: "2026-09-24T12:00:00Z" },
          ],
        },
      }) as UsageState;
    act(() => {
      useCCUsageStore.getState().upsert("local", withFable(3));
    });
    const before = useCCUsageStore.getState().byDevice;
    act(() => {
      useCCUsageStore.getState().upsert("local", withFable(3));
    });
    expect(useCCUsageStore.getState().byDevice).toBe(before);
    act(() => {
      useCCUsageStore.getState().upsert("local", withFable(5));
    });
    expect(useCCUsageStore.getState().byDevice).not.toBe(before);
  });

  it("remove 删除指定 key", () => {
    act(() => {
      useCCUsageStore.getState().upsert("local", sample());
      useCCUsageStore.getState().upsert("remote:1", sample());
    });
    act(() => {
      useCCUsageStore.getState().remove("local");
    });
    const m = useCCUsageStore.getState().byDevice;
    expect(m.has("local")).toBe(false);
    expect(m.has("remote:1")).toBe(true);
  });

  it("reason='' 视为未拉过 (未首次 probe 的占位)", () => {
    act(() => {
      useCCUsageStore.getState().upsert("local", {
        reason: "",
        fetchedAtMs: 0,
      } as UsageState);
    });
    const got = useCCUsageStore.getState().byDevice.get("local");
    expect(got?.reason).toBe("");
  });
});
