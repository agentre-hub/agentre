import { describe, expect, it } from "vitest";

import { computeLiveTurnStats, estimateCompletionTokens } from "./turn-stats";

describe("estimateCompletionTokens", () => {
  it("Given empty text, When estimated, Then it is 0", () => {
    expect(estimateCompletionTokens("")).toBe(0);
  });

  it("Given visible text, When estimated, Then it is ceil(chars / 4) at least 1", () => {
    expect(estimateCompletionTokens("abcd")).toBe(1);
    expect(estimateCompletionTokens("abcde")).toBe(2);
  });
});

describe("computeLiveTurnStats", () => {
  it("Given the turn has started with no visible token, When stats are computed, Then it is waiting and duration counts as first-token", () => {
    const stats = computeLiveTurnStats({
      model: "claude-sonnet-4",
      now: 1800,
      startedAt: 1000,
      firstTokenAt: null,
      generationMs: 0,
      burstStartedAt: null,
      promptTokens: 0,
      completionTokens: 0,
      cachedTokens: 0,
      cacheCreationTokens: 0,
      reasoningTokens: 0,
      liveText: "",
    });
    expect(stats.waitingFirstToken).toBe(true);
    expect(stats.durationMs).toBe(800);
    expect(stats.firstTokenMs).toBe(800);
    expect(stats.tokensPerSec).toBe(0);
    expect(stats.completionApprox).toBe(false);
    expect(stats.promptTokens).toBe(0);
    expect(stats.completionTokens).toBe(0);
  });

  it("Given visible text before any usage frame, When stats are computed, Then completion and speed are estimated with approx", () => {
    const stats = computeLiveTurnStats({
      model: "claude-sonnet-4",
      now: 3400,
      startedAt: 1000,
      firstTokenAt: 1420,
      generationMs: 0,
      burstStartedAt: 1420,
      promptTokens: 0,
      completionTokens: 0,
      cachedTokens: 0,
      cacheCreationTokens: 0,
      reasoningTokens: 0,
      liveText: "a".repeat(80),
    });
    expect(stats.waitingFirstToken).toBe(false);
    expect(stats.firstTokenMs).toBe(420);
    expect(stats.completionApprox).toBe(true);
    expect(stats.completionTokens).toBe(20);
    expect(stats.durationMs).toBe(2400);
    expect(stats.tokensPerSec).toBeCloseTo(20 / 1.98, 5);
  });

  it("Given two completed calls plus a live burst, When stats are computed, Then prompt is the latest and completion sums plus the live estimate", () => {
    const stats = computeLiveTurnStats({
      model: "claude-sonnet-4",
      now: 10_000,
      startedAt: 1000,
      firstTokenAt: 1420,
      generationMs: 3000,
      burstStartedAt: 8000,
      promptTokens: 22000,
      completionTokens: 600,
      cachedTokens: 100,
      cacheCreationTokens: 0,
      reasoningTokens: 50,
      liveText: "a".repeat(40),
    });
    expect(stats.promptTokens).toBe(22000);
    expect(stats.completionTokens).toBe(610);
    expect(stats.completionApprox).toBe(true);
    expect(stats.tokensPerSec).toBeCloseTo(122, 5);
  });

  it("Given a tool gap with no current burst, When stats are computed, Then speed uses only generation time and is not pulled toward zero", () => {
    const stats = computeLiveTurnStats({
      model: "claude-sonnet-4",
      now: 20_000,
      startedAt: 1000,
      firstTokenAt: 1420,
      generationMs: 2000,
      burstStartedAt: null,
      promptTokens: 12000,
      completionTokens: 200,
      cachedTokens: 0,
      cacheCreationTokens: 0,
      reasoningTokens: 0,
      liveText: "",
    });
    expect(stats.completionApprox).toBe(false);
    expect(stats.completionTokens).toBe(200);
    expect(stats.durationMs).toBe(19000);
    expect(stats.tokensPerSec).toBeCloseTo(100, 5);
  });
});
