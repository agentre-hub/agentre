import { describe, expect, it } from "vitest";

import { isTimeoutFailure } from "./timeout-failure";

/**
 * 「写操作超时（请求发出去了，可能已经生效）」全包只有一条认法：看板 / 项目 / 标签的
 * 新建与审批 / 提问的提交认的是同一件事，各写一份迟早漂移。
 */
describe("isTimeoutFailure", () => {
  const named = () => {
    const e = new Error("whatever");
    e.name = "RequestTimeoutError";
    return e;
  };

  it.each([
    ["名字带 Timeout 的 Error", named(), true],
    ["消息说 timed out", new Error("request timed out after 15000ms"), true],
    ["裸字符串（Wails 的 reject 形状）", "context timed out", true],
    ["带 message 字段的对象", { message: "request timed out" }, true],
    ["别的失败", new Error("relay closed"), false],
    ["对象里别的原因", { message: "permission denied" }, false],
  ])("%s", (_name, input, expected) => {
    expect(isTimeoutFailure(input)).toBe(expected);
  });
});
