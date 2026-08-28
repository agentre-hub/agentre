import { describe, expect, it } from "vitest";

import { splitStreamingMarkdown } from "./streaming-markdown";

// splitStreamingMarkdown 把「流式累积中的 markdown 文本」切成
// [已定稿 block...] + [活跃尾巴]。已定稿 block 一旦产出就永不再变 ——
// 调用方据此对每个已定稿 block 做 memo,只让活跃尾巴每 chunk 重解析,
// 把单 chunk 渲染开销从 O(n) 降到 O(Δ)。
describe("splitStreamingMarkdown", () => {
  it("when empty string then no committed blocks and empty tail", () => {
    expect(splitStreamingMarkdown("")).toEqual({ committed: [], tail: "" });
  });

  it("when single growing paragraph (no blank line) then it stays in tail", () => {
    // 还没出现 block 边界 —— 整段都可能继续生长,不能定稿。
    expect(splitStreamingMarkdown("hello wor")).toEqual({
      committed: [],
      tail: "hello wor",
    });
  });

  it("when paragraph + blank line + new paragraph then first commits, last is tail", () => {
    expect(splitStreamingMarkdown("para one\n\npara two")).toEqual({
      committed: ["para one"],
      tail: "para two",
    });
  });

  it("when text ends with a blank line then the trailing block is committed too", () => {
    // 末尾的空行意味着该段已闭合,模型已经移到下一个 block,可安全定稿。
    expect(splitStreamingMarkdown("para one\n\n")).toEqual({
      committed: ["para one"],
      tail: "",
    });
  });

  it("when a fenced code block is closed then it commits as one atomic block", () => {
    // 闭合的代码块永不再变 —— 定稿后只跑一次 highlight.js,后续 chunk 跳过。
    expect(splitStreamingMarkdown("```js\nconst x = 1;\n```\nafter")).toEqual({
      committed: ["```js\nconst x = 1;\n```"],
      tail: "after",
    });
  });

  it("when a fenced code block is still open then the whole fence stays in tail", () => {
    // 未闭合的 fence 仍在生长且其内容解释可能改变,必须留在尾巴里整体重解析。
    expect(splitStreamingMarkdown("intro line\n\n```js\nconst x =")).toEqual({
      committed: ["intro line"],
      tail: "```js\nconst x =",
    });
  });

  it("when a blank line sits inside an open fence then it does not split the block", () => {
    // fence 内部的空行不是 block 边界 —— 不能据此切开。
    expect(splitStreamingMarkdown("```\nline1\n\nline2\n```\ntail")).toEqual({
      committed: ["```\nline1\n\nline2\n```"],
      tail: "tail",
    });
  });

  it("when multiple committed blocks precede the tail then all but the last commit", () => {
    expect(
      splitStreamingMarkdown("a\n\n```\ncode\n```\n\nb\n\nc growing"),
    ).toEqual({
      committed: ["a", "```\ncode\n```", "b"],
      tail: "c growing",
    });
  });
});
