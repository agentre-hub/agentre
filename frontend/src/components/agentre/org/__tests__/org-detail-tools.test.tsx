import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { OrgDetailAgent } from "../org-detail-agent";
import type { OrgAgent, OrgDepartment } from "../types";

// 工具是**一份**清单：已授权的排在前面并标出，一行只有图标、名字、已授权时的
// 「需审批」、一句话能力和动作按钮。清单以外没有第二处入口（没有添加弹窗）。
// 需审批的已知集合是 {org, hook}，两项都要标，且只在已授权时出现。

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
    description: "",
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
    prompt: [],
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

function renderPanel(
  tools: Array<{ key: string; enabled: boolean }>,
  availableTools: string[],
) {
  const onUpdate = vi.fn().mockResolvedValue(undefined);
  render(
    <MemoryRouter initialEntries={["/org"]}>
      <OrgDetailAgent
        agent={agent({ tools } as Partial<OrgAgent>)}
        departments={[dept]}
        agents={[]}
        backends={[
          {
            id: 5,
            type: "claudecode",
            name: "Claude Code",
          } as never,
        ]}
        isLeadOf={null}
        availableTools={availableTools}
        onUpdate={onUpdate}
        onMove={vi.fn().mockResolvedValue(undefined)}
        onDelete={vi.fn().mockResolvedValue(undefined)}
        onUploadAvatar={vi.fn().mockResolvedValue(undefined)}
        onDeleteAvatar={vi.fn().mockResolvedValue(undefined)}
        onClose={vi.fn()}
      />
    </MemoryRouter>,
  );
  return { onUpdate };
}

async function toolRows() {
  const list = await screen.findByRole("list", { name: "Tools" });
  return within(list).getAllByRole("listitem");
}

describe("OrgDetailAgent tool list", () => {
  it("Given granted and ungranted tools, When the list renders, Then granted ones come first and carry a granted mark", async () => {
    renderPanel(
      [
        { key: "org", enabled: false },
        { key: "subagent", enabled: false },
        { key: "hook", enabled: true },
      ],
      ["org", "subagent", "hook"],
    );

    const rows = await toolRows();
    expect(rows).toHaveLength(3);
    expect(rows[0]).toHaveTextContent("Script Hooks");
    expect(rows[0]).toHaveAttribute("data-granted", "true");
    expect(rows[1]).toHaveAttribute("data-granted", "false");
    expect(rows[2]).toHaveAttribute("data-granted", "false");
    // 一行里有名字、一句话能力和动作按钮。
    expect(rows[0]).toHaveTextContent(/schedule script Hooks/);
    expect(
      within(rows[0]).getByRole("button", { name: "Revoke Script Hooks" }),
    ).toBeInTheDocument();
    expect(
      within(rows[1]).getByRole("button", { name: /^Grant / }),
    ).toBeInTheDocument();
  });

  it("Given the approval-gated tools, When they are granted, Then both 组织架构 and 脚本 Hook carry the approval mark", async () => {
    renderPanel(
      [
        { key: "org", enabled: true },
        { key: "hook", enabled: true },
        { key: "subagent", enabled: true },
      ],
      ["org", "subagent", "hook"],
    );

    const rows = await toolRows();
    const byName = (name: string) =>
      rows.find((row) => row.textContent?.includes(name));
    expect(
      within(byName("Org Structure")!).getByText("Approval"),
    ).toBeInTheDocument();
    expect(
      within(byName("Script Hooks")!).getByText("Approval"),
    ).toBeInTheDocument();
    expect(
      within(byName("Call Sub-agent")!).queryByText("Approval"),
    ).toBeNull();
  });

  it("Given an approval-gated tool that is not granted, When the list renders, Then it carries no approval mark", async () => {
    renderPanel([{ key: "org", enabled: false }], ["org"]);
    const rows = await toolRows();
    expect(within(rows[0]).queryByText("Approval")).toBeNull();
  });

  it("Given the tool list, When a tool is granted from its row, Then it is saved and there is no second entry point", async () => {
    const user = userEvent.setup();
    const { onUpdate } = renderPanel([{ key: "org", enabled: false }], ["org"]);
    const rows = await toolRows();
    await user.click(
      within(rows[0]).getByRole("button", { name: "Grant Org Structure" }),
    );
    await waitFor(() => expect(onUpdate).toHaveBeenCalled());
    expect(onUpdate).toHaveBeenCalledWith(
      expect.objectContaining({
        tools: expect.arrayContaining([
          expect.objectContaining({ key: "org", enabled: true }),
        ]),
      }),
    );
    expect(screen.queryByRole("button", { name: "Add Tool" })).toBeNull();
  });
});
