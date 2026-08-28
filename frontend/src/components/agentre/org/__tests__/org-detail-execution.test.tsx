import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { OrgDetailAgent } from "../org-detail-agent";
import type { OrgAgent, OrgDepartment } from "../types";

// 「今天说了两遍的状态」：noBackendBound 的 Alert 与执行目标列表的空态是同一个条件
// （execTargets.length === 0），今天两条同时出现。本轮只留一条，且那一条同时说明
// 「不能对话」与「至少要有一项」。

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

const agent = {
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
} as unknown as OrgAgent;

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

describe("OrgDetailAgent with no execution target", () => {
  it("Given no execution target, When the detail renders, Then exactly one message says it can't chat and at least one is required", () => {
    render(
      <MemoryRouter initialEntries={["/org"]}>
        <OrgDetailAgent
          agent={agent}
          departments={[dept]}
          agents={[]}
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

    expect(screen.getAllByText("This agent can't chat yet")).toHaveLength(1);
    expect(
      screen.getByText(/At least one is required before this agent/),
    ).toBeInTheDocument();
    // 面板自己那条 Alert 不再存在：同一个条件只说一次。
    expect(screen.queryByTestId("org-agent-no-backend")).toBeNull();
  });
});
