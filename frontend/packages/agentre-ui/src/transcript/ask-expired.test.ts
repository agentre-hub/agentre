import { describe, expect, it } from "vitest";

import { isAskExpiredError } from "./ask-expired";

// 运行时哨兵 ErrWaiterNotFound 的文本（internal/pkg/agentruntime/errors.go）。
const SENTINEL = "agentruntime: no waiting request for requestID";

describe("isAskExpiredError", () => {
  it.each<[string, unknown, boolean]>([
    ["裸哨兵（hermes / openclaw / remote）", new Error(SENTINEL), true],
    [
      "claudecode 包了一层的哨兵",
      new Error(
        `agentruntime/runtimes/claudecode: no waiting AskUserQuestion for requestID req-1: ${SENTINEL}`,
      ),
      true,
    ],
    [
      "codex 包了一层的哨兵",
      new Error(
        `agentruntime/runtimes/codex: no waiting request_user_input for requestID 7: ${SENTINEL}`,
      ),
      true,
    ],
    [
      "Wails 以裸字符串 reject，哨兵在本地化 headline 之后的 cause 行",
      `操作失败\n${SENTINEL}`,
      true,
    ],
    ["带 message 字段的普通对象", { message: SENTINEL }, true],
    ["网络失败", new Error("websocket: close 1006 (abnormal closure)"), false],
    ["只有本地化 headline", new Error("操作失败"), false],
    ["别的 no waiting 字样不算", "no waiting room available", false],
    ["空字符串", "", false],
    ["undefined", undefined, false],
    ["null", null, false],
    ["不带文本的对象", {}, false],
  ])("%s", (_name, err, expected) => {
    expect(isAskExpiredError(err)).toBe(expected);
  });
});
