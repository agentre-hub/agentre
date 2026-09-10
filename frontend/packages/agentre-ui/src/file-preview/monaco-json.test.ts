import { describe, expect, it, vi } from "vitest";

import type { MonacoNS } from "./monaco";
import { jsonTokenProvider, registerJsonLanguage } from "./monaco-json";

function fakeLanguages() {
  const register = vi.fn();
  const setLanguageConfiguration = vi.fn();
  const setMonarchTokensProvider = vi.fn();
  const languages = {
    register,
    setLanguageConfiguration,
    setMonarchTokensProvider,
  };
  const monaco = { languages } as unknown as MonacoNS;
  return {
    monaco,
    register,
    setLanguageConfiguration,
    setMonarchTokensProvider,
  };
}

describe("registerJsonLanguage", () => {
  it("registers the json language with the jsonc extension alias", () => {
    const { monaco, register, setMonarchTokensProvider } = fakeLanguages();

    registerJsonLanguage(monaco);

    expect(register).toHaveBeenCalledWith(
      expect.objectContaining({
        id: "json",
        extensions: expect.arrayContaining([".json", ".jsonc"]),
      }),
    );
    expect(setMonarchTokensProvider).toHaveBeenCalledWith(
      "json",
      jsonTokenProvider,
    );
  });

  it("wires JSONC comment configuration (line + block) for the read-only editor", () => {
    const { monaco, setLanguageConfiguration } = fakeLanguages();

    registerJsonLanguage(monaco);

    expect(setLanguageConfiguration).toHaveBeenCalledWith(
      "json",
      expect.objectContaining({
        comments: {
          lineComment: "//",
          blockComment: ["/*", "*/"],
        },
      }),
    );
  });

  it("tokenizer covers the full valid-JSON surface (no default-token leakage)", () => {
    // Monarch 规则面:root 必须命中 whitespace / numbers / propertyName / strings /
    // delimiters / brackets / 未引号字面量,合法 JSON 才不会被 defaultToken('invalid')
    // 标红。propertyName 在 strings 之前(键先于字符串值判定)。
    const root = jsonTokenProvider.tokenizer.root;
    const included = new Set(
      root
        .map((r) => (r as { include?: string }).include)
        .filter((v): v is string => Boolean(v)),
    );
    expect(included).toEqual(
      new Set(["@whitespace", "@numbers", "@propertyName", "@strings"]),
    );
    const delims = root.some(
      (r) => Array.isArray(r) && r[0] instanceof RegExp && r[1] === "delimiter",
    );
    const brackets = root.some(
      (r) => Array.isArray(r) && r[1] === "delimiter.bracket",
    );
    // 未引号字面量 true/false/null 有专门规则,不然会露红。
    const keywordRule = root.some(
      (r) =>
        Array.isArray(r) &&
        r[0] instanceof RegExp &&
        r[0].source.includes("true") &&
        r[1] === "keyword",
    );
    // 键命中 string.key.json(默认主题给键专门配色)。
    const keyRule = jsonTokenProvider.tokenizer.propertyName?.[0];
    expect(delims).toBe(true);
    expect(brackets).toBe(true);
    expect(keywordRule).toBe(true);
    expect(
      Array.isArray(keyRule) &&
        keyRule[0] instanceof RegExp &&
        keyRule[1] === "string.key.json",
    ).toBe(true);
  });
});
