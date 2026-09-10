import fs from "node:fs";
import path from "node:path";

import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Table, TableBody, TableCell, TableRow } from "../index";

// 表格行的 hover 与选中是**唯一**告诉用户"我停在哪一行 / 选中了哪一行"的信号：
// 表格里没有别的线索可以替代它。这条守卫盯的是它别再和自己坐着的表面同色 ——
// 此前 hover / 选中都取 --muted，而暗色下 --muted 和 --card 是同一个字节（#1d2025），
// 表格又正好放在卡片里，于是反馈是字面意义上的 1.00:1。
//
// 和 checkbox.test.tsx 同一套路：不断言"类名叫什么"（改个名就能骗过），而是从真正
// 渲染出来的 class 里**反查**用的是哪个 token，再去共享包 tokens.css 取值算对比度。
const TOKENS_CSS = path.resolve(
  __dirname,
  "../../../../packages/agentre-ui/styles/tokens.css",
);

/** 交互反馈没有法定门槛；这条线代表"肉眼能觉察"，与包内 tokens.test.ts 同值。 */
const FEEDBACK_MIN = 1.15;

function readTokens(scope: ":root" | ".dark"): Record<string, string> {
  const css = fs.readFileSync(TOKENS_CSS, "utf8");
  const start = css.indexOf(`${scope} {`);
  const end = css.indexOf("\n}", start);
  const out: Record<string, string> = {};
  for (const m of css
    .slice(start, end)
    .matchAll(/--([a-z0-9-]+):\s*(#[0-9a-fA-F]{6})\s*;/g)) {
    out[m[1]] = m[2];
  }
  return out;
}

function contrast(a: string, b: string): number {
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

function rowClasses(): string[] {
  const { container } = render(
    <Table>
      <TableBody>
        <TableRow data-testid="row">
          <TableCell>claude-opus-4-1</TableCell>
        </TableRow>
      </TableBody>
    </Table>,
  );
  const row = container.querySelector('[data-testid="row"]');
  return Array.from(row?.classList ?? []);
}

/**
 * 从 `hover:bg-accent/60`、`data-[state=selected]:bg-accent` 这样的类名里取出
 * 底色 token **和它的透明度**。
 *
 * 透明度必须一起取回来：`bg-accent/60` 渲染出来的不是 `--accent`，是 `--accent`
 * 按 60% 混到宿主表面上的结果，比实色弱得多。第一版守卫把 `/N` 直接丢掉、
 * 拿实色去算，于是报出 1.234 而实际渲染只有 1.108 —— 一条**放过了自己门槛**的
 * 守卫，比没有守卫更糟，因为它让人以为查过了。
 */
function bgFillFor(prefix: string): { token: string; alpha: number } | null {
  const escaped = prefix.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const re = new RegExp(`^${escaped}bg-([a-z-]+)(?:/(\\d+))?$`);
  for (const cls of rowClasses()) {
    const m = re.exec(cls);
    if (m) return { token: m[1], alpha: m[2] ? Number(m[2]) / 100 : 1 };
  }
  return null;
}

/** 把半透明前景按 alpha 混到底色上，得到实际渲染出的颜色。 */
function composite(fg: string, bg: string, alpha: number): string {
  const ch = (hex: string, i: number) => parseInt(hex.slice(i, i + 2), 16);
  return (
    "#" +
    [1, 3, 5]
      .map((i) =>
        Math.round(ch(fg, i) * alpha + ch(bg, i) * (1 - alpha))
          .toString(16)
          .padStart(2, "0"),
      )
      .join("")
  );
}

describe("TableRow 的 hover / 选中反馈", () => {
  it.each([
    ["hover:", "hover"],
    ["data-[state=selected]:", "选中"],
  ])("%s 用的底色 token 不是静止表面 muted", (prefix, _label) => {
    const fill = bgFillFor(prefix);

    expect(fill, `没找到 ${prefix}bg-* 类`).not.toBeNull();
    // --muted 在暗色下与 --card 同值，而表格就放在卡片里。
    expect(fill?.token).not.toBe("muted");
  });

  it.each([
    [":root" as const, "亮色"],
    [".dark" as const, "暗色"],
  ])(
    "%s(%s)下 hover 与选中**实际渲染出的**底色对 card 都可觉察",
    (scope, _theme) => {
      const tokens = readTokens(scope);

      for (const prefix of ["hover:", "data-[state=selected]:"]) {
        const fill = bgFillFor(prefix)!;
        const color = tokens[fill.token];
        expect(color, `tokens.css 里没有 --${fill.token}`).toBeDefined();

        const rendered = composite(color, tokens.card, fill.alpha);
        expect(
          contrast(rendered, tokens.card),
          `${prefix}bg-${fill.token}${fill.alpha < 1 ? `/${fill.alpha * 100}` : ""}` +
            ` 在 --card ${tokens.card} 上实际渲染成 ${rendered}`,
        ).toBeGreaterThanOrEqual(FEEDBACK_MIN);
      }
    },
  );

  it("hover 比选中弱，两者可区分", () => {
    // 同一个 token 承担两种状态，只靠 alpha 分开。若哪天有人把 hover 也调成实色，
    // 「停在这一行」和「选中了这一行」就会长得一模一样。
    const hover = bgFillFor("hover:")!;
    const selected = bgFillFor("data-[state=selected]:")!;

    expect(hover.token).toBe(selected.token);
    expect(hover.alpha).toBeLessThan(selected.alpha);
  });
});
