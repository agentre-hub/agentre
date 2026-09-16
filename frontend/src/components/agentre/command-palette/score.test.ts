import { describe, expect, it } from "vitest";

import { scoreSuggestion as scoreItem } from "@agentre-hub/agentre-ui";

describe("scoreItem — Chinese + English + Pinyin matching", () => {
  it("empty query returns 1 (preserve original order)", () => {
    expect(scoreItem({ query: "", title: "年度报告" })).toBe(1);
    expect(scoreItem({ query: "   ", title: "年度报告" })).toBe(1);
  });

  it("exact title match → 100", () => {
    expect(scoreItem({ query: "年度报告", title: "年度报告" })).toBe(100);
    // case-insensitive on ASCII
    expect(scoreItem({ query: "claude", title: "Claude" })).toBe(100);
  });

  it("title starts with query → 80", () => {
    expect(scoreItem({ query: "年度", title: "年度报告 v2" })).toBe(80);
    expect(scoreItem({ query: "Cla", title: "Claude Code" })).toBe(80);
  });

  it("title substring includes query → 60", () => {
    expect(scoreItem({ query: "报告", title: "年度报告 v2" })).toBe(60);
    expect(scoreItem({ query: "ude Co", title: "Claude Code" })).toBe(60);
  });

  it("ASCII query that is a full-pinyin substring of title → 50", () => {
    // 'niandu' substring of 'niandubaogao'
    expect(scoreItem({ query: "niandu", title: "年度报告" })).toBe(50);
    expect(scoreItem({ query: "niandubaogao", title: "年度报告" })).toBe(50);
  });

  it("ASCII query that matches pinyin initials → 40", () => {
    expect(scoreItem({ query: "ndbg", title: "年度报告" })).toBe(40);
    expect(scoreItem({ query: "ndb", title: "年度报告 v2" })).toBe(40);
  });

  it("ASCII query that fuzzy-matches the full pinyin → 30", () => {
    // 'nbg' as chars-in-order in 'niandubaogao'
    expect(scoreItem({ query: "nbg", title: "年度报告" })).toBe(30);
  });

  it("subtitle substring (when title misses) → 20", () => {
    expect(
      scoreItem({
        query: "claude",
        title: "二期收益估算",
        subtitle: "Claude Code",
      }),
    ).toBe(20);
  });

  it("non-match returns 0", () => {
    expect(scoreItem({ query: "zzzzz", title: "年度报告" })).toBe(0);
    expect(
      scoreItem({ query: "zzzzz", title: "年度报告", subtitle: "agent" }),
    ).toBe(0);
  });

  it("ASCII-only check skips pinyin paths for mixed/Chinese queries", () => {
    // 纯中文 query 走 substring；如果不命中标题就 0（pinyin 路径不应触发）
    expect(scoreItem({ query: "报告xx", title: "年度报告" })).toBe(0);
  });

  it("trims surrounding whitespace before comparing", () => {
    expect(scoreItem({ query: "  年度报告  ", title: "年度报告" })).toBe(100);
    expect(scoreItem({ query: "  niandu  ", title: "年度报告" })).toBe(50);
  });

  it("uppercase ASCII query still hits pinyin tiers (case-insensitive)", () => {
    expect(scoreItem({ query: "NDBG", title: "年度报告" })).toBe(40);
    expect(scoreItem({ query: "Niandu", title: "年度报告" })).toBe(50);
  });
});
