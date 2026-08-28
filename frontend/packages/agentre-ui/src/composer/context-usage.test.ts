import { describe, expect, it } from "vitest";

import { computeContextUsage } from "./context-usage";

/**
 * 判据取自搬迁前**桌面端 `computeComposerContextUsage` 的实测输出**（同一组入参
 * 在搬迁前跑过一遍 dump 下来），而不是照着这里的新实现写的。server 那份
 * `computeContextUsage` 是它去掉 `liveUsage` 的子集，所以同一张表也是它的判据。
 */
const assistant = (totalInputTokens?: number) => ({
  role: "assistant",
  totalInputTokens,
});
const user = (totalInputTokens?: number) => ({
  role: "user",
  totalInputTokens,
});

describe("computeContextUsage", () => {
  it("Given contextWindow 不为正, When 取用量, Then 返回 undefined（整块不渲染）", () => {
    expect(computeContextUsage([assistant(5)], 0)).toBeUndefined();
    expect(computeContextUsage([assistant(5)], -1)).toBeUndefined();
  });

  it("Given liveUsage 有正的 totalInputTokens, When 取用量, Then 它压过消息扫描", () => {
    expect(
      computeContextUsage([assistant(5)], 100, { totalInputTokens: 7 }),
    ).toEqual({ used: 7, max: 100 });
  });

  it("Given liveUsage 为 0 / 缺省 / null, When 取用量, Then 回落到消息扫描", () => {
    expect(
      computeContextUsage([assistant(5)], 100, { totalInputTokens: 0 }),
    ).toEqual({ used: 5, max: 100 });
    expect(computeContextUsage([assistant(5)], 100)).toEqual({
      used: 5,
      max: 100,
    });
    expect(computeContextUsage([assistant(5)], 100, null)).toEqual({
      used: 5,
      max: 100,
    });
  });

  it("Given 尾部是 user 与零 token 的 assistant, When 取用量, Then 跳过它们取更早那条 assistant", () => {
    expect(
      computeContextUsage([assistant(5), user(9), assistant(0)], 100),
    ).toEqual({ used: 5, max: 100 });
  });

  it("Given 没有任何带 token 的 assistant, When 取用量, Then 是 0/max 占位而不是 undefined", () => {
    expect(computeContextUsage([], 100)).toEqual({ used: 0, max: 100 });
    expect(computeContextUsage([assistant(undefined)], 100)).toEqual({
      used: 0,
      max: 100,
    });
    expect(computeContextUsage([user(9)], 100)).toEqual({ used: 0, max: 100 });
  });
});
