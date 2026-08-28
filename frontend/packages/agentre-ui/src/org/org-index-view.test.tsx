import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { OrgAgentRow } from "./org-agent-row";
import { OrgGroupHeader } from "./org-group-header";
import { OrgInsertLine } from "./org-insert-line";
import {
  ORG_INDENT_BASE_HEADER,
  ORG_INDENT_BASE_ROW,
  ORG_INDENT_STEP,
  ORG_RAIL_OFFSET,
} from "./org-indent";
import { buildOrgIndex, EMPTY_ORG_FILTERS } from "./org-index-model";
import type { OrgAgentModel, OrgDepartmentModel } from "./types";

/**
 * 这一组用例扮演的是 **agentre-server**：没有 Wails、没有桌面端 store、没有 dnd-kit，
 * 只有普通对象与回调。桌面端那份「拖着走」的行为由宿主的 org-index 用例覆盖，
 * 这里钉的是「不给拖拽装配时，索引照样画得出来」这条跨仓前提。
 */

const departments: OrgDepartmentModel[] = [
  { id: 1, name: "Engineering", leadAgentName: "Eva", memberCount: 2 },
  { id: 2, name: "Platform", parentId: 1, memberCount: 0 },
];

const agents: OrgAgentModel[] = [
  { id: 1, name: "CEO", systemBadge: "DEFAULT" },
  { id: 2, name: "Eva", description: "Head of engineering", departmentId: 1 },
  { id: 3, name: "Bob", parentAgentId: 2, backend: { name: "Claude Code" } },
  { id: 4, name: "Doc", departmentId: 1, noExecTarget: true },
];

function model() {
  return buildOrgIndex({ agents, departments, filters: EMPTY_ORG_FILTERS });
}

describe("OrgAgentRow（只吃 props）", () => {
  it("画出名字、描述、后端徽标与行内的 ↳ 主管", () => {
    const rows = model().groups[0].rows;
    const subordinate = rows.find((row) => row.agent.id === 3)!;

    render(
      <OrgAgentRow
        row={subordinate}
        indent={0}
        selected={false}
        onSelect={vi.fn()}
      />,
    );

    expect(screen.getByTestId("org-row-3")).toBeInTheDocument();
    expect(screen.getByText("Bob")).toBeInTheDocument();
    expect(screen.getByText("Claude Code")).toBeInTheDocument();
    // 下属与主管平排，从属关系写在行内。
    expect(screen.getByText("↳ Eva")).toBeInTheDocument();
  });

  it("没给拖拽绑定就没有拖拽柄，但占位还在（左缘仍与邻居对齐）", () => {
    const row = model().groups[0].rows[0];

    render(
      <OrgAgentRow row={row} indent={0} selected={false} onSelect={vi.fn()} />,
    );

    expect(screen.queryByTestId(`org-row-handle-${row.agent.id}`)).toBeNull();
    // 只剩「选中」那一个按钮。
    expect(screen.getAllByRole("button")).toHaveLength(1);
  });

  it("给了拖拽绑定才长出柄，柄上的事件原样交给宿主", async () => {
    const user = userEvent.setup();
    const onKeyDown = vi.fn();
    const row = model().groups[0].rows[0];

    render(
      <OrgAgentRow
        row={row}
        indent={0}
        selected={false}
        onSelect={vi.fn()}
        dragHandle={{ onKeyDown }}
      />,
    );

    const handle = screen.getByTestId(`org-row-handle-${row.agent.id}`);
    handle.focus();
    await user.keyboard(" ");
    expect(onKeyDown).toHaveBeenCalled();
  });

  it("行是内缩的圆角块：没有通栏的下边框，也没有左竖条", () => {
    const row = model().groups[0].rows[0];

    render(
      <OrgAgentRow row={row} indent={0} selected={false} onSelect={vi.fn()} />,
    );

    const node = screen.getByTestId(`org-row-${row.agent.id}`);
    expect(node.className).toContain("rounded-md");
    expect(node.className).not.toContain("border-b");
    expect(node.className).not.toContain("border-l-");
  });

  it("选中的一行压进选中面，而不是靠左竖条转主色", () => {
    const row = model().groups[0].rows[0];

    render(<OrgAgentRow row={row} indent={0} selected onSelect={vi.fn()} />);

    const node = screen.getByTestId(`org-row-${row.agent.id}`);
    expect(node.className).toContain("bg-sidebar-selected-bg");
    expect(node.className).not.toContain("border-l-primary");
  });

  it("索引里的行不带描述第二行：那句话在详情里", () => {
    const row = model().groups[0].rows.find((r) => r.agent.id === 2)!;

    render(
      <OrgAgentRow row={row} indent={0} selected={false} onSelect={vi.fn()} />,
    );

    expect(screen.queryByText("Head of engineering")).toBeNull();
  });

  it("系统 Agent 带一枚「系统」徽标", () => {
    const system = model().topRows.find((r) => r.agent.id === 1)!;
    const plain = model().groups[0].rows.find((r) => r.agent.id === 2)!;

    const { rerender } = render(
      <OrgAgentRow
        row={system}
        indent={0}
        selected={false}
        onSelect={vi.fn()}
      />,
    );
    expect(screen.getByText("System")).toBeInTheDocument();

    rerender(
      <OrgAgentRow
        row={plain}
        indent={0}
        selected={false}
        onSelect={vi.fn()}
      />,
    );
    expect(screen.queryByText("System")).toBeNull();
  });

  it("宿主明说没有执行目标，行尾才画成拒绝色的「无目标」", () => {
    const doc = model().groups[0].rows.find((r) => r.agent.id === 4)!;

    render(
      <OrgAgentRow row={doc} indent={0} selected={false} onSelect={vi.fn()} />,
    );

    const tail = screen.getByTestId("org-row-tail-4");
    expect(tail).toHaveTextContent("No target");
    expect(tail.className).toContain("text-destructive");
  });

  it("宿主没喂 backend 也没说「没有目标」时，行尾什么都不画", () => {
    const eva = model().groups[0].rows.find((r) => r.agent.id === 2)!;

    render(
      <OrgAgentRow row={eva} indent={0} selected={false} onSelect={vi.fn()} />,
    );

    // 「没喂」不等于「没有」——不能拿缺省当告警。
    expect(screen.queryByTestId("org-row-tail-2")).toBeNull();
  });

  it("行尾的机器名是纯文字，不是填色方块", () => {
    const bob = model().groups[0].rows.find((r) => r.agent.id === 3)!;

    render(
      <OrgAgentRow row={bob} indent={0} selected={false} onSelect={vi.fn()} />,
    );

    const tail = screen.getByTestId("org-row-tail-3");
    expect(tail).toHaveTextContent("Claude Code");
    expect(tail.className).not.toContain("bg-secondary");
  });

  it("点行把选中交回宿主，选中态与落点态都只由 props 决定", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    const row = model().groups[0].rows[0];

    render(
      <OrgAgentRow
        row={row}
        indent={2}
        selected
        onSelect={onSelect}
        droppable
        dropState="invalid"
      />,
    );

    const node = screen.getByTestId(`org-row-${row.agent.id}`);
    expect(node).toHaveAttribute("aria-current", "true");
    expect(node).toHaveAttribute("data-drop-kind", "agent");
    expect(node).toHaveAttribute("data-drop-state", "invalid");
    expect(node).toHaveAttribute("data-indent", "2");

    await user.click(screen.getByTestId(`org-row-select-${row.agent.id}`));
    expect(onSelect).toHaveBeenCalledWith({ kind: "agent", id: row.agent.id });
  });
});

