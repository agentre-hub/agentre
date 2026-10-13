import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { contrast, readTokens } from "../styles/__testing__/tokens";

import { Button } from "./button";
import { Select, SelectTrigger, SelectValue } from "./select";

// 下拉触发器是字段的一种（docs/design.md §3.4），静止态的边由 field-edge.test.tsx
// 把关。这里只管悬停：那是用户正要点它的那一刻，边不能在这时候变淡——此前
// `hover:border-border-strong` 把它降到 1.48（暗色 1.53），是旧边框色留下的写法。

function renderTrigger(): string[] {
  render(
    <Select defaultValue="a">
      <SelectTrigger aria-label="引擎">
        <SelectValue />
      </SelectTrigger>
    </Select>,
  );
  return Array.from(screen.getByRole("combobox", { name: "引擎" }).classList);
}

describe("SelectTrigger · 悬停", () => {
  it.each([
    [":root" as const, "亮色"],
    [".dark" as const, "暗色"],
  ])("%s(%s)下悬停不会让边变得比静止时更淡", (scope, _theme) => {
    const classes = renderTrigger();
    const t = readTokens(scope);

    const rest = classes
      .map((c) => /^border-([a-z]+(?:-[a-z]+)*)$/.exec(c)?.[1])
      .find((token) => token && t[token]) as string;
    const hover = classes
      .map((c) => /^hover:border-([a-z]+(?:-[a-z]+)*)$/.exec(c)?.[1])
      .find((token) => token && t[token]);

    expect(rest, "触发器没有静止态的边 token").toBeDefined();
    // 悬停不改边是允许的（那就沿用静止态的边）；改了就不能更淡。
    const hoverEdge = t[hover ?? rest];
    for (const surface of ["card", "secondary"]) {
      expect(
        contrast(hoverEdge, t[surface]),
        `悬停的边 ${hoverEdge} 落在 --${surface} 上`,
      ).toBeGreaterThanOrEqual(contrast(t[rest], t[surface]));
    }
  });

  it("悬停有底色反馈", () => {
    // 边不动了，悬停就得另有可见的变化，否则这个控件点上去之前没有任何回应。
    expect(renderTrigger().some((c) => /^hover:bg-/.test(c))).toBe(true);
  });

  it("悬停的底色与 outline 按钮是同一组", () => {
    // 两颗控件常常并排（筛选条、表单页脚），悬停反馈一深一浅就成了两套规则。
    // 更淡的 accent/50 叠在白卡片上只有 1.14，够不到「肉眼能觉察」的 1.15；
    // outline 按钮用的实心 accent 是 1.32。
    const hoverFills = (classes: string[]) =>
      classes.filter((c) => /^(?:dark:)?hover:bg-/.test(c)).sort();

    const trigger = hoverFills(renderTrigger());
    render(<Button variant="outline">取消</Button>);
    const outline = hoverFills(
      Array.from(screen.getByRole("button", { name: "取消" }).classList),
    );

    expect(trigger).toEqual(outline);
  });
});
