import fs from "node:fs";
import path from "node:path";

import { describe, expect, it } from "vitest";

import {
  labelToneClassNames,
  toneClass,
  type IssueLabelTone,
} from "@/components/agentre/issue-tones";

// issue 标签是十个色档的胶囊,文字直接坐在自己的底色上,尺寸是 text-2xs(11px)——
// 按 WCAG AA 属正文,门槛 4.5(docs/design.md §10)。
//
// 这条守卫刻意**不**断言类名字符串(那样改个名就能骗过,而且改名不改颜色也照样红),
// 而是:
//   ① 从 issue-tones.ts 真正导出的类名里反查 bg-/text- 用的是哪个 token;
//   ② 去共享包 tokens.css 取该 token 在 :root / .dark 两块里的实际色值;
//   ③ 对"文字色 vs 它自己的底色"算对比度。
// 想让它变绿,只能真的把颜色改够。
//
// ③ 里有一步不能省:类名可以带不透明度。auth/ui 曾经写成 `bg-agent-1/10`,而
// Tailwind v4 的 `/10` 生成 `color-mix(in oklab, <color> 10%, transparent)`,即该色的
// 10% alpha,浏览器再把它合成到底下那层表面上。所以半透明底必须先和宿主表面混出
// 实际色再算——直接拿 `--agent-1` 当底色会算出一个页面上根本不存在的数字。十档现在
// 都是不透明软底,但这段合成留着:再有人写回半透明胶囊,这条线仍然算得准。
const TOKENS_CSS = path.resolve(
  __dirname,
  "../../../../packages/agentre-ui/styles/tokens.css",
);

/** 正文门槛,见 docs/design.md §10。 */
const TEXT_MIN = 4.5;

/**
 * 标签胶囊实际落在的宿主表面,取自真实调用点:
 *   - card:看板卡片 `bg-card`(kanban/issues-board.tsx);
 *   - background:issue 列表行(issues-page.tsx);
 *   - 列表行 hover:同一行 `hover:bg-accent/40`(issues-page.tsx)——hover 是可读性
 *     必须成立的状态之一,而它比静止态更暗,半透明胶囊在这里最吃亏。
 * 半透明底在不同表面上混出的结果不同,所以每种都要算。
 */
const HOST_SURFACES: {
  label: string;
  resolve: (tokens: Record<string, string>) => string;
}[] = [
  { label: "--card", resolve: (t) => t["--card"] },
  { label: "--background", resolve: (t) => t["--background"] },
  {
    label: "列表行 hover(--accent/40 on --background)",
    resolve: (t) => composite(t["--accent"], 0.4, t["--background"]),
  },
];

