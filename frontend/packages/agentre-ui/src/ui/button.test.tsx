import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { contrast, mix, readTokens } from "../styles/__testing__/tokens";

import { Button } from "./button";

// 按钮的轮廓靠什么读出来。
//
// 边分四档（docs/design.md §3.4）：分隔线 `border`、可点控件的边 `input`、
// 字段的边 `field-border`、控件本体 `control-border`。按钮里有文字，边只是辅助，
// 所以落在第二档——既不能是分隔线（亮色对 card 1.27，对话框页脚上 1.22，
// 几乎看不见），也不必是 3:1 的那档（太重，那是给「轮廓就是控件」的复选框的）。
//
// 和 checkbox.test.tsx 同一个做法：不断言类名叫什么，而是从渲染出来的 class 里
// 反查 token，去 tokens.css 取色值，对真实表面算对比度。

/** 交互反馈没有法定门槛，与 tokens.test.ts 用同一条「肉眼能觉察」的工程线。 */
const PERCEIVABLE_MIN = 1.15;

const THEMES: Array<[":root" | ".dark", string]> = [
  [":root", "亮色"],
  [".dark", "暗色"],
];

describe("Button · outline 的边", () => {
  it("两个主题走同一个 token，不按主题换档", () => {
    render(<Button variant="outline">取消</Button>);

    const classes = Array.from(
      screen.getByRole("button", { name: "取消" }).classList,
    );

    // 此前亮色落到分隔线色、暗色才用 control-border：同一个按钮两个主题两套规则。
    expect(classes.filter((c) => /^dark:border-[a-z-]+$/.test(c))).toEqual([]);
    expect(
      classes.filter((c) => /^border-[a-z]+(-[a-z]+)*$/.test(c)),
    ).toHaveLength(1);
  });

  it.each(THEMES)(
    "%s(%s)下比分隔线明显，又轻于「轮廓即控件」那一档",
    (scope, _theme) => {
      render(<Button variant="outline">取消</Button>);

      const edge = Array.from(
        screen.getByRole("button", { name: "取消" }).classList,
      )
        .map((c) => /^border-([a-z]+(?:-[a-z]+)*)$/.exec(c)?.[1])
        .find(Boolean) as string;
      const t = readTokens(scope);
      expect(t[edge], `tokens.css 里没有 --${edge}`).toBeDefined();

      // card 是表单与卡片；secondary/40 叠 card 是对话框页脚，「取消」最常待的地方。
      for (const surface of [t.card, mix(t.secondary, t.card, 0.4)]) {
        const own = contrast(t[edge], surface);
        expect(own, `--${edge} 落在 ${surface} 上`).toBeGreaterThan(
          contrast(t.border, surface),
        );
        expect(own, `--${edge} 落在 ${surface} 上`).toBeLessThan(
          contrast(t["control-border"], surface),
        );
        expect(own, `--${edge} 落在 ${surface} 上`).toBeGreaterThanOrEqual(1.5);
      }
    },
  );
});

describe("Button · secondary 的填充", () => {
  /** `bg-foreground/8` → ["foreground", 0.08]；没有不透明度时是 1。 */
  function fillOf(classes: string[], prefix: string): [string, number] {
    const hit = classes
      .map((c) =>
        new RegExp(`^${prefix}bg-([a-z]+(?:-[a-z]+)*)(?:/(\\d+))?$`).exec(c),
      )
      .find(Boolean);
    if (!hit) throw new Error(`没有 ${prefix}bg-* 类：${classes.join(" ")}`);
    return [hit[1], hit[2] ? Number(hit[2]) / 100 : 1];
  }

  it.each(THEMES)(
    "%s(%s)下在它会落到的每个表面上都与底可分，悬停再深一档",
    (scope, _theme) => {
      render(<Button variant="secondary">测试连接</Button>);

      const classes = Array.from(
        screen.getByRole("button", { name: "测试连接" }).classList,
      );
      const t = readTokens(scope);
      const [restToken, restAlpha] = fillOf(classes, "");
      const [hoverToken, hoverAlpha] = fillOf(classes, "hover:");

      // 此前填充是 --secondary 本身：落在 secondary 面与暗色弹层上是 1.00，按钮只剩文字。
      for (const name of ["card", "background", "secondary", "popover"]) {
        const surface = t[name];
        const rest = contrast(mix(t[restToken], surface, restAlpha), surface);
        const hover = contrast(
          mix(t[hoverToken], surface, hoverAlpha),
          surface,
        );

        expect(
          rest,
          `静止填充落在 --${name} ${surface} 上`,
        ).toBeGreaterThanOrEqual(PERCEIVABLE_MIN);
        expect(hover, `悬停填充落在 --${name} ${surface} 上`).toBeGreaterThan(
          rest,
        );
      }
    },
  );
});
