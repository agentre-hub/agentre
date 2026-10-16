import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "./select";

// 下拉触发器是字段的一种（静止态无边、填充，见 field-edge.test.tsx）。
// 这里只管悬停与浮层：悬停在填充上加深一档，展开的列表只靠表面色与阴影立起来，不画边。

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
  it("悬停用字段填充的加深档，不改边（没有边）", () => {
    const classes = renderTrigger();

    expect(classes).toContain("hover:bg-field-fill-hover");
    expect(classes.filter((c) => /^(?:hover:)?border(?:-|$)/.test(c))).toEqual(
      [],
    );
  });
});

describe("SelectContent", () => {
  it("展开的列表没有边，也不叠 1px 阴影环", () => {
    render(
      <Select open defaultValue="a">
        <SelectTrigger aria-label="引擎">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="a">甲</SelectItem>
        </SelectContent>
      </Select>,
    );

    const classes = Array.from(
      document.querySelector('[data-slot="select-content"]')?.classList ?? [],
    );
    expect(classes.length).toBeGreaterThan(0);
    expect(classes.filter((c) => /^border(?:-|$)/.test(c))).toEqual([]);
    expect(classes.filter((c) => c.startsWith("shadow-["))).toEqual([]);
  });
});
