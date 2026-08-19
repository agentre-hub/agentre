import { render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// 选中一行时详情落在**主区**：索引收成左侧固定宽的一列，详情吃掉剩下的宽度。
// 三栏只有在主区那么宽的容器里才成立，所以这一条钉的是「谁是主区」，
// 而不是详情组件自己的内部版式（那在 org-detail-columns.test.tsx）。

const mocks = vi.hoisted(() => ({
  useOrgData: vi.fn(),
  useOrgIndexView: vi.fn(),
}));

vi.mock("../use-org-data", () => ({ useOrgData: mocks.useOrgData }));
vi.mock("../use-org-index-view", () => ({
  useOrgIndexView: mocks.useOrgIndexView,
}));

import { OrgChartPage } from "../../org-chart-page";

const EVA = {
  id: 2,
  name: "Eva",
  description: "工程总监",
  avatarColor: "agent-2",
  avatarIcon: "",
  avatarDataUrl: "",
  systemBadge: "",
  departmentId: 1,
  departmentName: "工程部",
  parentAgentId: 0,
  sortOrder: 1,
  prompt: [],
  skills: [],
  execTargets: [],
  tools: [],
};
const ENGINEERING = {
  id: 1,
  name: "工程部",
  description: "",
  icon: "hammer",
  accentColor: "agent-2",
  parentId: 0,
  leadAgentId: 0,
  leadAgentName: "",
  sortOrder: 1,
  memberCount: 1,
};

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
  mocks.useOrgData.mockReturnValue({
    loading: false,
    error: null,
    departments: [ENGINEERING],
    agents: [EVA],
    backends: [],
    availableTools: [],
    moveAgent: vi.fn(),
    moveDepartment: vi.fn(),
    reorderAgents: vi.fn(),
    reorderDepartments: vi.fn(),
    updateDepartment: vi.fn(),
    deleteDepartment: vi.fn(),
    updateAgent: vi.fn().mockResolvedValue(undefined),
    deleteAgent: vi.fn(),
    uploadAgentAvatar: vi.fn(),
    deleteAgentAvatar: vi.fn(),
    createDepartment: vi.fn(),
    createAgent: vi.fn(),
  });
  mocks.useOrgIndexView.mockReturnValue({
    selected: { kind: "agent", id: 2 },
    setSelected: vi.fn(),
  });
});

afterEach(() => {
  delete window.go;
  vi.clearAllMocks();
});

describe("org detail placement in the page shell", () => {
  it("Given a selected row, When /org renders, Then the detail owns the main area and the index is the narrow column", () => {
    render(
      <MemoryRouter initialEntries={["/org"]}>
        <OrgChartPage />
      </MemoryRouter>,
    );

    const main = screen.getByTestId("org-detail-main");
    // 详情吃掉剩余宽度（主区），索引是固定宽的一列 —— 三栏靠这个前提才装得下。
    expect(main.className).toContain("flex-1");
    expect(screen.getByTestId("org-index-pane").className).toMatch(
      /w-\[\d+px\]/,
    );
    expect(screen.getByTestId("org-index-pane").className).not.toContain(
      "flex-1",
    );
    expect(
      within(main).getByRole("region", { name: "Identity" }),
    ).toBeInTheDocument();
  });
});
