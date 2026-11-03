import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { presetForKey } from "../presets/preset-registry";

import { DepartmentMark } from "./department-mark";

describe("DepartmentMark", () => {
  it("部门图标是预设 key 就整格画预设图：不叠主题色底、不画首字", () => {
    const preset = presetForKey("preset:team-dev");
    render(
      <DepartmentMark
        name="Engineering"
        icon="preset:team-dev"
        accentColor="agent-3"
      />,
    );

    const glyph = screen.getByRole("img", { name: "Engineering" });
    expect(glyph.querySelector("img")?.getAttribute("src")).toBe(preset?.src);
    expect(glyph).not.toHaveTextContent("E");
    expect(glyph.style.backgroundColor).toBe("");
  });

  it("预设图是装饰：可及名仍是部门名，不念预设名", () => {
    const { container } = render(
      <DepartmentMark name="Engineering" icon="preset:team-dev" />,
    );

    expect(
      screen.getByRole("img", { name: "Engineering" }),
    ).toBeInTheDocument();
    expect(screen.getAllByRole("img")).toHaveLength(1);
    expect(container.querySelector("img")?.getAttribute("alt")).toBe("");
  });

  it("认不出的 preset key 退回部门名首字，画在主题色上", () => {
    render(
      <DepartmentMark
        name="Engineering"
        icon="preset:not-registered-yet"
        accentColor="agent-3"
      />,
    );

    const glyph = screen.getByRole("img", { name: "Engineering" });
    expect(glyph).toHaveTextContent(/^E$/);
    expect(glyph.querySelector("img")).toBeNull();
    expect(glyph.style.backgroundColor).toBe("var(--agent-3)");
  });

  it("词表里认得的图标 key 就画那一枚，不画首字", () => {
    render(
      <DepartmentMark name="Engineering" icon="rocket" accentColor="agent-3" />,
    );

    const glyph = screen.getByRole("img", { name: "Engineering" });
    expect(glyph.querySelector("svg")?.getAttribute("class")).toContain(
      "lucide-rocket",
    );
    expect(glyph).not.toHaveTextContent("E");
  });

  it("没有图标时画部门名首字（不是首字母缩写）", () => {
    render(<DepartmentMark name="Platform Team" accentColor="agent-3" />);

    const glyph = screen.getByRole("img", { name: "Platform Team" });
    expect(glyph).toHaveTextContent(/^P$/);
    expect(glyph.style.backgroundColor).toBe("var(--agent-3)");
  });
});
