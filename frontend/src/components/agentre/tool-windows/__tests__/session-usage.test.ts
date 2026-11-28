import { describe, expect, it } from "vitest";

import type { chat_svc } from "../../../../../wailsjs/go/models";
import { sumSessionUsage } from "../session-usage";

const msg = (over: Partial<chat_svc.ChatMessage>): chat_svc.ChatMessage =>
  ({
    id: 1,
    role: "assistant",
    blocks: [],
    promptTokens: 0,
    completionTokens: 0,
    cachedTokens: 0,
    ...over,
  }) as unknown as chat_svc.ChatMessage;

describe("sumSessionUsage", () => {
  // Given 若干条带记录用量的消息, Then 输入 / 输出 / 缓存命中分别求和。
  it("sums input, output and cache hits across messages", () => {
    expect(
      sumSessionUsage([
        msg({
          id: 1,
          promptTokens: 100,
          completionTokens: 20,
          cachedTokens: 5,
        }),
        msg({
          id: 2,
          promptTokens: 300,
          completionTokens: 40,
          cachedTokens: 15,
        }),
        msg({ id: 3, role: "user" }),
      ]),
    ).toEqual({ input: 400, output: 60, cacheHits: 20 });
  });

  // Given 消息正文里有子代理块且带自己的用量, Then 它们不进总和（只取消息自己的记录列）。
  it("ignores subagent usage carried inside blocks", () => {
    const withSubagent = msg({
      id: 4,
      promptTokens: 10,
      completionTokens: 2,
      blocks: [
        {
          type: "subagent_state",
          subagent: { runs: [{ inputTokens: 9999, outputTokens: 8888 }] },
        },
      ] as unknown as chat_svc.ChatMessage["blocks"],
    });
    expect(sumSessionUsage([withSubagent])).toEqual({
      input: 10,
      output: 2,
      cacheHits: 0,
    });
  });

  // Given 没有任何记录用量, Then 返回 null（该行不显示，不编 0）。
  it("returns null when nothing was recorded", () => {
    expect(sumSessionUsage([])).toBeNull();
    expect(sumSessionUsage([msg({}), msg({ role: "user" })])).toBeNull();
  });
});
