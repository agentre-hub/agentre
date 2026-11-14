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
  it("「N 人 ›」是一个整体可点控件，替掉负责人徽标与动作，命中区 ≥44px", async () => {
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
    expect(screen.queryByRole("img", { name: "Leader" })).toBeNull();
    expect(ctl).not.toHaveTextContent("Ann");
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

  it("不传 summary：负责人徽标与动作照旧", () => {
    render(
      <OrgGroupHeader
        group={group}
        selected={false}
        onSelect={vi.fn()}
        actions={<button type="button">plus</button>}
      />,
    );
    expect(screen.getByText("plus")).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "Leader" })).toBeInTheDocument();
  });
});

describe("OrgGroupHeader 负责人徽标", () => {
  it("Given 部门有负责人 When 渲染 Then 皇冠记号名与提示为 Leader，后跟纯文本姓名，且不出现 Lead ·", () => {
    render(
      <OrgGroupHeader group={group} selected={false} onSelect={vi.fn()} />,
    );
    const mark = screen.getByRole("img", { name: "Leader" });
    expect(mark).toHaveAttribute("title", "Leader");
    expect(mark.querySelector("svg")).not.toBeNull();
    const ctl = screen.getByTestId("org-group-select-3");
    expect(ctl).toContainElement(mark);
    expect(screen.getByText("Ann")).toBeInTheDocument();
    expect(mark.nextElementSibling).toHaveTextContent(/^Ann$/);
    expect(ctl).not.toHaveTextContent(/Lead\b/);
  });

  it("Given 部门没有负责人 When 渲染 Then 不画皇冠也不画姓名位", () => {
    render(
      <OrgGroupHeader
        group={{
          ...group,
          department: { ...group.department, leadAgentName: undefined },
        }}
        selected={false}
        onSelect={vi.fn()}
      />,
    );
    expect(screen.queryByRole("img", { name: "Leader" })).toBeNull();
    expect(screen.getByTestId("org-group-select-3")).toHaveTextContent(/^Eng/);
  });
});