describe("OrgGroupHeader（只吃 props）", () => {
  it("组头带负责人与成员数，子部门缩在父部门里", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    const groups = model().groups;

    render(
      <>
        <OrgGroupHeader
          group={groups[0]}
          selected={false}
          onSelect={onSelect}
        />
        <OrgGroupHeader
          group={groups[1]}
          selected={false}
          onSelect={onSelect}
        />
      </>,
    );

    expect(screen.getByText("Lead · Eva")).toBeInTheDocument();
    expect(screen.getByText("2 members")).toBeInTheDocument();
    // 空部门照常摆组头（决策 13）。
    expect(screen.getByTestId("org-group-2")).toHaveAttribute(
      "data-depth",
      "1",
    );

    await user.click(screen.getByTestId("org-group-select-2"));
    expect(onSelect).toHaveBeenCalledWith({ kind: "department", id: 2 });
  });
});

describe("OrgGroupHeader 的收放与动作（都归宿主）", () => {
  it("组头不是通栏灰带：无底色、圆角块", () => {
    render(
      <OrgGroupHeader
        group={model().groups[0]}
        selected={false}
        onSelect={vi.fn()}
      />,
    );

    const node = screen.getByTestId("org-group-1");
    expect(node.className).toContain("rounded-md");
    expect(node.className).not.toContain("bg-secondary");
    expect(node.className).not.toContain("border-b");
  });

  it("给了 onToggleExpanded 才长出收放三角；状态是宿主给的，包只画它并回调", async () => {
    const user = userEvent.setup();
    const onToggleExpanded = vi.fn();

    const { rerender } = render(
      <OrgGroupHeader
        group={model().groups[0]}
        selected={false}
        onSelect={vi.fn()}
      />,
    );
    expect(screen.queryByTestId("org-group-toggle-1")).toBeNull();

    rerender(
      <OrgGroupHeader
        group={model().groups[0]}
        selected={false}
        onSelect={vi.fn()}
        expanded={false}
        onToggleExpanded={onToggleExpanded}
      />,
    );

    const toggle = screen.getByTestId("org-group-toggle-1");
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    await user.click(toggle);
    expect(onToggleExpanded).toHaveBeenCalledTimes(1);
  });

  it("动作插槽由宿主填，包里不硬编码任何动作语义", () => {
    const { rerender } = render(
      <OrgGroupHeader
        group={model().groups[0]}
        selected={false}
        onSelect={vi.fn()}
      />,
    );
    expect(screen.queryByTestId("gh-action")).toBeNull();

    rerender(
      <OrgGroupHeader
        group={model().groups[0]}
        selected={false}
        onSelect={vi.fn()}
        actions={<button data-testid="gh-action">add</button>}
      />,
    );
    expect(screen.getByTestId("gh-action")).toBeInTheDocument();
  });
});

