import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * 色板 token 本身的守卫已随 tokens 迁到共享包
 * （packages/agentre-ui/src/styles/tokens.test.ts）。这里只留宿主侧
 * base 层的规则——选中/复制语义是桌面端 globals.css 独有的，不进共享包。
 */
const globalsPath = resolve(process.cwd(), "src/styles/globals.css");

describe("app base layer", () => {
  it("keeps copyable control text enabled after button selection reset", () => {
    const css = readFileSync(globalsPath, "utf8");
    const buttonReset = css.indexOf('[data-selectable-text="true"] button');
    const copyableText = css.indexOf('[data-copyable-control-text="true"]');

    expect(buttonReset).toBeGreaterThanOrEqual(0);
    expect(copyableText).toBeGreaterThan(buttonReset);
    expect(css).toContain('[data-copyable-control-text="true"] *');
    expect(css).toContain("user-select: text;");
  });

  it("pulls design tokens from the shared package rather than redefining them", () => {
    const css = readFileSync(globalsPath, "utf8");

    expect(css).toContain('@import "@agentre-hub/agentre-ui/tokens.css";');
    // 回流守卫：token 定义块一旦被抄回宿主，共享包就不再是唯一真相源。
    expect(css).not.toMatch(/^@theme\b/m);
    expect(css).not.toMatch(/^\.dark\s*\{/m);
  });
});
