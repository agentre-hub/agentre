import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { Checkbox } from "./checkbox";
import { Input } from "./input";
import { SearchInput } from "./search-input";
import { Select, SelectTrigger, SelectValue } from "./select";
import { Textarea } from "./textarea";

// 字段（输入框 / 文本域 / 下拉 / 搜索框）：无边填充。
//
// 静止态是相对前景色 5% 的填充（悬停 7%），不画边；聚焦或展开时底色回到内容页色
// （bg-sheet），只剩焦点环；aria-invalid 时是危险色的环。四种字段共用同一个填充
// token，并排出现在一张表单里不能各有各的底色。

function classesOf(el: Element | null): string[] {
  return Array.from(el?.classList ?? []);
}

/** 不带变体前缀的 `border` / `border-*`：静止态画了边。 */
function restingEdges(classes: string[]): string[] {
  return classes.filter((c) => /^border(?:-|$)/.test(c));
}

function renderFields(): Record<string, { el: Element; focus: string }> {
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
    Input: {
      el: screen.getByRole("textbox", { name: "名称" }),
      focus: "focus-visible",
    },
    Textarea: {
      el: screen.getByRole("textbox", { name: "说明" }),
      focus: "focus-visible",
    },
    SelectTrigger: {
      el: screen.getByRole("combobox", { name: "归属" }),
      focus: "focus-visible",
    },
    SearchInput: {
      el: screen
        .getByRole("searchbox", { name: "搜索" })
        .closest('[data-slot="search-input"]') as Element,
      focus: "focus-within",
    },
  };
}

describe("字段的填充", () => {
  it("四种字段静止态都是同一个字段填充、悬停同一个加深档", () => {
    for (const [name, { el }] of Object.entries(renderFields())) {
      const classes = classesOf(el);
      expect(classes, name).toContain("bg-field-fill");
      expect(classes, name).toContain("hover:bg-field-fill-hover");
    }
  });

  it("四种字段静止态都没有边", () => {
    for (const [name, { el }] of Object.entries(renderFields())) {
      expect(restingEdges(classesOf(el)), name).toEqual([]);
    }
  });

  it("聚焦时底色回到内容页色，只画焦点环（不是 focus-halo 的边 + 光晕）", () => {
    for (const [name, { el, focus }] of Object.entries(renderFields())) {
      const classes = classesOf(el);
      expect(classes, name).toContain(`${focus}:bg-sheet`);
      expect(classes, name).toContain(`${focus}:focus-outline`);
      expect(
        classes.filter((c) => c.includes("focus-halo")),
        name,
      ).toEqual([]);
    }
  });

  it("下拉触发器展开时与聚焦同样：回到内容页色、只有焦点环", () => {
    const classes = classesOf(renderFields().SelectTrigger.el);
    expect(classes).toContain("data-[state=open]:bg-sheet");
    expect(classes).toContain("data-[state=open]:focus-outline");
  });

  it("输入框、文本域、下拉触发器 aria-invalid 时是危险色的环，而不是危险色的边", () => {
    const fields = renderFields();
    for (const name of ["Input", "Textarea", "SelectTrigger"]) {
      const classes = classesOf(fields[name].el);
      expect(classes, name).toContain("aria-invalid:ring-destructive");
      expect(
        classes.filter((c) => c.startsWith("aria-invalid:border")),
        name,
      ).toEqual([]);
    }
  });
});

describe("就地编辑的 variant=inline", () => {
  it.each([
    ["Input", () => <Input variant="inline" aria-label="标题" />],
    ["Textarea", () => <Textarea variant="inline" aria-label="标题" />],
  ])("%s 是另一种写法：不填充、不画边，聚焦只出焦点环", (_n, ui) => {
    render(ui());
    const el = screen.getByRole("textbox", { name: "标题" });
    const classes = classesOf(el);

    expect(el).toHaveAttribute("data-variant", "inline");
    expect(classes.filter((c) => c.includes("field-fill"))).toEqual([]);
    expect(restingEdges(classes)).toEqual([]);
    expect(classes).toContain("focus-visible:focus-outline");
    expect(classes).toContain("bg-transparent");
  });

  it.each([
    ["Input", () => <Input aria-label="标题" />],
    ["Textarea", () => <Textarea aria-label="标题" />],
  ])("%s 不传 variant 时是默认的填充字段", (_n, ui) => {
    render(ui());
    expect(screen.getByRole("textbox", { name: "标题" })).toHaveAttribute(
      "data-variant",
      "default",
    );
  });
});

describe("复选框", () => {
  it("边不变：仍然是控件本体的边", () => {
    render(<Checkbox aria-label="全选" />);
    expect(classesOf(screen.getByRole("checkbox"))).toContain(
      "border-control-border",
    );
  });
});
