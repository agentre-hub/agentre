import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { OrgGroupHeader } from "./org-group-header";
import type { OrgIndexGroup } from "./org-index-model";

const group = {
  depth: 0,
  department: { id: 3, name: "Eng", leadAgentName: "Ann", memberCount: 5 },
} as OrgIndexGroup;

describe("OrgGroupHeader 摘要档（summary）", () => {
  it("「N 人 ›」是一个整体可点控件，替掉 Lead 徽标与动作，命中区 ≥44px", async () => {
    const onSelect = vi.fn();
    render(
      <OrgGroupHeader
        group={group}
        selected={false}
        onSelect={onSelect}
        summary
        actions={<button type="button">plus</button>}
      />,
    );
    const ctl = screen.getByTestId("org-group-select-3");
    expect(ctl).toHaveTextContent("Eng");
    expect(ctl).toHaveTextContent(/5.*›/);
    expect(ctl.className).toContain("min-h-11");
    expect(screen.queryByText(/Lead/)).toBeNull();
    expect(screen.queryByText("plus")).toBeNull();
    await userEvent.click(ctl);
    expect(onSelect).toHaveBeenCalledWith({ kind: "department", id: 3 });
  });

  it("英文单数：一个人的组写「1 person ›」，不是「1 people ›」", () => {
    render(
      <OrgGroupHeader
        group={{
          ...group,
          department: { ...group.department, memberCount: 1 },
        }}
        selected={false}
        onSelect={vi.fn()}
        summary
      />,
    );
    expect(screen.getByTestId("org-group-select-3")).toHaveTextContent(
      "1 person ›",
    );
  });

  it("不传 summary：Lead 徽标与动作照旧", () => {
    render(
      <OrgGroupHeader
        group={group}
        selected={false}
        onSelect={vi.fn()}
        actions={<button type="button">plus</button>}
      />,
    );
    expect(screen.getByText(/Lead/)).toBeInTheDocument();
    expect(screen.getByText("plus")).toBeInTheDocument();
  });
});