/** 取一个块里的 `--name: #rrggbb;`。 */
function readScope(scope: ":root" | ".dark"): Record<string, string> {
  const css = fs.readFileSync(TOKENS_CSS, "utf8");
  const start = css.indexOf(`${scope} {`);
  const end = css.indexOf("\n}", start);
  const out: Record<string, string> = {};
  for (const m of css
    .slice(start, end)
    .matchAll(/(--[a-z0-9-]+):\s*(#[0-9a-fA-F]{6})\s*;/g)) {
    out[m[1]] = m[2];
  }
  return out;
}

const root = readScope(":root");
// 暗色只覆写一部分 token,其余继承 :root(例如主题无关的 --agent-foreground)。
const dark = { ...root, ...readScope(".dark") };

const THEMES = [["亮色", root, false] as const, ["暗色", dark, true] as const];

const channels = (hex: string) =>
  [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16));

/** WCAG 2.x 相对亮度 → 对比度。 */
function contrast(a: string, b: string): number {
  const luminance = (hex: string) => {
    const linear = channels(hex).map((v) => {
      const c = v / 255;
      return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
  };
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

/** alpha 合成:把 `color` 以 alpha 盖在 `under` 上,得到屏幕上真正的那个色。 */
function composite(color: string, alpha: number, under: string): string {
  if (alpha >= 1) return color;
  const top = channels(color);
  const bottom = channels(under);
  return `#${top
    .map((v, i) => Math.round(v * alpha + bottom[i] * (1 - alpha)))
    .map((v) => v.toString(16).padStart(2, "0"))
    .join("")}`;
}

type Layer = { token: string; alpha: number };

/**
 * 从类名串里挑出 `bg-x` / `text-x`,并解出可选的 `/N` 不透明度。
 *
 * 暗色下必须优先认 `dark:` 变体:critical 写的是
 * `bg-destructive … dark:bg-destructive/60`,暗色实际渲染的是后者。只认无前缀的
 * 那条,量出来的是一个屏幕上不存在的颜色——守卫会以 2.65 判它不达标,而真实渲染
 * 是 5.30。这正是「守卫必须模拟真实渲染」那条教训的第三次出现。
 */
function layer(
  classNames: string,
  prefix: "bg" | "text",
  isDark = false,
): Layer {
  const pick = (variant: string) =>
    new RegExp(
      `(?:^|\\s)${variant}${prefix}-([a-z0-9-]+)(?:/(\\d{1,3}))?(?=\\s|$)`,
    ).exec(classNames);
  const m = (isDark && pick("dark:")) || pick("");
  if (!m) throw new Error(`"${classNames}" 里没有 ${prefix}- 类`);
  return { token: `--${m[1]}`, alpha: m[2] ? Number(m[2]) / 100 : 1 };
}

/** 一档色在某主题、某宿主表面上,屏幕上真正的 {文字色, 底色}。 */
function rendered(
  classNames: string,
  tokens: Record<string, string>,
  surface: string,
  isDark = false,
): { text: string; fill: string } {
  const bg = layer(classNames, "bg", isDark);
  const text = layer(classNames, "text", isDark);
  for (const { token } of [bg, text]) {
    expect(tokens[token], `tokens.css 里没有 ${token}`).toBeDefined();
  }
  const fill = composite(tokens[bg.token], bg.alpha, surface);
  return { text: composite(tokens[text.token], text.alpha, fill), fill };
}

// 十档全部在内,critical 也不例外。它曾经被豁免过一轮:唯一一档实心填充,暗色下
// 白字压在**浅**红 --destructive #f87171 上只有 2.65,而当时判断改全局
// --destructive-foreground 会波及每个 destructive 按钮。后来查清影响面其实只有三处
// (button/badge 都带着 dark:bg-destructive/60,混出 #a05153 后白字 5.30),
// 于是让 critical 照同样的办法压暗填充,而不是动那个共用 token。
const TONE_CASES: [string, string][] = (
  Object.keys(labelToneClassNames) as IssueLabelTone[]
).map((tone) => [tone, labelToneClassNames[tone]]);

// 后端可以送来任何 tone 字符串,认不出来的走 toneClass 的兜底。那也是一枚一模一样
// 的胶囊,所以它必须和十档一起过同一条线——否则兜底就是护栏上的一个洞。
TONE_CASES.push(["(未知 tone 兜底)", toneClass("__no_such_tone__")]);

describe.each(THEMES)(
  "issue 标签色档在%s下的正文对比度",
  (_theme, tokens, isDark) => {
    it.each(TONE_CASES)(
      `%s 的文字在自己的底色上 ≥ ${TEXT_MIN}`,
      (name, classNames) => {
        for (const surface of HOST_SURFACES) {
          const under = surface.resolve(tokens);
          const { text, fill } = rendered(classNames, tokens, under, isDark);
          expect(
            contrast(text, fill),
            `${name} "${classNames}" 落在 ${surface.label} ${under} 上:文字 ${text} / 底 ${fill}`,
          ).toBeGreaterThanOrEqual(TEXT_MIN);
        }
      },
    );
  },
);
