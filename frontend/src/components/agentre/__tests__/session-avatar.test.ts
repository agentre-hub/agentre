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
  it("从 meta 推导首字母 + 颜色，没有身份字段时不画图标/图片", () => {
    expect(
      avatarFromMeta({ agentColor: "agent-2", agentName: "Codex" }),
    ).toEqual({
      letter: "C",
      color: "var(--agent-2)",
      avatarIcon: "",
      avatarDataUrl: "",
    });
  });
  it("meta 缺失时回落灰底问号", () => {
    expect(avatarFromMeta(null)).toEqual({
      letter: "?",
      color: "#94a3b8",
      avatarIcon: "",
      avatarDataUrl: "",
    });
    expect(avatarFromMeta({})).toEqual({
      letter: "?",
      color: "#94a3b8",
      avatarIcon: "",
      avatarDataUrl: "",
    });
  });
  it("agent 在 store 里时，名字 / 颜色 / 图标 / 上传图片都以它为准", () => {
    expect(
      avatarFromMeta(
        { agentColor: "agent-2", agentName: "旧名字" },
        {
          name: "设计师",
          color: "agent-9",
          avatarIcon: "preset:agent-assistant",
          avatarDataUrl: "data:image/png;base64,AAAA",
        },
      ),
    ).toEqual({
      letter: "设",
      color: "var(--agent-9)",
      avatarIcon: "preset:agent-assistant",
      avatarDataUrl: "data:image/png;base64,AAAA",
    });
  });
  it("agent 不在 store 里时回落 meta 的名字与颜色，只是没有图标/图片", () => {
    expect(
      avatarFromMeta({ agentColor: "agent-2", agentName: "Codex" }, null),
    ).toEqual({
      letter: "C",
      color: "var(--agent-2)",
      avatarIcon: "",
      avatarDataUrl: "",
    });
  });
});
