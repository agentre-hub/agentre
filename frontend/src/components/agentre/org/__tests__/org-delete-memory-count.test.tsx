import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { OrgDetailAgent } from "../org-detail-agent";
import { OrgDetailDepartment } from "../org-detail-department";
import type { OrgAgent, OrgDepartment } from "../types";

// 三处删除确认里的第一处和第二处：agent 与部门。确认框要写明会一并删掉多少条记忆，
// 数取自 MemoryCountsByOwner；部门按所选策略汇总（只挪走子节点 vs 整棵子树）。

const countsMock = vi.fn();

function mockCounts(rows: { kind: string; id: number; count: number }[]) {
  countsMock.mockResolvedValue(
    rows.map((r) => ({ owner: { kind: r.kind, id: r.id }, count: r.count })),
  );
}

beforeEach(() => {
  countsMock.mockReset();
  window.go = {
    app: {
      App: {
        MemoryCountsByOwner: countsMock,
        GetBackendCapabilities: vi.fn().mockResolvedValue({ capabilities: [] }),
      },
    },
  } as unknown as typeof window.go;
});

afterEach(() => {
  delete (window as { go?: unknown }).go;
});

const dept = (over: Partial<OrgDepartment>): OrgDepartment =>
  ({
    id: 3,
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
    ...over,
  }) as OrgDepartment;

const agent = (over: Partial<OrgAgent>): OrgAgent =>
  ({
    id: 7,
    name: "Eva",
    description: "",
    avatarColor: "agent-2",
    avatarIcon: "",
    avatarDataUrl: "",
    systemBadge: "",
    departmentId: 3,
    departmentName: "工程部",
    parentAgentId: 0,
    parentAgentName: "",
    agentBackendId: 5,
    sortOrder: 1,
    prompt: [],
    skills: [],
    execTargets: [],
    tools: [],
    createtime: 0,
    updatetime: 0,
    ...over,
  }) as unknown as OrgAgent;

function renderAgent() {
  render(
    <OrgDetailAgent
      agent={agent({})}
      departments={[dept({})]}
      agents={[]}
      backends={[]}
      isLeadOf={null}
      onUpdate={vi.fn()}
      onMove={vi.fn()}
      onDelete={vi.fn()}
      onUploadAvatar={vi.fn()}
      onDeleteAvatar={vi.fn()}
      onClose={vi.fn()}
    />,
  );
}

function renderDepartment() {
  const root = dept({ id: 3 });
  const child = dept({ id: 4, parentId: 3, name: "前端组" });
  const other = dept({ id: 9, name: "市场部" });
  render(
    <OrgDetailDepartment
      department={root}
      allDepartments={[root, child, other]}
      allAgents={[
        agent({ id: 7, departmentId: 3 }),
        agent({ id: 8, departmentId: 4 }),
        agent({ id: 10, departmentId: 9 }),
      ]}
      leadCandidates={[]}
      onUpdate={vi.fn()}
      onMove={vi.fn()}
      onDelete={vi.fn()}
      onSelect={vi.fn()}
      onClose={vi.fn()}
    />,
  );
}

describe("delete agent confirmation", () => {
  it("states how many memories the agent owns will be deleted", async () => {
    mockCounts([
      { kind: "agent", id: 7, count: 5 },
      { kind: "department", id: 7, count: 11 },
    ]);
    renderAgent();
    await userEvent.click(screen.getByRole("button", { name: "Delete Agent" }));
    expect(
      await screen.findByText("Its 5 memories are deleted too."),
    ).toBeInTheDocument();
  });

  it("says nothing about memories when the count cannot be loaded", async () => {
    countsMock.mockRejectedValue(new Error("boom"));
    renderAgent();
    await userEvent.click(screen.getByRole("button", { name: "Delete Agent" }));
    await waitFor(() => expect(countsMock).toHaveBeenCalled());
    expect(screen.queryByText(/memor/i)).toBeNull();
  });
});

describe("delete department confirmation", () => {
  const rows = [
    { kind: "department", id: 3, count: 8 },
    { kind: "department", id: 4, count: 2 },
    { kind: "department", id: 9, count: 100 },
    { kind: "agent", id: 7, count: 5 },
    { kind: "agent", id: 8, count: 1 },
    { kind: "agent", id: 10, count: 100 },
  ];

  it("with the default reparent strategy counts only the department's own memories", async () => {
    mockCounts(rows);
    renderDepartment();
    await userEvent.click(
      screen.getAllByRole("button", { name: /Delete department/ })[0],
    );
    expect(
      await screen.findByText("Its 8 memories are deleted too."),
    ).toBeInTheDocument();
  });

  it("with the cascade strategy adds every sub-department and agent removed with it", async () => {
    mockCounts(rows);
    renderDepartment();
    await userEvent.click(
      screen.getAllByRole("button", { name: /Delete department/ })[0],
    );
    await userEvent.click(
      screen.getByRole("radio", { name: "Recursively delete the subtree" }),
    );
    // 8 + 2（两个部门）+ 5 + 1（两个 agent）；别的部门的 100 不算。
    expect(
      await screen.findByText("Its 16 memories are deleted too."),
    ).toBeInTheDocument();
  });
});
