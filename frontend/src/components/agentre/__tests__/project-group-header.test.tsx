import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  ListChatAgents: vi.fn(),
  ProjectGet: vi.fn(),
  ProjectLocationList: vi.fn(),
  RemoteDeviceAdd: vi.fn(),
  RemoteDeviceList: vi.fn(),
  RemoteDeviceRefresh: vi.fn(),
  RemoteDeviceRemove: vi.fn(),
  RemoteDeviceRename: vi.fn(),
  RemoteDeviceUpdateTLS: vi.fn(),
  ServerListDevices: vi.fn(),
}));

vi.mock("../../../../wailsjs/go/app/App", () => appMocks);
// 组件间接 import wails runtime（use-remote-devices 订阅设备在线事件）：
// per-file mock，importActual 后只覆盖用到的两个，别加全局 alias。
vi.mock("../../../../wailsjs/runtime/runtime", async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  EventsOn: vi.fn(() => () => {}),
  EventsOff: vi.fn(),
}));

import { useChatAgentsStore } from "@/stores/chat-agents-store";

import {
  ProjectGroupHeader,
  type ProjectGroupHeaderProps,
} from "../session-index/project-group-header";
import type { app } from "../../../../wailsjs/go/models";

function makeProject(
  overrides: Partial<app.ProjectItem> = {},
): app.ProjectItem {
  return {
    id: 1,
    parentID: 0,
    name: "Atlas",
    icon: "folder",
    color: "agent-1",
    description: "",
    path: "/tmp/atlas",
    isGitRepo: false,
    sortOrder: 0,
    createtime: 0,
    updatetime: 0,
    localPathMissing: false,
    ...overrides,
  } as app.ProjectItem;
}

function renderHeader(overrides: Partial<ProjectGroupHeaderProps> = {}) {
  const props: ProjectGroupHeaderProps = {
    project: makeProject(),
    depth: 0,
    expanded: true,
    onToggle: vi.fn(),
    attentionCount: 0,
    hasRunning: false,
    allLocalPathsMissing: false,
    onOpenSettings: vi.fn(),
    onAddSubProject: vi.fn(),
    onNewSession: vi.fn(),
    onOpenTerminal: vi.fn(),
    onSpecifyPath: vi.fn(),
    onMergeInto: vi.fn(),
    onDelete: vi.fn(),
    ...overrides,
  };
  render(<ProjectGroupHeader {...props} />);
  return props;
}

// Radix 菜单在 happy-dom 中需要关闭 pointerEvents 检查。
function setupUser() {
  return userEvent.setup({ pointerEventsCheck: 0 });
}

function menuItemLabels() {
  return screen.getAllByRole("menuitem").map((el) => el.textContent);
}

describe("ProjectGroupHeader ⋮ menu", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    useChatAgentsStore.getState().__reset();
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectGet.mockResolvedValue({
      project: null,
      directMembers: [],
      inheritedMembers: [],
    });
    appMocks.ProjectLocationList.mockResolvedValue([]);
    appMocks.RemoteDeviceList.mockResolvedValue([]);
    appMocks.ServerListDevices.mockResolvedValue([]);
  });

  it("Given a project whose local path is missing, When the ⋮ menu opens, Then all six actions are reachable", async () => {
    const user = setupUser();
    const props = renderHeader({
      project: makeProject({ localPathMissing: true }),
    });

    await user.click(
      screen.getByRole("button", { name: "More actions for Atlas" }),
    );
    await screen.findByRole("menuitem", { name: "Project Settings" });

    expect(menuItemLabels()).toEqual([
      "Project Settings",
      "New Sub-project",
      "New Terminal",
      "Specify path…",
      "Merge into existing project…",
      "Delete Project",
    ]);

    await user.click(screen.getByRole("menuitem", { name: "Specify path…" }));
    expect(props.onSpecifyPath).toHaveBeenCalledWith(1);
  });

  it("Given a project with a configured local path, When the ⋮ menu opens, Then only the four always-on actions show", async () => {
    const user = setupUser();
    renderHeader({ project: makeProject({ localPathMissing: false }) });

    await user.click(
      screen.getByRole("button", { name: "More actions for Atlas" }),
    );
    await screen.findByRole("menuitem", { name: "Project Settings" });

    expect(menuItemLabels()).toEqual([
      "Project Settings",
      "New Sub-project",
      "New Terminal",
      "Delete Project",
    ]);
    expect(screen.queryByText("Specify path…")).toBeNull();
    expect(screen.queryByText("Merge into existing project…")).toBeNull();
  });
});

