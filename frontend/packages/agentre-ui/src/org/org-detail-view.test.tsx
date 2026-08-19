import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { OrgExecTargetRow } from "./org-exec-target-row";
import { OrgPlacementField } from "./org-placement-field";
import { OrgToolList } from "./org-tool-list";
import type { OrgAgentModel, OrgDepartmentModel } from "./types";

/**
 * 与索引那组同理：这里扮演 agentre-server —— 详情三栏里的呈现件必须只靠普通对象
 * 与回调就画得出来，取数（能力矩阵、技能目录）一律以 slot / 布尔位的形式由宿主给。
 */

const departments: OrgDepartmentModel[] = [
  { id: 1, name: "Engineering" },
  { id: 2, name: "Platform", parentId: 1 },
];

// CEO(1) ─ Eva(2, Engineering) ─ Bob(3, 汇报给 Eva) ─ Cid(4, 汇报给 Bob)
const agents: OrgAgentModel[] = [
  { id: 1, name: "CEO", systemBadge: "DEFAULT" },
  { id: 2, name: "Eva", departmentId: 1 },
  { id: 3, name: "Bob", parentAgentId: 2 },
  { id: 4, name: "Cid", parentAgentId: 3 },
];

describe("OrgPlacementField（只吃 props）", () => {
  async function openField(agentId: number) {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    const onPick = vi.fn();
    render(
      <OrgPlacementField
        agent={agents.find((a) => a.id === agentId)!}
        agents={agents}
        departments={departments}
        placement={{ kind: "department", id: 1 }}
        reportTarget={null}
        onPick={onPick}
      />,
    );
    await user.click(screen.getByRole("combobox"));
    return { user, onPick };
  }

  it("一个下拉、两组互斥选项，选任一组只回一个归属", async () => {
    const { user, onPick } = await openField(3);

    expect(screen.getByText("Departments")).toBeInTheDocument();
    expect(screen.getByText("Parent agent")).toBeInTheDocument();

    await user.click(
      screen.getByRole("option", { name: /Engineering \/ Platform/ }),
    );
    expect(onPick).toHaveBeenCalledWith({ kind: "department", id: 2 });
  });

  it("子部门带出祖先路径前缀，同名部门不致混淆", async () => {
    await openField(3);
    expect(
      screen.getByRole("option", { name: /Engineering \/ Platform/ }),
    ).toBeInTheDocument();
  });

  it("自己与自己的后代就地置灰并写明原因，判据与拖拽的非法落点同一条", async () => {
    await openField(2);

    const self = screen.getByRole("option", { name: /Eva/ });
    expect(self).toHaveAttribute("aria-disabled", "true");
    expect(self.textContent).toContain("An agent can't be its own manager");

    // Cid 是 Eva 的孙下属：挂过去会成环。
    const descendant = screen.getByRole("option", { name: /Cid/ });
    expect(descendant).toHaveAttribute("aria-disabled", "true");
    expect(descendant.textContent).toContain(
      "An agent can't report to its own subordinate",
    );
  });

  it("系统 Agent 仍是回到顶层的那条路：它不置灰", async () => {
    await openField(3);
    expect(screen.getByRole("option", { name: /CEO/ })).not.toHaveAttribute(
      "aria-disabled",
    );
  });

  it("归属是部门时附一条只读的推导行；是上级 Agent 时不出现", () => {
    const { rerender } = render(
      <OrgPlacementField
        agent={agents[1]}
        agents={agents}
        departments={departments}
        placement={{ kind: "department", id: 1 }}
        reportTarget={agents[0]}
        onPick={vi.fn()}
      />,
    );
    expect(screen.getByTestId("org-agent-derived-manager")).toHaveTextContent(
      "changes with the department lead",
    );

    rerender(
      <OrgPlacementField
        agent={agents[2]}
        agents={agents}
        departments={departments}
        placement={{ kind: "agent", id: 2 }}
        reportTarget={agents[1]}
        onPick={vi.fn()}
      />,
    );
    expect(screen.queryByTestId("org-agent-derived-manager")).toBeNull();
  });
});

