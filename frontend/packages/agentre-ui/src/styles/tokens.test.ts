import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * 这份测试有两个入口：宿主 app 的 vitest（cwd=frontend/）会连它一起收进去跑，
 * 包自己的 `pnpm test`（cwd=packages/agentre-ui）也会跑。所以 cwd 不是稳定基准，
 * 而 import.meta.url 在 happy-dom 环境下不是 file: scheme——两条都探一下。
 */
function locateTokensCss(): string {
  const found = [
    resolve(process.cwd(), "packages/agentre-ui/styles/tokens.css"),
    resolve(process.cwd(), "styles/tokens.css"),
  ].find((candidate) => existsSync(candidate));

  if (!found) {
    throw new Error("tokens.css not found from either workspace root");
  }

  return found;
}

const tokensPath = locateTokensCss();

function readThemeBlock(selector: string) {
  const css = readFileSync(tokensPath, "utf8");
  const match = new RegExp(`${selector.replace(".", "\\.")}\\s*{([^}]*)}`).exec(
    css,
  );

  if (!match) {
    throw new Error(`Missing ${selector} theme block`);
  }

  return match[1];
}

function readColorVar(block: string, name: string) {
  const match = new RegExp(`${name}:\\s*(#[0-9a-fA-F]{6});`).exec(block);

  if (!match) {
    throw new Error(`Missing ${name} color variable`);
  }

  return match[1].toLowerCase();
}

function rgb(hex: string) {
  return {
    r: Number.parseInt(hex.slice(1, 3), 16),
    g: Number.parseInt(hex.slice(3, 5), 16),
    b: Number.parseInt(hex.slice(5, 7), 16),
  };
}

function colorDistance(a: string, b: string) {
  const left = rgb(a);
  const right = rgb(b);

  return Math.hypot(left.r - right.r, left.g - right.g, left.b - right.b);
}

describe("theme tokens", () => {
  it("keeps dark accent visibly distinct from popover for hover states", () => {
    const darkTheme = readThemeBlock(".dark");
    const popover = readColorVar(darkTheme, "--popover");
    const accent = readColorVar(darkTheme, "--accent");

    expect(accent).not.toBe(popover);
    expect(colorDistance(accent, popover)).toBeGreaterThanOrEqual(32);
  });

  it("ships the keyframes that its own animate tokens reference", () => {
    const css = readFileSync(tokensPath, "utf8");
    const referenced = [...css.matchAll(/--animate-[\w-]+:\s*([\w-]+)\s/g)].map(
      (m) => m[1],
    );

    expect(referenced.length).toBeGreaterThan(0);
    for (const name of new Set(referenced)) {
      // 消费方（agentre-server）只 import tokens.css，keyframe 若留在宿主侧
      // 这里就会红——动画 token 引用一个不存在的 keyframe 是静默失效。
      expect(css).toContain(`@keyframes ${name}`);
    }
  });
});
