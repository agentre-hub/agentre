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

  it("项目自己选了图标就画那一枚：key 由包里的词表解，宿主不必递节点", () => {
    // 此前这里是一个 `glyph` 插槽：词表在包里、解 key 的那一步却留在宿主，于是
    // 桌面端填了、agentre-server 没填 —— 同一个项目在控制台里永远只是一个字母。
    render(
      <ProjectGlyph
        project={{ name: "Agentre", color: "agent-7", icon: "rocket" }}
      />,
    );

    const glyph = screen.getByRole("img", { name: "Agentre" });
    expect(glyph.querySelector("svg")?.getAttribute("class")).toContain(
      "lucide-rocket",
    );
    expect(glyph).not.toHaveTextContent("A");
  });

  it("词表不认得的 key 退回项目名首字：猜一枚图标比一个字母更糟", () => {
    render(
      <ProjectGlyph
        project={{ name: "Agentre", color: "agent-7", icon: "nope" }}
      />,
    );

    expect(screen.getByRole("img", { name: "Agentre" })).toHaveTextContent("A");
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
    expect(svg?.getAttribute("class")).toContain("text-decorative-foreground");
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
