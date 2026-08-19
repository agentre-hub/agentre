import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { ProjectGlyph } from "./project-glyph";

describe("ProjectGlyph", () => {
  it("给项目色方块 + 项目名首字：组头与行里是同一枚字形，只是尺寸不同", () => {
    render(<ProjectGlyph project={{ name: "Agentre", color: "agent-7" }} />);

    const glyph = screen.getByRole("img", { name: "Agentre" });
    expect(glyph).toHaveTextContent("A");
    expect(glyph.style.backgroundColor).toBe("var(--agent-7)");
  });

  it("宿主给了字形就画宿主那一枚：icon key 换成图标的注册表在宿主，不在包里", () => {
    render(
      <ProjectGlyph
        project={{ name: "Agentre", color: "agent-7" }}
        glyph={<span data-testid="host-icon">🚀</span>}
      />,
    );

    expect(screen.getByTestId("host-icon")).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "Agentre" })).not.toHaveTextContent(
      "A",
    );
  });

  it("颜色不是调色板 token 时退回中性面，而不是拼一个解析不出来的 var()", () => {
    // agentre-server 的项目色是十六进制串，不是 token —— 直接 `var(--#3B82F6)`
    // 在语法上合法、解析结果为空，调用方分辨不出「没颜色」和「颜色坏了」。
    render(<ProjectGlyph project={{ name: "Agentre", color: "#3B82F6" }} />);

    const glyph = screen.getByRole("img", { name: "Agentre" });
    expect(glyph.style.backgroundColor).toBe("");
    expect(glyph.className).toContain("bg-secondary");
  });

  it("不属于任何项目时槽位照占，字形置灰（决策 4）", () => {
    const { container } = render(<ProjectGlyph project={null} />);

    // 自由会话没有身份可言，读屏不该把它念成一个项目。
    expect(screen.queryByRole("img")).toBeNull();
    const svg = container.querySelector("svg");
    expect(svg?.getAttribute("class")).toContain("text-subtle-foreground");
  });

  it("尺寸由调用方给：行里 14px、组头 24px 是同一枚字形的两种尺寸", () => {
    render(
      <ProjectGlyph
        project={{ name: "Agentre", color: "agent-7" }}
        className="size-6"
        testId="group-header-glyph"
      />,
    );

    const glyph = screen.getByTestId("group-header-glyph");
    expect(glyph.className).toContain("size-6");
    expect(glyph.className).not.toContain("size-3.5");
  });
});
