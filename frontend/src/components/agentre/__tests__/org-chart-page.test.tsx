import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  consumeNewAgentDialogIntent,
  requestNewAgentDialog,
} from "@/stores/new-agent-intent-store";

const mocks = vi.hoisted(() => ({
  useOrgData: vi.fn(),
  useOrgIndexView: vi.fn(),
}));

vi.mock("../org/use-org-data", () => ({
  useOrgData: mocks.useOrgData,
}));
vi.mock("../org/use-org-index-view", () => ({
  useOrgIndexView: mocks.useOrgIndexView,
}));
vi.mock("../org/org-detail-agent", () => ({
  OrgDetailAgent: () => <div data-testid="agent-detail" />,
}));
vi.mock("../org/org-detail-department", () => ({
  OrgDetailDepartment: () => <div data-testid="department-detail" />,
}));
vi.mock("../icon-picker", () => ({
  AgentAvatarPicker: () => <div data-testid="agent-avatar-picker" />,
  IconPicker: () => <div data-testid="icon-picker" />,
}));

import { OrgChartPage } from "../org-chart-page";

const CEO = {
  id: 1,
  name: "CEO 助手",
  systemBadge: "DEFAULT",
  departmentId: 0,
  parentAgentId: 0,
  sortOrder: 0,
};
const EVA = {
  id: 2,
  name: "Eva",
  systemBadge: "",
  departmentId: 1,
  parentAgentId: 0,
  sortOrder: 1,
};
const BOB = {
  id: 3,
  name: "Bob",
  systemBadge: "",
  departmentId: 0,
  parentAgentId: 2,
  sortOrder: 1,
};
const DAN = {
  id: 5,
  name: "Dan",
  systemBadge: "",
  departmentId: 1,
  parentAgentId: 0,
  sortOrder: 2,
};
const ENGINEERING = {
  id: 1,
  name: "工程部",
  parentId: 0,
  leadAgentId: 2,
  leadAgentName: "Eva",
  sortOrder: 1,
  memberCount: 3,
};
const PLATFORM = {
  id: 2,
  name: "平台组",
  parentId: 1,
  leadAgentId: 0,
  leadAgentName: "",
  sortOrder: 1,
  memberCount: 0,
};
const PRODUCT = {
  id: 3,
  name: "产品部",
  parentId: 0,
  leadAgentId: 0,
  leadAgentName: "",
  sortOrder: 2,
  memberCount: 0,
};

function orgData(overrides: Record<string, unknown> = {}) {
  return {
    loading: false,
    error: null,
    departments: [ENGINEERING, PLATFORM, PRODUCT],
    agents: [CEO, EVA, BOB, DAN],
    backends: [{ id: 5, name: "Claude Code", type: "claudecode" }],
    availableTools: [],
    moveAgent: vi.fn(),
    moveDepartment: vi.fn(),
    reorderAgents: vi.fn(),
    reorderDepartments: vi.fn(),
    updateDepartment: vi.fn(),
    deleteDepartment: vi.fn(),
    updateAgent: vi.fn(),
    deleteAgent: vi.fn(),
    uploadAgentAvatar: vi.fn(),
    deleteAgentAvatar: vi.fn(),
    createDepartment: vi.fn(),
    createAgent: vi.fn(),
    ...overrides,
  };
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/org"]}>
      <OrgChartPage />
    </MemoryRouter>,
  );
}

function announcement(): string {
  return screen.getByTestId("org-index-announcer").textContent ?? "";
}

async function pickUp(user: ReturnType<typeof userEvent.setup>, id: string) {
  screen.getByTestId(id).focus();
  await user.keyboard(" ");
}

async function moveUntil(
  user: ReturnType<typeof userEvent.setup>,
  pattern: RegExp,
) {
  for (let step = 0; step < 40; step += 1) {
    if (pattern.test(announcement())) return;
    await user.keyboard("{ArrowDown}");
  }
  throw new Error(`no drop target matched ${pattern}: ${announcement()}`);
}

