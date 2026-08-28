import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Input } from "./input";

describe("Input", () => {
  it("disables browser text assistance by default", () => {
    render(<Input aria-label="名称" />);

    const input = screen.getByRole("textbox", { name: "名称" });

    expect(input).toHaveAttribute("autocomplete", "off");
    expect(input).toHaveAttribute("autocorrect", "off");
    expect(input).toHaveAttribute("autocapitalize", "off");
    expect(input).toHaveAttribute("spellcheck", "false");
  });

  it("allows callers to opt into a specific autocomplete purpose", () => {
    render(<Input aria-label="邮箱" autoComplete="email" spellCheck />);

    const input = screen.getByRole("textbox", { name: "邮箱" });

    expect(input).toHaveAttribute("autocomplete", "email");
    expect(input).toHaveAttribute("spellcheck", "true");
  });
});
