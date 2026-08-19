import fs from "node:fs";
import path from "node:path";

import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Table, TableBody, TableCell, TableRow } from "@/components/ui/table";

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
 * 从 `hover:bg-muted/50`、`data-[state=selected]:bg-muted` 这样的类名里取出底色 token。
 * 带 `/50` 透明度的也要能认出来——透明度只改浓淡，撞色与否看的是那个 token。
 */
function bgTokenFor(prefix: string): string | undefined {
  const escaped = prefix.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const re = new RegExp(`^${escaped}bg-([a-z-]+)(?:/\\d+)?$`);
  return rowClasses()
    .map((c) => re.exec(c)?.[1])
    .find(Boolean);
}

describe("TableRow 的 hover / 选中反馈", () => {
  it.each([
    ["hover:", "hover"],
    ["data-[state=selected]:", "选中"],
  ])("%s 用的底色 token 不是静止表面 muted", (prefix, _label) => {
    const token = bgTokenFor(prefix);

    expect(token, `没找到 ${prefix}bg-* 类`).toBeDefined();
    // --muted 在暗色下与 --card 同值，而表格就放在卡片里。
    expect(token).not.toBe("muted");
  });

  it.each([
    [":root" as const, "亮色"],
    [".dark" as const, "暗色"],
  ])("%s(%s)下 hover 与选中底色对 card 都可觉察", (scope, _theme) => {
    const tokens = readTokens(scope);

    for (const prefix of ["hover:", "data-[state=selected]:"]) {
      const token = bgTokenFor(prefix) as string;
      const color = tokens[token];
      expect(color, `tokens.css 里没有 --${token}`).toBeDefined();
      expect(
        contrast(color, tokens.card),
        `${prefix}bg-${token} (${color}) 落在 --card ${tokens.card} 上`,
      ).toBeGreaterThanOrEqual(FEEDBACK_MIN);
    }
  });
});
