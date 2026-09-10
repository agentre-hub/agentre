import { describe, expect, it } from "vitest";

import { tokenizeMarkdownAutoLinks } from "./markdown-autolinks";

const CWD = "/Users/me/proj";

function linkValues(text: string, cwd?: string): string[] {
  return tokenizeMarkdownAutoLinks(text, cwd)
    .filter((segment) => segment.type === "link")
    .map((segment) => segment.value);
}

function visibleText(text: string, cwd?: string): string {
  return tokenizeMarkdownAutoLinks(text, cwd)
    .map((segment) => segment.value)
    .join("");
}

describe("tokenizeMarkdownAutoLinks", () => {
  it("Given ordinary prose, when it contains allowed URL and path classes, then each complete target is recognized without its punctuation", () => {
    const text =
      "See https://example.com/a, www.example.com；mailto:a@example.com tel:+1234 ./docs/guide.md:42:7，frontend/src/components。README.md!";

    expect(linkValues(text, CWD)).toEqual([
      "https://example.com/a",
      "www.example.com",
      "mailto:a@example.com",
      "tel:+1234",
      "./docs/guide.md:42:7",
      "frontend/src/components",
      "README.md",
    ]);
    expect(visibleText(text, CWD)).toBe(text);
  });

  it("Given cwd-independent absolute targets, when tokenized, then POSIX, Windows, and file URL forms are recognized", () => {
    expect(
      linkValues("file:///Users/me/a.go /Users/me/b.go:3 C:\\work\\c.go:4:2"),
    ).toEqual([
      "file:///Users/me/a.go",
      "/Users/me/b.go:3",
      "C:\\work\\c.go:4:2",
    ]);
  });

  it("Given a path containing spaces, when it is paired-quoted, then only the inner complete path is linked and both quotes remain visible", () => {
    const text = "Open \"docs/My Guide.md\" or 'frontend/My File.tsx'.";

    expect(linkValues(text, CWD)).toEqual([
      "docs/My Guide.md",
      "frontend/My File.tsx",
    ]);
    expect(visibleText(text, CWD)).toBe(text);
  });

  it("Given complete targets inside smart quotes, when tokenized, then the closing quotes stay outside the links", () => {
    const text = "Open “README.md” and ‘frontend/src/chat.tsx’.";

    expect(linkValues(text, CWD)).toEqual([
      "README.md",
      "frontend/src/chat.tsx",
    ]);
    expect(visibleText(text, CWD)).toBe(text);
  });

  it("Given an unquoted relative path containing spaces, when tokenized, then no misleading partial filename is linked", () => {
    expect(linkValues("Open docs/My Guide.md now", CWD)).toEqual([]);
  });

  it("Given unquoted absolute paths containing spaces, when tokenized, then no misleading partial absolute target is linked", () => {
    for (const text of [
      "Open /Users/me/My Guide.md now",
      "Open C:\\work\\My Guide.md now",
      "Open file:///Users/me/My Guide.md now",
    ]) {
      expect(linkValues(text, CWD)).toEqual([]);
    }
  });

  it("Given targets followed by an ASCII colon or closing angle bracket, when tokenized, then the peripheral punctuation remains outside each link", () => {
    const text = "README.md:next https://example.com>";

    expect(linkValues(text, CWD)).toEqual(["README.md", "https://example.com"]);
    expect(visibleText(text, CWD)).toBe(text);
  });

  it("Given prose using a bare slash as a separator, when tokenized, then the bare root slash is not linked while real file targets still are", () => {
    expect(linkValues("doSend / Regenerate", CWD)).toEqual([]);
    expect(visibleText("doSend / Regenerate", CWD)).toBe("doSend / Regenerate");
    expect(linkValues("README.md / LICENSE", CWD)).toEqual(["README.md"]);
  });

  it("Given a parenthesized slash-separated list, when tokenized, then the whole phrase stays plain text", () => {
    const text = "四条已落地（#1/#2/#5/#6）";
    expect(linkValues(text, CWD)).toEqual([]);
    expect(visibleText(text, CWD)).toBe(text);
  });

  it("Given an enumeration of single-character segments, when tokenized, then it stays plain text while real multi-segment paths still link", () => {
    expect(linkValues("改动 a/b/c 三处", CWD)).toEqual([]);
    expect(visibleText("改动 a/b/c 三处", CWD)).toBe("改动 a/b/c 三处");
    expect(linkValues("按 A/B/C 排序", CWD)).toEqual([]);
    expect(
      linkValues("改动 frontend/src/components 与 x/y/z.txt", CWD),
    ).toEqual(["frontend/src/components", "x/y/z.txt"]);
  });

  it("Given ambiguous or unsafe text, when tokenized, then it remains plain text", () => {
    const text =
      "example.com github.com/owner/repo docs foo.bar 2026/08/14 1/2 javascript:alert(1) data:text/plain,x file://server/share/a.go #L42";

    expect(linkValues(text, CWD)).toEqual([]);
    expect(visibleText(text, CWD)).toBe(text);
  });

  it("Given home-anchored targets, when tokenized, then ~/ paths link without cwd while a bare tilde stays plain text", () => {
    const text = "\u770b\u770b ~/Code/agentre/frontend \u548c ~/notes.md:12";
    expect(linkValues(text, undefined)).toEqual([
      "~/Code/agentre/frontend",
      "~/notes.md:12",
    ]);
    expect(visibleText(text, undefined)).toBe(text);

    const plain = "~ \u4e0e ~~\u5220\u9664\u7ebf~~ \u4fdd\u6301\u539f\u6837";
    expect(linkValues(plain, CWD)).toEqual([]);
    expect(visibleText(plain, CWD)).toBe(plain);
  });

  it("Given no cwd, when relative paths and trusted filenames appear, then only cwd-independent URLs are recognized", () => {
    expect(
      linkValues("README.md ./docs/guide.md https://example.com", undefined),
    ).toEqual(["https://example.com"]);
  });
});
