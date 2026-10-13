import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { RadioGroup, RadioGroupItem } from "./radio-group";

/**
 * 单选项的圆点是图形，不是文字。
 *
 * `-text` 角色只给要读的字用（docs/design.md §3.5 / §3.11）；圆点、圆圈这类图形
 * 留在填充色上。这一颗里圆点经 `fill-current` 取 Item 的文字色，而圈是
 * `border-primary`：把 Item 换成 `text-primary-text`，暗色下圆点（#8eb6dc）就和
 * 自己的圈（#5b8dbf）成了两个蓝。
 */
describe("RadioGroupItem", () => {
  it("Given a radio item, When rendered, Then its dot takes the same fill color as its ring", () => {
    render(
      <RadioGroup defaultValue="a">
        <RadioGroupItem value="a" aria-label="选项 A" />
      </RadioGroup>,
    );

    const item = screen.getByRole("radio", { name: "选项 A" });
    expect(item).toHaveClass("border-primary", "text-primary");
    expect(item).not.toHaveClass("text-primary-text");
  });

  it("Given a radio item, When it takes keyboard focus, Then it draws the shared button-like outline", () => {
    render(
      <RadioGroup defaultValue="a">
        <RadioGroupItem value="a" aria-label="选项 A" />
      </RadioGroup>,
    );

    expect(screen.getByRole("radio", { name: "选项 A" })).toHaveClass(
      "focus-visible:focus-outline",
    );
  });
});
