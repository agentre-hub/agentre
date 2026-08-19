import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { OrgDetailAgent } from "../org-detail-agent";
import type { OrgAgent, OrgDepartment } from "../types";

// 归属是**一个**下拉、两组互斥选项（部门 / 上级 Agent）：选任一组即清空另一边。
// 自己与自己的后代就地置灰并写明原因，判据与拖拽的非法落点是同一条（isValidOrgDrop）。
// 移动端改归属全靠它，所以「回到顶层」（挂到系统 Agent 下）也必须在这里做得到 ——
// 索引里的系统 Agent 行不接受落点，拖拽没有这条路。

const department = (overrides: Partial<OrgDepartment> = {}): OrgDepartment =>
  ({
    id: 1,
    name: "工程部",
    description: "",
    icon: "hammer",
    accentColor: "agent-2",
    parentId: 0,
    leadAgentId: 0,
    leadAgentName: "",
    sortOrder: 1,
    directAgentCount: 0,
    subdepartmentCount: 0,
    memberCount: 0,
    createtime: 0,
    updatetime: 0,
    ...overrides,
  }) as OrgDepartment;

const agent = (overrides: Partial<OrgAgent> = {}): OrgAgent =>
  ({
    id: 7,
    name: "Eva",
    description: "",
    avatarColor: "agent-2",
    avatarIcon: "",
    avatarDataUrl: "",
    systemBadge: "",
    departmentId: 1,
    departmentName: "工程部",
    parentAgentId: 0,
    parentAgentName: "",
    agentBackendId: 0,
    sortOrder: 1,
    prompt: [],
    skills: [],
    execTargets: [],
    tools: [],
    createtime: 0,
    updatetime: 0,
    ...overrides,
  }) as OrgAgent;

const ENGINEERING = department({ id: 1, name: "工程部", parentId: 0 });
const PLATFORM = department({ id: 2, name: "平台组", parentId: 1 });
const CEO = agent({
  id: 1,
  name: "CEO 助手",
  systemBadge: "DEFAULT",
  departmentId: 0,
});
const EVA = agent({ id: 7, name: "Eva", departmentId: 1 });
const BOB = agent({ id: 8, name: "Bob", departmentId: 0, parentAgentId: 7 });
const DAN = agent({ id: 9, name: "Dan", departmentId: 1 });

beforeEach(() => {
  window.go = {
    app: {
      App: {
        GetBackendCapabilities: vi.fn().mockResolvedValue({ capabilities: [] }),
        ListAgentSkillPacks: vi.fn().mockResolvedValue({ packs: [] }),
        ListAgentExecTargetAvailability: vi.fn().mockResolvedValue([]),
      },
    },
  };
});

afterEach(() => {
  delete window.go;
});

function renderPanel(subject: OrgAgent = EVA) {
  const onMove = vi.fn().mockResolvedValue(undefined);
  render(
    <MemoryRouter initialEntries={["/org"]}>
      <OrgDetailAgent
        agent={subject}
        departments={[ENGINEERING, PLATFORM]}
        agents={[CEO, EVA, BOB, DAN]}
        backends={[]}
        isLeadOf={null}
        availableTools={[]}
        onUpdate={vi.fn().mockResolvedValue(undefined)}
        onMove={onMove}
        onDelete={vi.fn().mockResolvedValue(undefined)}
        onUploadAvatar={vi.fn().mockResolvedValue(undefined)}
        onDeleteAvatar={vi.fn().mockResolvedValue(undefined)}
        onClose={vi.fn()}
      />
    </MemoryRouter>,
  );
  return { onMove };
}

async function openPlacement() {
  const user = userEvent.setup();
  await user.click(screen.getByRole("combobox", { name: "Placement" }));
  return user;
}

