import { render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { OrgDetailAgent } from "../org-detail-agent";
import { OrgDetailDepartment } from "../org-detail-department";
import type { OrgAgent, OrgDepartment } from "../types";

// 详情落在主区，分三栏：身份 / 行为 / 执行（部门是 身份 / 结构 / 成员）。
// 三栏各自是一个具名 region，读屏能直接跳；字段集合与今天一致，变的是它们落在哪一栏。

const dept: OrgDepartment = {
  id: 1,
  name: "工程部",
  description: "",
  icon: "hammer",
  accentColor: "agent-2",
  parentId: 0,
  leadAgentId: 0,
  leadAgentName: "",
  sortOrder: 1,
  directAgentCount: 1,
  subdepartmentCount: 0,
  memberCount: 1,
  createtime: 0,
  updatetime: 0,
} as OrgDepartment;

const agent = (overrides: Partial<OrgAgent> = {}): OrgAgent =>
  ({
    id: 7,
    name: "Eva",
    description: "工程总监",
    avatarColor: "agent-2",
    avatarIcon: "",
    avatarDataUrl: "",
    systemBadge: "",
    departmentId: 1,
    departmentName: "工程部",
    parentAgentId: 0,
    parentAgentName: "",
    agentBackendId: 5,
    sortOrder: 1,
    prompt: ["你是 Eva。"],
    skills: [],
    execTargets: [{ id: 1, agentBackendId: 5, skills: [] }],
    tools: [],
    createtime: 0,
    updatetime: 0,
    ...overrides,
  }) as OrgAgent;

beforeEach(() => {
  window.go = {
    app: {
      App: {
        GetBackendCapabilities: vi
          .fn()
          .mockResolvedValue({ capabilities: ["mcp_tools"] }),
        ListAgentSkillPacks: vi.fn().mockResolvedValue({ packs: [] }),
        ListAgentExecTargetAvailability: vi
          .fn()
          .mockResolvedValue([
            { agentBackendId: 5, available: true, reason: "", hint: "" },
          ]),
      },
    },
  };
});

afterEach(() => {
  delete window.go;
});

function renderAgent(overrides: Partial<OrgAgent> = {}) {
  render(
    <MemoryRouter initialEntries={["/org"]}>
      <OrgDetailAgent
        agent={agent(overrides)}
        departments={[dept]}
        agents={[agent(overrides)]}
        backends={[{ id: 5, type: "claudecode", name: "Claude Code" } as never]}
        isLeadOf={null}
        availableTools={["org"]}
        onUpdate={vi.fn().mockResolvedValue(undefined)}
        onMove={vi.fn().mockResolvedValue(undefined)}
        onDelete={vi.fn().mockResolvedValue(undefined)}
        onUploadAvatar={vi.fn().mockResolvedValue(undefined)}
        onDeleteAvatar={vi.fn().mockResolvedValue(undefined)}
        onClose={vi.fn()}
      />
    </MemoryRouter>,
  );
}

describe("agent detail columns", () => {
  it("Given an agent is selected, When the detail renders, Then it has the three columns identity / behavior / execution", () => {
    renderAgent();
    expect(
      screen.getByRole("region", { name: "Identity" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("region", { name: "Behavior" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("region", { name: "Execution" }),
    ).toBeInTheDocument();
  });

  it("Given the three columns, When the fields are placed, Then the field set is exactly today's, regrouped", async () => {
    renderAgent();

    const identity = screen.getByRole("region", { name: "Identity" });
    expect(within(identity).getByLabelText("Name")).toBeInTheDocument();
    expect(within(identity).getByLabelText("Description")).toBeInTheDocument();
    expect(
      within(identity).getByRole("radiogroup", { name: "Avatar Color" }),
    ).toBeInTheDocument();
    expect(
      within(identity).getAllByLabelText("Change avatar").length,
    ).toBeGreaterThan(0);
    // 归属：身份栏里的那一个下拉（部门 / 上级 Agent 二选一）。
    expect(
      within(identity).getByRole("combobox", { name: "Placement" }),
    ).toBeInTheDocument();

    const behavior = screen.getByRole("region", { name: "Behavior" });
    expect(
      within(behavior).getByLabelText("System Prompt"),
    ).toBeInTheDocument();
    expect(
      await within(behavior).findByRole("list", { name: "Tools" }),
    ).toBeInTheDocument();

    const execution = screen.getByRole("region", { name: "Execution" });
    expect(
      within(execution).getByRole("heading", { name: "Execution Targets" }),
    ).toBeInTheDocument();
  });
});

describe("department detail columns", () => {
  it("Given a department is selected, When the detail renders, Then it has three columns and the same fields as today", () => {
    render(
      <OrgDetailDepartment
        department={dept}
        allDepartments={[dept]}
        allAgents={[]}
        leadCandidates={[]}
        onUpdate={vi.fn().mockResolvedValue(undefined)}
        onMove={vi.fn().mockResolvedValue(undefined)}
        onDelete={vi.fn().mockResolvedValue(undefined)}
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    );

    const identity = screen.getByRole("region", { name: "Identity" });
    expect(within(identity).getByLabelText("Name")).toBeInTheDocument();
    expect(within(identity).getByLabelText("Description")).toBeInTheDocument();
    expect(
      within(identity).getByRole("radiogroup", { name: "Theme Color" }),
    ).toBeInTheDocument();

    const structure = screen.getByRole("region", { name: "Structure" });
    expect(
      within(structure).getByRole("combobox", { name: "Parent" }),
    ).toBeInTheDocument();
    expect(
      within(structure).getByRole("combobox", { name: "Leader" }),
    ).toBeInTheDocument();

    const members = screen.getByRole("region", { name: "Members" });
    expect(
      within(members).getByRole("heading", { name: "Members" }),
    ).toBeInTheDocument();
  });
});
