import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import {
  contrast,
  edgeTokenOf,
  readTokens,
} from "../styles/__testing__/tokens";

import { Button } from "./button";
import { Checkbox } from "./checkbox";
import { Input } from "./input";
import { SearchInput } from "./search-input";
import { Select, SelectTrigger, SelectValue } from "./select";
import { Textarea } from "./textarea";

// 字段（输入框 / 文本域 / 下拉 / 搜索框）静止时的边。
//
// 它们在一张表单里并排出现，边必须是同一个取值；这一档落在 outline 按钮与未选中
// 复选框之间（docs/design.md §3.4）：比按钮实——字段可能是空的，没有文字替它
// 说明自己；比复选框轻——字段有标签、占位和固定的位置可认，不必守 3:1。
//
// 和 checkbox.test.tsx 同一个做法：不断言类名叫什么，而是从渲染出来的 class 里
// 反查 token，去 tokens.css 取色值，对真实表面算对比度。

const THEMES: Array<[":root" | ".dark", string]> = [
  [":root", "亮色"],
  [".dark", "暗色"],
];

function edgeOf(el: Element): string {
  const edge = edgeTokenOf(el);
  expect(
    edge,
    `${el.outerHTML.slice(0, 80)} 没有静止态的边 token`,
  ).toBeDefined();
  return edge as string;
}

function renderFields(): Record<string, string> {
  render(
    <>
      <Input aria-label="名称" />
      <Textarea aria-label="说明" />
      <Select defaultValue="a">
        <SelectTrigger aria-label="归属">
          <SelectValue />
        </SelectTrigger>
      </Select>
      <SearchInput value="" onChange={vi.fn()} aria-label="搜索" />
    </>,
  );
  return {
    Input: edgeOf(screen.getByRole("textbox", { name: "名称" })),
    Textarea: edgeOf(screen.getByRole("textbox", { name: "说明" })),
    SelectTrigger: edgeOf(screen.getByRole("combobox", { name: "归属" })),
    SearchInput: edgeOf(
      screen
        .getByRole("searchbox", { name: "搜索" })
        .closest('[data-slot="search-input"]') as Element,
    ),
  };
}

describe("字段的边", () => {
  it("输入框、文本域、下拉、搜索框用同一个 token", () => {
    const edges = renderFields();

    expect(new Set(Object.values(edges)).size, JSON.stringify(edges)).toBe(1);
  });

  it.each(THEMES)(
    "%s(%s)下比 outline 按钮的边实，比未选中复选框的边轻",
    (scope, _theme) => {
      const field = renderFields().Input;
      render(
        <>
          <Button variant="outline">取消</Button>
          <Checkbox aria-label="全选" />
        </>,
      );
      const button = edgeOf(screen.getByRole("button", { name: "取消" }));
      const checkbox = edgeOf(screen.getByRole("checkbox", { name: "全选" }));
      const t = readTokens(scope);

      for (const surface of ["card", "background", "secondary"]) {
        const own = contrast(t[field], t[surface]);
        expect(
          own,
          `--${field} 落在 --${surface} 上应当比按钮的 --${button} 实`,
        ).toBeGreaterThan(contrast(t[button], t[surface]));
        expect(
          own,
          `--${field} 落在 --${surface} 上应当比复选框的 --${checkbox} 轻`,
        ).toBeLessThan(contrast(t[checkbox], t[surface]));
      }
    },
  );
});