describe("OrgDetailAgent placement dropdown", () => {
  it("Given the placement dropdown, When it opens, Then it holds two mutually exclusive option groups", async () => {
    renderPanel();
    await openPlacement();

    const departments = screen.getByRole("group", { name: "Departments" });
    expect(
      within(departments).getByRole("option", { name: "工程部" }),
    ).toBeInTheDocument();

    const parents = screen.getByRole("group", { name: "Parent agent" });
    expect(
      within(parents).getByRole("option", { name: /Dan/ }),
    ).toBeInTheDocument();
    // 一个下拉、一份选项集合：部门选项不会同时出现在上级 Agent 组里。
    expect(
      within(parents).queryByRole("option", { name: "工程部" }),
    ).toBeNull();
  });

  it("Given a department is picked, When it is applied, Then the parent agent is cleared", async () => {
    const { onMove } = renderPanel();
    const user = await openPlacement();
    await user.click(screen.getByRole("option", { name: /平台组/ }));
    expect(onMove).toHaveBeenCalledWith({
      id: 7,
      newDepartmentId: 2,
      newParentAgentId: 0,
      newSortOrder: 0,
    });
  });

  it("Given a parent agent is picked, When it is applied, Then the department is cleared", async () => {
    const { onMove } = renderPanel();
    const user = await openPlacement();
    await user.click(screen.getByRole("option", { name: /Dan/ }));
    expect(onMove).toHaveBeenCalledWith({
      id: 7,
      newDepartmentId: 0,
      newParentAgentId: 9,
      newSortOrder: 0,
    });
  });

  it("Given itself and its descendants, When the options render, Then they are disabled and say why", async () => {
    const { onMove } = renderPanel();
    const user = await openPlacement();

    const self = screen.getByRole("option", { name: /Eva/ });
    expect(self).toHaveAttribute("aria-disabled", "true");
    expect(self).toHaveTextContent("An agent can't be its own manager");

    const descendant = screen.getByRole("option", { name: /Bob/ });
    expect(descendant).toHaveAttribute("aria-disabled", "true");
    expect(descendant).toHaveTextContent(
      "An agent can't report to its own subordinate",
    );

    await user.click(descendant);
    expect(onMove).not.toHaveBeenCalled();
  });

  it("Given a sub-department, When its option renders, Then it carries the path prefix of its ancestors", async () => {
    renderPanel();
    await openPlacement();
    expect(
      screen.getByRole("option", { name: "工程部 / 平台组" }),
    ).toBeInTheDocument();
  });

  it("Given the index refuses drops on the system agent, When the placement dropdown opens, Then it is still the route back to the top level", async () => {
    const { onMove } = renderPanel();
    const user = await openPlacement();
    const ceo = screen.getByRole("option", { name: /CEO 助手/ });
    expect(ceo).not.toHaveAttribute("aria-disabled", "true");
    await user.click(ceo);
    expect(onMove).toHaveBeenCalledWith({
      id: 7,
      newDepartmentId: 0,
      newParentAgentId: 1,
      newSortOrder: 0,
    });
  });

  it("Given the placement is a department, When the detail renders, Then a read-only line derives the manager and says it follows the lead", () => {
    render(
      <MemoryRouter initialEntries={["/org"]}>
        <OrgDetailAgent
          agent={agent({ id: 9, name: "Dan", departmentId: 1 })}
          departments={[department({ id: 1, name: "工程部", leadAgentId: 7 })]}
          agents={[CEO, EVA, agent({ id: 9, name: "Dan", departmentId: 1 })]}
          backends={[]}
          isLeadOf={null}
          availableTools={[]}
          onUpdate={vi.fn().mockResolvedValue(undefined)}
          onMove={vi.fn().mockResolvedValue(undefined)}
          onDelete={vi.fn().mockResolvedValue(undefined)}
          onUploadAvatar={vi.fn().mockResolvedValue(undefined)}
          onDeleteAvatar={vi.fn().mockResolvedValue(undefined)}
          onClose={vi.fn()}
        />
      </MemoryRouter>,
    );
    const derived = screen.getByTestId("org-agent-derived-manager");
    expect(derived).toHaveTextContent("Eva");
    expect(derived).toHaveTextContent("changes with the department lead");
  });

  it("Given the placement is a parent agent, When the detail renders, Then no derived manager line is shown", () => {
    render(
      <MemoryRouter initialEntries={["/org"]}>
        <OrgDetailAgent
          agent={BOB}
          departments={[ENGINEERING, PLATFORM]}
          agents={[CEO, EVA, BOB, DAN]}
          backends={[]}
          isLeadOf={null}
          availableTools={[]}
          onUpdate={vi.fn().mockResolvedValue(undefined)}
          onMove={vi.fn().mockResolvedValue(undefined)}
          onDelete={vi.fn().mockResolvedValue(undefined)}
          onUploadAvatar={vi.fn().mockResolvedValue(undefined)}
          onDeleteAvatar={vi.fn().mockResolvedValue(undefined)}
          onClose={vi.fn()}
        />
      </MemoryRouter>,
    );
    expect(screen.queryByTestId("org-agent-derived-manager")).toBeNull();
  });
});