beforeEach(() => {
  consumeNewAgentDialogIntent();
  mocks.useOrgIndexView.mockReturnValue({
    selected: null,
    setSelected: vi.fn(),
  });
  mocks.useOrgData.mockReturnValue(orgData());
});

describe("OrgChartPage 一屏之内", () => {
  it("搜索、按后端 / 按汇报对象两项筛选与新建部门同屏在场", () => {
    renderPage();

    expect(screen.getByLabelText("Search agents")).toBeInTheDocument();
    expect(screen.getByLabelText("Filter by backend")).toBeInTheDocument();
    expect(screen.getByLabelText("Filter by manager")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "New Department" }),
    ).toBeInTheDocument();
  });

  it("画布与列表的两套开关都不在了：没有视图切换，也没有缩放", () => {
    renderPage();

    expect(screen.queryByRole("button", { name: "Tree view" })).toBeNull();
    expect(screen.queryByRole("button", { name: "List view" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Zoom In" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Zoom Out" })).toBeNull();
  });

  it("遗留的 agentre.orgView.mode 值被忽略：照常渲染索引，键也没被抹掉", () => {
    localStorage.setItem("agentre.orgView.mode", "list");

    renderPage();

    expect(screen.getByTestId("org-group-1")).toBeInTheDocument();
    expect(screen.getByTestId("org-row-2")).toBeInTheDocument();
    expect(localStorage.getItem("agentre.orgView.mode")).toBe("list");
  });

  it("新建部门按钮打开新建部门对话框", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(screen.getByRole("button", { name: "New Department" }));

    expect(
      await screen.findByRole("dialog", { name: "New Department" }),
    ).toBeInTheDocument();
  });

  it("点一行把选中态交给索引的选中状态", async () => {
    const setSelected = vi.fn();
    mocks.useOrgIndexView.mockReturnValue({ selected: null, setSelected });
    const user = userEvent.setup();
    renderPage();

    await user.click(screen.getByTestId("org-row-select-3"));

    expect(setSelected).toHaveBeenCalledWith({ kind: "agent", id: 3 });
  });
});

describe("OrgChartPage 三种落点发出的写操作", () => {
  it("落在组头上 → moveAgent，入参与今天一模一样", async () => {
    const data = orgData();
    mocks.useOrgData.mockReturnValue(data);
    const user = userEvent.setup();
    renderPage();

    await pickUp(user, "org-row-handle-3");
    await moveUntil(user, /move into 产品部/);
    await user.keyboard("{Enter}");

    expect(data.moveAgent).toHaveBeenCalledWith({
      id: 3,
      newDepartmentId: 3,
      newParentAgentId: 0,
      newSortOrder: 0,
    });
  });

  it("落在另一行上 → moveAgent 换汇报对象", async () => {
    const data = orgData();
    mocks.useOrgData.mockReturnValue(data);
    const user = userEvent.setup();
    renderPage();

    await pickUp(user, "org-row-handle-5");
    await moveUntil(user, /report to Bob/);
    await user.keyboard("{Enter}");

    expect(data.moveAgent).toHaveBeenCalledWith({
      id: 5,
      newDepartmentId: 0,
      newParentAgentId: 3,
      newSortOrder: 0,
    });
  });

  it("落在细线上 → reorderAgents(部门, 上级, 新次序)", async () => {
    const data = orgData();
    mocks.useOrgData.mockReturnValue(data);
    const user = userEvent.setup();
    renderPage();

    await pickUp(user, "org-row-handle-5");
    await moveUntil(user, /place before Eva/);
    await user.keyboard("{Enter}");

    expect(data.reorderAgents).toHaveBeenCalledWith(1, 0, [5, 2]);
  });

  it("部门落在部门组头上 → moveDepartment", async () => {
    const data = orgData();
    mocks.useOrgData.mockReturnValue(data);
    const user = userEvent.setup();
    renderPage();

    await pickUp(user, "org-group-handle-2");
    await moveUntil(user, /move into 产品部/);
    await user.keyboard("{Enter}");

    expect(data.moveDepartment).toHaveBeenCalledWith({
      id: 2,
      newParentId: 3,
      newSortOrder: 0,
    });
  });

  it("非法落点不发任何写操作", async () => {
    const data = orgData();
    mocks.useOrgData.mockReturnValue(data);
    const user = userEvent.setup();
    renderPage();

    // Eva 拖到自己的下属 Bob 上
    await pickUp(user, "org-row-handle-2");
    await moveUntil(user, /report to Bob/);
    await user.keyboard("{Enter}");

    expect(data.moveAgent).not.toHaveBeenCalled();
    expect(data.reorderAgents).not.toHaveBeenCalled();
  });
});

