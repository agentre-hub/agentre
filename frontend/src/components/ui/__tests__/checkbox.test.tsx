import fs from "node:fs";
import path from "node:path";

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Checkbox } from "@/components/ui/checkbox";

// 未选中的复选框**没有填充**——边框就是这个控件的全部可见信息。按 WCAG 2.2
// §1.4.11（非文本对比度）它必须对所处表面有 3:1，否则控件整个消失。
//
// 这条守卫刻意不去断言"类名叫什么"（那样改个名字就能骗过），而是：
//   ① 从真正渲染出来的 class 列表里**反查**边框用的是哪个 token；
//   ② 去共享包的 tokens.css 里取那个 token 的实际色值；
//   ③ 对复选框实际会落在的两种表面算对比度。
// 想让它变绿，只能真的把颜色改够。
//
// 两种表面来自真实调用点：
//   - secondary：模型表表头的全选（provider-workspace.tsx）、发现模型弹窗的
//     「全选新模型」（discover-dialog.tsx），都坐在 bg-secondary 上；
//   - card：同一张表的行内勾选、导出范围勾选，坐在 bg-card 上。
const TOKENS_CSS = path.resolve(
  __dirname,
  "../../../../packages/agentre-ui/styles/tokens.css",
);

const NON_TEXT_CONTRAST_MIN = 3;

/** 取 :root / .dark 两块里的 `--name: #rrggbb;`。 */
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

/** WCAG 2.x 相对亮度 → 对比度。 */
function contrast(a: string, b: string): number {
  const luminance = (hexColor: string) => {
    const channels = [1, 3, 5].map((i) => {
      const c = parseInt(hexColor.slice(i, i + 2), 16) / 255;
      return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2];
  };
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

describe("Checkbox", () => {
  it("未选中时用一个独立的控件边界 token,而不是分隔线用的 border/input", () => {
    render(<Checkbox aria-label="全选" />);

    const box = screen.getByRole("checkbox", { name: "全选" });
    const borderToken = Array.from(box.classList)
      .map((c) => /^border-([a-z-]+)$/.exec(c)?.[1])
      .find(Boolean);

    expect(borderToken).toBeDefined();
    // 分隔线（border）与表单字段（input）在两个主题下都是同一个安静的值,
    // 拿它当"边框即控件"的边界必然不达标 —— 复选框必须另有出处。
    expect(borderToken).not.toBe("border");
    expect(borderToken).not.toBe("input");
  });

  it.each([
    [":root" as const, "亮色"],
    [".dark" as const, "暗色"],
  ])("%s(%s)下,未选中的边框对 secondary / card 都有 3:1", (scope, _theme) => {
    render(<Checkbox aria-label="全选" />);

    const box = screen.getByRole("checkbox", { name: "全选" });
    const borderToken = Array.from(box.classList)
      .map((c) => /^border-([a-z-]+)$/.exec(c)?.[1])
      .find(Boolean) as string;

    const tokens = readTokens(scope);
    const borderColor = tokens[borderToken];
    expect(borderColor, `tokens.css 里没有 --${borderToken}`).toBeDefined();

    for (const surface of ["secondary", "card"] as const) {
      expect(
        contrast(borderColor, tokens[surface]),
        `--${borderToken} ${borderColor} 落在 --${surface} ${tokens[surface]} 上`,
      ).toBeGreaterThanOrEqual(NON_TEXT_CONTRAST_MIN);
    }
  });
});