describe("ProjectGroupHeader context menu", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    useChatAgentsStore.getState().__reset();
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectGet.mockResolvedValue({
      project: null,
      directMembers: [],
      inheritedMembers: [],
    });
    appMocks.ProjectLocationList.mockResolvedValue([]);
    appMocks.RemoteDeviceList.mockResolvedValue([]);
    appMocks.ServerListDevices.mockResolvedValue([]);
  });

  it("Given a project whose local path is missing, When the header is right-clicked, Then exactly four items show and neither specify-path nor merge is among them", async () => {
    const props = renderHeader({
      project: makeProject({ localPathMissing: true }),
    });

    fireEvent.contextMenu(screen.getByText("Atlas"));
    await screen.findByRole("menuitem", { name: "Project Settings" });

    // 右键菜单比 ⋮ 少「指定本机路径」「合并到已有项目」—— 这是既有差异，
    // 不是遗漏；守卫住它，别被「顺手补齐」。
    expect(menuItemLabels()).toEqual([
      "Project Settings",
      "New Sub-project",
      "New Terminal",
      "Delete Project",
    ]);
    expect(screen.queryByText("Specify path…")).toBeNull();
    expect(screen.queryByText("Merge into existing project…")).toBeNull();
    expect(props.onSpecifyPath).not.toHaveBeenCalled();
    expect(props.onMergeInto).not.toHaveBeenCalled();
  });
});

describe("ProjectGroupHeader running highlight", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    useChatAgentsStore.getState().__reset();
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectGet.mockResolvedValue({
      project: null,
      directMembers: [],
      inheritedMembers: [],
    });
    appMocks.ProjectLocationList.mockResolvedValue([]);
    appMocks.RemoteDeviceList.mockResolvedValue([]);
    appMocks.ServerListDevices.mockResolvedValue([]);
  });

  it("Given the subtree has a running session, When the header renders, Then the left running bar shows", () => {
    renderHeader({ hasRunning: true });

    expect(screen.getByTestId("project-running-indicator")).toBeInTheDocument();
  });

  it("Given nothing in the subtree runs, When the header renders, Then no running bar shows", () => {
    renderHeader({ hasRunning: false });

    expect(screen.queryByTestId("project-running-indicator")).toBeNull();
  });
});

describe("ProjectGroupHeader ＋ member picker", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    useChatAgentsStore.getState().__reset();
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectLocationList.mockResolvedValue([]);
    appMocks.RemoteDeviceList.mockResolvedValue([]);
    appMocks.ServerListDevices.mockResolvedValue([]);
  });

  it("Given the project has exactly one member, When ＋ is clicked, Then that member starts a session and the list never expands", async () => {
    const user = setupUser();
    appMocks.ProjectGet.mockResolvedValue({
      project: null,
      directMembers: [{ agentID: 7, inherited: false }],
      inheritedMembers: [],
    });
    const props = renderHeader();

    await user.click(
      screen.getByRole("button", { name: "New session in Atlas" }),
    );

    await waitFor(() => {
      expect(props.onNewSession).toHaveBeenCalledWith(1, 7);
    });
    await waitFor(() => {
      expect(screen.queryByText("Choose an Agent")).toBeNull();
    });
    expect(screen.queryByRole("menuitem")).toBeNull();
  });

  it("Given the project has two members, When ＋ is clicked, Then the picker lists both and marks the inherited one", async () => {
    const user = setupUser();
    appMocks.ProjectGet.mockResolvedValue({
      project: null,
      directMembers: [{ agentID: 7, inherited: false, agentName: "Scout" }],
      inheritedMembers: [{ agentID: 9, inherited: true, agentName: "Relay" }],
    });
    const props = renderHeader();

    await user.click(
      screen.getByRole("button", { name: "New session in Atlas" }),
    );

    expect(await screen.findByText("Choose an Agent")).toBeInTheDocument();
    expect(screen.getByText("Inherited")).toBeInTheDocument();
    expect(props.onNewSession).not.toHaveBeenCalled();

    await user.click(screen.getByRole("menuitem", { name: /Relay/ }));
    expect(props.onNewSession).toHaveBeenCalledWith(1, 9);
  });

  it("Given an agent is picked, When the menu closes, Then Radix does not pull focus back to ＋ — the new tab's editor has already claimed it", async () => {
    // 回归：新建会话后输入框「拿到焦点又丢了」。Radix DropdownMenu 默认的
    // onCloseAutoFocus 在关闭时把焦点还给 trigger，正好抹掉 ChatPanelHost
    // setTimeout(0) 给编辑器的那次 focus。修复 = onCloseAutoFocus preventDefault。
    const user = setupUser();
    appMocks.ProjectGet.mockResolvedValue({
      project: null,
      directMembers: [
        { agentID: 5, agentName: "Builder", inherited: false },
        { agentID: 6, agentName: "Reviewer", inherited: false },
      ],
      inheritedMembers: [],
    });
    renderHeader();

    const trigger = screen.getByRole("button", {
      name: "New session in Atlas",
    });
    await user.click(trigger);
    await user.click(await screen.findByText("Builder"));

    await waitFor(() => {
      expect(document.activeElement).not.toBe(trigger);
    });
  });

  it("Given the member list was empty on first open, When ＋ is reopened, Then members are refetched instead of reusing the stale empty menu", async () => {
    const user = setupUser();
    appMocks.ProjectGet.mockResolvedValueOnce({
      project: null,
      directMembers: [],
      inheritedMembers: [],
    }).mockResolvedValueOnce({
      project: null,
      directMembers: [
        { agentID: 6, agentName: "Reviewer", inherited: false },
        { agentID: 7, agentName: "Auditor", inherited: false },
      ],
      inheritedMembers: [],
    });
    renderHeader();

    const trigger = screen.getByRole("button", {
      name: "New session in Atlas",
    });
    await user.click(trigger);
    expect(await screen.findByText(/No members yet/)).toBeInTheDocument();

    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(screen.queryByText(/No members yet/)).not.toBeInTheDocument();
    });
    await user.click(trigger);

    expect(await screen.findByText("Reviewer")).toBeInTheDocument();
    expect(appMocks.ProjectGet).toHaveBeenCalledTimes(2);
  });
});