describe("OrgChartPage 空态的出路", () => {
  it("一个部门都没建时，索引的出路直接打开两个对话框", async () => {
    mocks.useOrgData.mockReturnValue(
      orgData({ departments: [], agents: [CEO] }),
    );
    const user = userEvent.setup();
    const { container } = renderPage();

    const empty = container.querySelector<HTMLElement>(
      '[data-slot="org-index-empty-departments"]',
    );
    expect(empty).not.toBeNull();

    await user.click(
      within(empty!).getByRole("button", { name: "New Department" }),
    );
    expect(
      await screen.findByRole("dialog", { name: "New Department" }),
    ).toBeInTheDocument();
  });

  it("没有任何可挂载节点时不给「添加 Agent」这条出路", () => {
    mocks.useOrgData.mockReturnValue(orgData({ departments: [], agents: [] }));
    const { container } = renderPage();

    const empty = container.querySelector<HTMLElement>(
      '[data-slot="org-index-empty-departments"]',
    );
    expect(
      within(empty!).queryByRole("button", { name: "Add Agent" }),
    ).toBeNull();
  });
});

describe("OrgChartPage navigation intents", () => {
  it("applies a no-backend agent selection when navigation targets an already-mounted org chart", async () => {
    const setSelected = vi.fn();
    mocks.useOrgIndexView.mockReturnValue({ selected: null, setSelected });

    render(
      <MemoryRouter
        initialEntries={[
          {
            pathname: "/org",
            state: { orgSelection: { kind: "agent", id: 42 } },
          },
        ]}
      >
        <OrgChartPage />
      </MemoryRouter>,
    );

    await waitFor(() =>
      expect(setSelected).toHaveBeenCalledWith({ kind: "agent", id: 42 }),
    );
  });

  it("Given a pending intent, when the org chart mounts, then it opens the dialog, selects CEO placement, leaves backend empty, and consumes the intent", async () => {
    mocks.useOrgData.mockReturnValue(
      orgData({ departments: [], agents: [CEO] }),
    );
    requestNewAgentDialog();

    renderPage();

    expect(
      await screen.findByText(
        "Configure the backend after creating the agent, then start chatting",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("combobox", {
        name: "Select a department or parent agent",
      }),
    ).toHaveTextContent("Agent · CEO 助手");
    expect(screen.getByRole("combobox", { name: "Backend" })).toHaveTextContent(
      "Backend",
    );
    await waitFor(() => expect(consumeNewAgentDialogIntent()).toBe(false));
  });

  it("Given no departments and an unordered agent list, when a pending intent opens the dialog, then CEO remains the selected parent", async () => {
    mocks.useOrgData.mockReturnValue(
      orgData({
        departments: [],
        agents: [
          { id: 2, name: "Worker", systemBadge: "" },
          { id: 1, name: "CEO 助手", systemBadge: "DEFAULT" },
        ],
      }),
    );
    requestNewAgentDialog();

    renderPage();

    expect(
      await screen.findByRole("combobox", {
        name: "Select a department or parent agent",
      }),
    ).toHaveTextContent("Agent · CEO 助手");
  });
});