describe("OrgToolList（只吃 props）", () => {
  const toolKeys = ["subagent", "org", "hook"];

  it("已授权的排在前面并带 ✓，需审批只标在已授权的 org 与 hook 上", () => {
    render(
      <OrgToolList
        toolKeys={toolKeys}
        agentTools={[
          { key: "org", enabled: true },
          { key: "hook", enabled: true },
          { key: "subagent", enabled: false },
        ]}
        onToggleGrant={vi.fn()}
      />,
    );

    const rows = screen.getAllByRole("listitem");
    expect(rows.map((row) => row.getAttribute("data-granted"))).toEqual([
      "true",
      "true",
      "false",
    ]);
    expect(screen.getAllByText("Approval")).toHaveLength(2);
    expect(screen.getByText("2 enabled")).toBeInTheDocument();
  });

  it("未授权的工具不标需审批（还没有写操作可言），清单以外没有第二处入口", async () => {
    const user = userEvent.setup();
    const onToggleGrant = vi.fn();
    render(
      <OrgToolList
        toolKeys={["org"]}
        agentTools={[{ key: "org", enabled: false }]}
        onToggleGrant={onToggleGrant}
      />,
    );

    expect(screen.queryByText("Approval")).toBeNull();
    await user.click(
      screen.getByRole("button", { name: "Grant Org Structure" }),
    );
    expect(onToggleGrant).toHaveBeenCalledWith("org");
  });
});

describe("OrgExecTargetRow（只吃 props）", () => {
  const backend = { id: 7, type: "claude-code", name: "cc" };

  it("多档时给序号与上移下移；当前生效的一档与其余区分", () => {
    render(
      <OrgExecTargetRow
        index={0}
        total={2}
        backend={backend}
        status={{ available: true, reason: "" }}
        isFirstAvailable
        onMoveDown={vi.fn()}
        skillsSupported={false}
      />,
    );

    expect(screen.getByText("1")).toBeInTheDocument();
    expect(screen.getByText("Currently active")).toBeInTheDocument();
    expect(screen.getByTestId("exec-target-row-0").className).toContain(
      "border-l-status-running",
    );
  });

  it("不可用的一档留在列表里并给出原因，不隐藏", () => {
    render(
      <OrgExecTargetRow
        index={1}
        total={2}
        backend={{ ...backend, deviceId: "dev", deviceName: "MacBook" }}
        status={{ available: false, reason: "exec-target-offline" }}
        isFirstAvailable={false}
        skillsSupported={false}
      />,
    );

    expect(screen.getByText("MacBook")).toBeInTheDocument();
    expect(screen.getByText("Offline")).toBeInTheDocument();
  });

  it("后端不支持技能就不给展开入口，只说明为什么", () => {
    render(
      <OrgExecTargetRow
        index={0}
        total={2}
        backend={backend}
        status={undefined}
        isFirstAvailable={false}
        skillsSupported={false}
        skillsBlock={<span>skills body</span>}
      />,
    );

    expect(
      screen.getByText("This backend doesn't support skills"),
    ).toBeInTheDocument();
    expect(screen.queryByText("skills body")).toBeNull();
  });

  it("单档时技能已经展开，且宿主给的技能块原样挂在折叠里", async () => {
    const user = userEvent.setup();
    render(
      <OrgExecTargetRow
        index={0}
        total={1}
        backend={backend}
        status={undefined}
        isFirstAvailable={false}
        skillsSupported
        skillsBlock={<span>skills body</span>}
      />,
    );

    expect(screen.getByText("skills body")).toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "Skills for Local machine" }),
    );
    expect(screen.queryByText("skills body")).toBeNull();
  });

  it("拖拽柄上的 ↑ / ↓ 直接移动这一行，不必先提起", async () => {
    const user = userEvent.setup();
    const onMoveDown = vi.fn();
    const listenerKeyDown = vi.fn();
    render(
      <OrgExecTargetRow
        index={0}
        total={2}
        backend={backend}
        status={undefined}
        isFirstAvailable={false}
        skillsSupported={false}
        onMoveDown={onMoveDown}
        drag={{ handle: { listeners: { onKeyDown: listenerKeyDown } } }}
      />,
    );

    screen.getByRole("button", { name: /Reorder target/ }).focus();
    await user.keyboard("{ArrowDown}");
    expect(onMoveDown).toHaveBeenCalledTimes(1);
    // 方向键被这一行消化掉，不再落到宿主的 KeyboardSensor 上。
    expect(listenerKeyDown).not.toHaveBeenCalled();
  });
});
