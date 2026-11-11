import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { OrgDetailAgent } from "../org-detail-agent";
import { OrgDetailDepartment } from "../org-detail-department";
import { OrgMemoryRow } from "../org-memory-row";
import type { OrgAgent, OrgDepartment } from "../types";

// 「记忆 N 条」是 agent 详情、部门详情、项目设置三处共有的一行：数字取自
// MemoryCountsByOwner，点一下带着归属跳到记忆页。读数失败不能拖垮宿主页面。

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

function Where() {
  const loc = useLocation();
  return <div data-testid="where">{loc.pathname + loc.search}</div>;
}

function inRouter(ui: React.ReactNode) {
  return render(
    <MemoryRouter initialEntries={["/org"]}>
      {ui}
      <Where />
    </MemoryRouter>,
  );
}

describe("OrgMemoryRow", () => {
  it("shows the owner's memory count", async () => {
    mockCounts([
      { kind: "agent", id: 7, count: 6 },
      { kind: "agent", id: 8, count: 99 },
    ]);
    inRouter(<OrgMemoryRow owner={{ kind: "agent", id: 7 }} />);
    expect(await screen.findByRole("button", { name: /6/ })).toHaveTextContent(
      "6 memories",
    );
  });

  it("navigates to the memory page with that owner selected", async () => {
    mockCounts([{ kind: "department", id: 3, count: 2 }]);
    inRouter(<OrgMemoryRow owner={{ kind: "department", id: 3 }} />);
    await userEvent.click(await screen.findByRole("button"));
    expect(screen.getByTestId("where")).toHaveTextContent(
      "/memory?owner=department:3",
    );
  });

  it("shows zero when the owner has no entry in the counts", async () => {
    mockCounts([{ kind: "agent", id: 1, count: 4 }]);
    inRouter(<OrgMemoryRow owner={{ kind: "project", id: 5 }} />);
    expect(await screen.findByRole("button")).toHaveTextContent("0 memories");
  });

  it("uses the singular form for exactly one entry", async () => {
    mockCounts([{ kind: "agent", id: 7, count: 1 }]);
    inRouter(<OrgMemoryRow owner={{ kind: "agent", id: 7 }} />);
    expect(await screen.findByRole("button")).toHaveTextContent("1 memory");
  });

  it("renders nothing when the count cannot be loaded", async () => {
    countsMock.mockRejectedValue(new Error("boom"));
    inRouter(<OrgMemoryRow owner={{ kind: "agent", id: 7 }} />);
    await waitFor(() => expect(countsMock).toHaveBeenCalled());
    expect(screen.queryByRole("button")).toBeNull();
  });
});

const dept = {
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
} as OrgDepartment;

const agent = {
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
} as unknown as OrgAgent;

describe("detail pages carry the memory row", () => {
  it("agent detail shows the agent's own count and routes to agent:<id>", async () => {
    mockCounts([
      { kind: "agent", id: 7, count: 5 },
      { kind: "department", id: 7, count: 11 },
    ]);
    inRouter(
      <OrgDetailAgent
        agent={agent}
        departments={[dept]}
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
    const row = await screen.findByRole("button", { name: /5 memories/ });
    await userEvent.click(row);
    expect(screen.getByTestId("where")).toHaveTextContent(
      "/memory?owner=agent:7",
    );
  });

  it("department detail shows the department's count and routes to department:<id>", async () => {
    mockCounts([
      { kind: "department", id: 3, count: 8 },
      { kind: "agent", id: 3, count: 1 },
    ]);
    inRouter(
      <OrgDetailDepartment
        department={dept}
        allDepartments={[dept]}
        allAgents={[]}
        leadCandidates={[]}
        onUpdate={vi.fn()}
        onMove={vi.fn()}
        onDelete={vi.fn()}
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    );
    const row = await screen.findByRole("button", { name: /8 memories/ });
    await userEvent.click(row);
    expect(screen.getByTestId("where")).toHaveTextContent(
      "/memory?owner=department:3",
    );
  });

  it("a failed count load leaves the detail page intact", async () => {
    countsMock.mockRejectedValue(new Error("boom"));
    inRouter(
      <OrgDetailDepartment
        department={dept}
        allDepartments={[dept]}
        allAgents={[]}
        leadCandidates={[]}
        onUpdate={vi.fn()}
        onMove={vi.fn()}
        onDelete={vi.fn()}
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    );
    await waitFor(() => expect(countsMock).toHaveBeenCalled());
    expect(screen.getByDisplayValue("工程部")).toBeInTheDocument();
    expect(screen.queryByText(/memor/)).toBeNull();
  });
});
