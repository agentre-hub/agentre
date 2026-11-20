// 手机触屏档（touch）：执行目标行、工具清单与组头收放三角的点按区。
// 视觉尽量不变，命中区 ≥44px；不传 touch 时与桌面一致。
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { OrgExecTargetRow } from "./org-exec-target-row";
import { OrgGroupHeader } from "./org-group-header";
import type { OrgIndexGroup } from "./org-index-model";
import { OrgToolList } from "./org-tool-list";

const backend = {
  id: 1,
  name: "Claude Code",
  type: "claudecode",
  deviceName: "MacBook",
} as never;

function renderRow(touch?: boolean) {
  return render(
    <OrgExecTargetRow
      index={0}
      total={2}
      backend={backend}
      status={{ available: true, reason: "" }}
      isFirstAvailable
      onMoveDown={vi.fn()}
      onRemove={vi.fn()}
      skillsSupported
      touch={touch}
    />,
  );
}

const svgClass = (el: HTMLElement) =>
  el.querySelector("svg")?.getAttribute("class") ?? "";

describe("OrgExecTargetRow touch", () => {
  it("柄与移除键：图标 16px，按钮本身 44×44（负外边距收回占位，不靠会被裁掉的 ::before）", () => {
    renderRow(true);
    for (const name of [/Reorder target/, /^Remove$/]) {
      const btn = screen.getByRole("button", { name });
      expect(btn).toHaveClass("size-11", "-m-3");
      expect(svgClass(btn)).toContain("size-4");
    }
  });

  it("行名 14px；技能入口视觉 28px、点按区 44px", () => {
    renderRow(true);
    expect(screen.getByText("Local machine")).toHaveClass("text-sm");
    const skills = screen.getByRole("button", { name: /^Skills for/ });
    expect(skills).toHaveClass("h-7", "relative", "before:-inset-y-2");
  });

  it("不传 touch：与桌面一致", () => {
    renderRow();
    const remove = screen.getByRole("button", { name: /^Remove$/ });
    expect(remove).not.toHaveClass("size-11");
    expect(svgClass(remove)).toContain("size-3.5");
    expect(screen.getByText("Local machine")).toHaveClass("text-xs");
  });
});

describe("OrgToolList touch", () => {
  it("授权键视觉 28px、点按区 44px", () => {
    render(
      <OrgToolList
        toolKeys={["org"]}
        agentTools={[]}
        onToggleGrant={vi.fn()}
        touch
      />,
    );
    const grant = screen.getByRole("button", { name: /^Grant / });
    expect(grant).toHaveClass("h-7", "relative", "before:-inset-y-2");
  });

  it("不传 touch：与桌面一致", () => {
    render(
      <OrgToolList
        toolKeys={["org"]}
        agentTools={[]}
        onToggleGrant={vi.fn()}
      />,
    );
    expect(screen.getByRole("button", { name: /^Grant / })).not.toHaveClass(
      "h-7",
    );
  });
});

describe("OrgGroupHeader 摘要档的收放三角", () => {
  const group = {
    depth: 0,
    department: { id: 3, name: "Eng", memberCount: 5 },
  } as OrgIndexGroup;

  it("三角 16px，点按区 44×44", () => {
    render(
      <OrgGroupHeader
        group={group}
        selected={false}
        onSelect={vi.fn()}
        summary
        onToggleExpanded={vi.fn()}
      />,
    );
    const toggle = screen.getByTestId("org-group-toggle-3");
    expect(toggle).toHaveClass("relative", "before:-inset-3.5");
    expect(svgClass(toggle)).toContain("size-4");
  });

  it("不传 summary：三角仍是 12px", () => {
    render(
      <OrgGroupHeader
        group={group}
        selected={false}
        onSelect={vi.fn()}
        onToggleExpanded={vi.fn()}
      />,
    );
    const toggle = screen.getByTestId("org-group-toggle-3");
    expect(toggle).not.toHaveClass("before:-inset-3.5");
    expect(svgClass(toggle)).toContain("size-3");
  });
});