// 「新建终端」子菜单的设备矩阵。合并前它有一套组件级测试（靠一个只为测试存在的
// DropdownMenu harness）；现在从组头的 ⋮ 菜单真实打开，不再为测试导出内部组件。
describe("ProjectGroupHeader new-terminal submenu", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    useChatAgentsStore.getState().__reset();
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectGet.mockResolvedValue({
      project: null,
      directMembers: [],
      inheritedMembers: [],
    });
    appMocks.ProjectLocationList.mockResolvedValue([]);
    appMocks.RemoteDeviceList.mockResolvedValue([]);
    appMocks.ServerListDevices.mockResolvedValue([]);
  });

  /** 打开 ⋮ 菜单并把「新建终端」子菜单 hover 开。 */
  async function openTerminalSub(user: ReturnType<typeof setupUser>) {
    await user.click(
      screen.getByRole("button", { name: "More actions for Atlas" }),
    );
    await user.hover(await screen.findByText("New Terminal"));
  }

  it("Given the submenu opens, When 本地 is clicked, Then a local terminal is requested with an empty deviceID", async () => {
    const user = setupUser();
    const props = renderHeader();

    await openTerminalSub(user);
    // fireEvent 直接触发 Radix 的 onSelect —— user.click 会先 pointer-leave 把子菜单收起。
    fireEvent.click(await screen.findByText("Local"));

    // deviceName 在组头这一层归一成 ""（onOpenTerminal 收的是 string，不是可选），
    // 所以本地终端是「空 deviceID + 空名字」而不是 undefined。
    expect(props.onOpenTerminal).toHaveBeenCalledWith(1, "", "");
  });

  it("Given an online device with a configured path, When its item is clicked, Then the terminal opens on that device", async () => {
    const user = setupUser();
    appMocks.RemoteDeviceList.mockResolvedValue([
      { id: 42, name: "MacMini", online: true },
    ]);
    appMocks.ProjectLocationList.mockResolvedValue([
      { id: 1, projectId: 1, deviceId: "42", path: "/tmp/proj", online: true },
    ]);
    const props = renderHeader();

    await openTerminalSub(user);
    const item = await screen.findByText("MacMini");
    expect(item.closest("[data-disabled]")).toBeNull();

    fireEvent.click(item);

    expect(props.onOpenTerminal).toHaveBeenCalledWith(1, "42", "MacMini");
  });

  it("Given an online device with no configured path, Then its item is disabled — there is nowhere to cd to", async () => {
    const user = setupUser();
    appMocks.RemoteDeviceList.mockResolvedValue([
      { id: 7, name: "NAS", online: true },
    ]);
    appMocks.ProjectLocationList.mockResolvedValue([]);
    renderHeader();

    await openTerminalSub(user);

    const item = await screen.findByText(/NAS/);
    expect(item.closest("[data-disabled]")).not.toBeNull();
  });

  it("Given an offline device, Then its item is disabled and says so", async () => {
    const user = setupUser();
    appMocks.RemoteDeviceList.mockResolvedValue([
      { id: 9, name: "OfflineBox", online: false },
    ]);
    appMocks.ProjectLocationList.mockResolvedValue([
      { id: 2, projectId: 1, deviceId: "9", path: "/tmp/proj", online: false },
    ]);
    renderHeader();

    await openTerminalSub(user);

    const item = await screen.findByText(/OfflineBox/);
    expect(item).toHaveTextContent("offline");
    expect(item.closest("[data-disabled]")).not.toBeNull();
  });
});
