import fs from "node:fs";
import path from "node:path";

/**
 * 测试里「从 tokens.css 取色值、算对比度」的公共件。
 *
 * 配色守卫都是同一个做法：不断言类名叫什么，而是从渲染出来的 class 反查 token，
 * 去 `styles/tokens.css` 取色值，对真实表面算对比度。取值与公式此前在每份用例里
 * 各抄一遍，抄出了分歧（有的只读 `:root`、有的不按级联合并深色）；算法只该有一份。
 *
 * 直接读文件而不是 import CSS：vitest 不编译 Tailwind，只有读源文件才测得到真实声明。
 */
const TOKENS_CSS = path.resolve(__dirname, "../../../styles/tokens.css");

export type TokenScope = ":root" | ".dark";

/** 取一个选择器块里的 `--name: #rrggbb;`，键不带 `--`。 */
function readBlock(css: string, selector: TokenScope): Record<string, string> {
  const out: Record<string, string> = {};
  // 行首锚定：`@custom-variant dark (&:is(.dark *));` 那行里也有 `.dark`。
  const head = new RegExp(`^${selector.replace(".", "\\.")}[^{]*\\{`, "m").exec(
    css,
  );
  if (!head) return out;
  // 块尾用花括号计数，不靠缩进或非贪婪正则。
  const open = head.index + head[0].length - 1;
  let depth = 0;
  let end = css.length;
  for (let i = open; i < css.length; i++) {
    if (css[i] === "{") depth++;
    else if (css[i] === "}" && --depth === 0) {
      end = i;
      break;
    }
  }
  for (const m of css
    .slice(open + 1, end)
    .matchAll(/--([a-z0-9-]+):\s*(#[0-9a-fA-F]{6})\s*;/g)) {
    out[m[1]] = m[2];
  }
  return out;
}

/**
 * 一个主题下**生效的**十六进制 token，键不带 `--`（`t.card`、`t["control-border"]`）。
 * 深色没有覆写的 token 沿用 `:root` —— 按级联后的有效值给。
 */
export function readTokens(scope: TokenScope): Record<string, string> {
  const css = fs
    .readFileSync(TOKENS_CSS, "utf8")
    .replace(/\/\*[\s\S]*?\*\//g, "");
  const light = readBlock(css, ":root");
  return scope === ":root" ? light : { ...light, ...readBlock(css, ".dark") };
}

/**
 * 元素静止态的边用的是哪个 token：不带变体前缀、且在 tokens.css 里真有色值的
 * `border-<token>`（`border-dashed`、`border-b` 这类不是颜色的类不算）。没有就是 undefined。
 */
export function edgeTokenOf(el: Element): string | undefined {
  const tokens = readTokens(":root");
  return Array.from(el.classList)
    .map((c) => /^border-([a-z]+(?:-[a-z]+)*)$/.exec(c)?.[1])
    .find((name) => name !== undefined && name in tokens);
}

/** WCAG 2.x 对比度，两个 `#rrggbb`。 */
export function contrast(a: string, b: string): number {
  const luminance = (hex: string) => {
    const ch = [1, 3, 5].map((i) => {
      const c = parseInt(hex.slice(i, i + 2), 16) / 255;
      return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * ch[0] + 0.7152 * ch[1] + 0.0722 * ch[2];
  };
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

/** 前景以 alpha 叠在底上得到的实色。半透明填充的对比度要先合成再算。 */
export function mix(fg: string, bg: string, alpha: number): string {
  const ch = (hex: string) =>
    [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16));
  const [f, b] = [ch(fg), ch(bg)];
  return `#${f
    .map((c, i) =>
      Math.round(c * alpha + b[i] * (1 - alpha))
        .toString(16)
        .padStart(2, "0"),
    )
    .join("")}`;
}

/**
 * `bg-<name>` 落在 `surface`（一个 token 名，不带 `--`）上时画出来的实色。
 *
 * 两种底色都要认：带色值的 token（`raised`、`sidebar-active-bg`）就是它自己；相对填充
 * （@theme 里的 `--color-<name>: color-mix(in oklab, var(--foreground) N%, transparent)`）
 * 是前景色按 N% 叠在脚下的表面上——不先合成就没法和别的面比。两样都不是就是 undefined。
 */
export function fillOn(
  name: string,
  scope: TokenScope,
  surface: string,
): string | undefined {
  const t = readTokens(scope);
  if (name in t) return t[name];
  const css = fs
    .readFileSync(TOKENS_CSS, "utf8")
    .replace(/\/\*[\s\S]*?\*\//g, "");
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const m = new RegExp(
    `--color-${escaped}:\\s*color-mix\\(\\s*in oklab,\\s*var\\(--foreground\\)\\s+([\\d.]+)%,\\s*transparent\\s*\\)\\s*;`,
  ).exec(css);
  if (!m || !(surface in t)) return undefined;
  return mix(t.foreground, t[surface], Number(m[1]) / 100);
}
