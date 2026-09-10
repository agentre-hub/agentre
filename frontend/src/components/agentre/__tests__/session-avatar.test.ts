import { describe, expect, it } from "vitest";

import { avatarFromMeta, firstLetter } from "../session-avatar";

// tokenToCssColor 已随颜色 token 词汇表搬进 @agentre-hub/agentre-ui，
// 它的用例跟着去了 packages/agentre-ui/src/lib/agent-color.test.ts。

describe("firstLetter", () => {
  it("取名字首字符", () => {
    expect(firstLetter("Claude")).toBe("C");
    expect(firstLetter("  Gemini")).toBe("G");
  });
  it("空 / undefined → ?", () => {
    expect(firstLetter("")).toBe("?");
    expect(firstLetter("   ")).toBe("?");
    expect(firstLetter(undefined)).toBe("?");
  });
});

describe("avatarFromMeta", () => {
  it("从 meta 推导首字母 + 颜色", () => {
    expect(
      avatarFromMeta({ agentColor: "agent-2", agentName: "Codex" }),
    ).toEqual({ letter: "C", color: "var(--agent-2)" });
  });
  it("meta 缺失时回落灰底问号", () => {
    expect(avatarFromMeta(null)).toEqual({ letter: "?", color: "#94a3b8" });
    expect(avatarFromMeta({})).toEqual({ letter: "?", color: "#94a3b8" });
  });
});