describe("OrgInsertLine（只吃 props）", () => {
  it("非法落点留在原地画成拒绝态，而不是让高亮消失", () => {
    render(<OrgInsertLine id="insert-1-0-0" dropState="invalid" />);

    const line = screen.getByTestId("insert-1-0-0");
    expect(line).toHaveAttribute("data-drop-kind", "reorder");
    expect(line).toHaveAttribute("data-drop-state", "invalid");
    expect(line.className).toContain("bg-destructive");
  });
});

/**
 * 层级线索（规格 2026-08-26「组织索引 · 层级线索」方案 A）。
 *
 * 索引里唯一的层级是「部门套部门」，而它此前只由 15px 的缩进步长表达 —— 步长小、
 * 组头与行又同字号，深度只能靠数像素。这里钉三件事：行缩在自己的部门头下一级、
 * 每个祖先层级画一条竖线、竖线与内容的左缘同源（同一个步长常量）。
 */
describe("层级线索", () => {
  it("行的左缘比自己的部门头缩一级：同一个 depth 下，行不再与组头挤在一条线上", () => {
    const group = model().groups[0];

    render(
      <>
        <OrgGroupHeader group={group} selected={false} onSelect={vi.fn()} />
        <OrgAgentRow
          row={group.rows[0]}
          indent={group.depth + 1}
          selected={false}
          onSelect={vi.fn()}
        />
      </>,
    );

    const header = screen.getByTestId("org-group-1");
    const row = screen.getByTestId(`org-row-${group.rows[0].agent.id}`);
    expect(header.style.paddingLeft).toBe(`${ORG_INDENT_BASE_HEADER}px`);
    expect(row.style.paddingLeft).toBe(
      `${ORG_INDENT_BASE_ROW + ORG_INDENT_STEP}px`,
    );
  });

  it("行按 indent 画出每个祖先层级的竖线，顶层的行一条都不画", () => {
    const row = model().groups[0].rows[0];

    const { rerender } = render(
      <OrgAgentRow row={row} indent={0} selected={false} onSelect={vi.fn()} />,
    );
    expect(
      screen
        .getByTestId(`org-row-${row.agent.id}`)
        .querySelectorAll('[data-slot="org-indent-rail"]'),
    ).toHaveLength(0);

    rerender(
      <OrgAgentRow row={row} indent={3} selected={false} onSelect={vi.fn()} />,
    );
    const rails = screen
      .getByTestId(`org-row-${row.agent.id}`)
      .querySelectorAll('[data-slot="org-indent-rail"]');
    expect(rails).toHaveLength(3);
    // 竖线落在各层级左缘的空槽里，与那一层的内容同源于一个步长常量。
    expect((rails[0] as HTMLElement).style.left).toBe(`${ORG_RAIL_OFFSET}px`);
    expect((rails[2] as HTMLElement).style.left).toBe(
      `${ORG_RAIL_OFFSET + 2 * ORG_INDENT_STEP}px`,
    );
  });

  it("组头按 depth 画竖线：子部门有线接回父部门，顶层部门头没有", () => {
    const [engineering, platform] = model().groups;

    render(
      <>
        <OrgGroupHeader
          group={engineering}
          selected={false}
          onSelect={vi.fn()}
        />
        <OrgGroupHeader group={platform} selected={false} onSelect={vi.fn()} />
      </>,
    );

    expect(
      screen
        .getByTestId("org-group-1")
        .querySelectorAll('[data-slot="org-indent-rail"]'),
    ).toHaveLength(0);
    expect(
      screen
        .getByTestId("org-group-2")
        .querySelectorAll('[data-slot="org-indent-rail"]'),
    ).toHaveLength(1);
  });

  it("顶层部门头之间留一口气，子部门头不留（气口分的是部门块，不是每一层）", () => {
    const [engineering, platform] = model().groups;

    render(
      <>
        <OrgGroupHeader
          group={engineering}
          selected={false}
          onSelect={vi.fn()}
        />
        <OrgGroupHeader group={platform} selected={false} onSelect={vi.fn()} />
      </>,
    );

    expect(screen.getByTestId("org-group-1").className).toContain("mt-");
    expect(screen.getByTestId("org-group-2").className).not.toContain("mt-");
  });
});
