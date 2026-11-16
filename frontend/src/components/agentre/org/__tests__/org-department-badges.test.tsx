import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { DepartmentIconBadge, LeadBadge } from "../org-department-badges";

describe("DepartmentIconBadge", () => {
  it("Given a preset icon, When the badge renders, Then it draws the preset image instead of an icon or a character", () => {
    render(
      <DepartmentIconBadge
        name="Engineering"
        icon="preset:team-dev"
        accentColor="agent-3"
        className="size-7 rounded-md"
      />,
    );

    const mark = screen.getByRole("img", { name: "Engineering" });
    expect(mark.querySelector("img")?.getAttribute("src")).toContain(
      "team-dev.webp",
    );
    expect(mark).not.toHaveTextContent("E");
  });

  it("Given a registry icon, When the badge renders, Then it draws that icon", () => {
    render(
      <DepartmentIconBadge
        name="Engineering"
        icon="hammer"
        accentColor="agent-3"
      />,
    );

    const mark = screen.getByRole("img", { name: "Engineering" });
    expect(mark.querySelector("svg")).toHaveClass("lucide-hammer");
  });

  it("Given no icon, When the badge renders, Then it draws the department's first character on its accent colour", () => {
    render(
      <DepartmentIconBadge name="Engineering" icon="" accentColor="agent-3" />,
    );

    const mark = screen.getByRole("img", { name: "Engineering" });
    expect(mark).toHaveTextContent(/^E$/);
    expect(mark.style.backgroundColor).toBe("var(--agent-3)");
    expect(mark).not.toHaveTextContent("🧩");
  });
});

describe("LeadBadge", () => {
  it("Given a lead with an identity color, When the badge renders, Then only the crown remains, colored, with no text", () => {
    // 身份色是为「色块 + 白字」调的，当文字对 bg-secondary 对比不足；图形只要 3:1，
    // 所以颜色留在皇冠上。
    render(<LeadBadge color="agent-7" />);

    const badge = screen.getByRole("img", { name: "Leader" });
    const crown = badge.querySelector("svg");

    expect(screen.queryByText("LEAD")).not.toBeInTheDocument();
    expect(badge).toHaveTextContent("");
    expect(crown).toHaveClass("text-agent-7");
  });

  it("Given the badge, When hovered, Then the tip is the same name as the accessible name", () => {
    render(<LeadBadge color="agent-1" compact />);

    expect(screen.getByRole("img", { name: "Leader" })).toHaveAttribute(
      "title",
      "Leader",
    );
  });
});
