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
    attentionTone: null,
    allLocalPathsMissing: false,
    onOpenSettings: vi.fn(),
    onAddSubProject: vi.fn(),
    onNewSession: vi.fn(),
    onOpenTerminal: vi.fn(),
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

  it("Given a project whose local path is missing, When the ⋮ menu opens, Then the full item set shows in the package's order", async () => {
    const user = setupUser();
    renderHeader({ project: makeProject({ localPathMissing: true }) });

    await user.click(
      screen.getByRole("button", { name: "More actions for Atlas" }),
    );
    await screen.findByRole("menuitem", { name: "Settings" });

    expect(menuItemLabels()).toEqual([
      "Settings",
      "New subproject",
      "Members…",
      "Machines and paths…",
      "New Terminal",
      "Merge into an existing project",
      "Delete project",
    ]);
  });

  it("Given a configured local path, When the ⋮ menu opens, Then merge drops out — it is an R10 way out, not an always-on action", async () => {
    const user = setupUser();
    renderHeader({ project: makeProject({ localPathMissing: false }) });

    await user.click(
      screen.getByRole("button", { name: "More actions for Atlas" }),
    );
    await screen.findByRole("menuitem", { name: "Settings" });

    expect(menuItemLabels()).toEqual([
      "Settings",
      "New subproject",
      "Members…",
      "Machines and paths…",
      "New Terminal",
      "Delete project",
    ]);
  });

  it("Given the ⋮ menu, When 「机器与路径…」 is picked, Then settings opens straight at that section", async () => {
    const user = setupUser();
    const props = renderHeader();

    await user.click(
      screen.getByRole("button", { name: "More actions for Atlas" }),
    );
    await user.click(
      await screen.findByRole("menuitem", { name: "Machines and paths…" }),
    );
    expect(props.onOpenSettings).toHaveBeenCalledWith(1, "paths");
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

  it("Given the header is right-clicked, Then the menu is identical to ⋮ — one definition, two containers", async () => {
    renderHeader({ project: makeProject({ localPathMissing: true }) });

    fireEvent.contextMenu(screen.getByText("Atlas"));
    await screen.findByRole("menuitem", { name: "Settings" });

    /*
      此前这里守的是**相反**的事：「右键菜单比 ⋮ 少两项，这是既有差异，别顺手补齐」。
      规格 2026-08-22 决策 5 把那条差异判成 bug（问题 4）——两处各摆一遍就是两处各漏
      一项的机会，而右键那份漏掉的正是「成员…」「机器与路径…」。条目现在只在包里
      定义一次，两种容器各渲染一遍，所以这里改守「两处一模一样」。
    */
    expect(menuItemLabels()).toEqual([
      "Settings",
      "New subproject",
      "Members…",
      "Machines and paths…",
      "New Terminal",
      "Merge into an existing project",
      "Delete project",
    ]);
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

  it("Given the subtree has a running session, When the header renders, Then no left bar is drawn any more", () => {
    renderHeader({ attentionTone: "running", attentionCount: 3 });

    // 那根 3px 绿条撤了：它说的「子树里有东西在跑」，与同一行的计数记号完全重合，
    // 而且 hover 时整行底色会被顶掉、只剩它——三个记号说同一件事的局面到此为止。
    expect(screen.queryByTestId("project-running-indicator")).toBeNull();
  });

  it("Given the subtree only has unread sessions, When the header renders, Then the count is amber rather than green", () => {
    renderHeader({ attentionTone: "waiting", attentionCount: 3 });

    // 此前这里写死 text-status-running：3 条未读的项目显示绿色「3」，
    // 而那三行自己画的是琥珀点，组头和它自己的行对不上。
    const mark = screen.getByTestId("project-attention-mark");
    expect(mark.className).toMatch(/text-status-waiting-text/);
    expect(mark.className).not.toMatch(/text-status-running/);
  });

  it("Given a running subtree, When the header renders, Then the dot uses the saturated fill and the number the text role", () => {
    renderHeader({ attentionTone: "running", attentionCount: 2 });

    // 点是填充、数字是文字，两个角色分开取值——与会话行的「行首点 + 行尾短标签」
    // 逐字同一套投影。饱和色当文字在亮色下只有 2.31:1。
    const mark = screen.getByTestId("project-attention-mark");
    expect(mark.className).toMatch(/text-status-running-text/);
    expect(
      // 记号已经归共享包的组头外壳，四种组头共用同一个 slot 名。
      mark.querySelector("[data-slot='group-attention-dot']")?.className,
    ).toMatch(/bg-status-running/);
  });

  it("Given nothing needs attention, When the header renders, Then no mark is drawn", () => {
    renderHeader({ attentionTone: null, attentionCount: 0 });

    expect(screen.queryByTestId("project-attention-mark")).toBeNull();
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

  it("Given exactly one member, When ＋ is clicked, Then that member starts a session and no popover ever opens", async () => {
    const user = setupUser();
    appMocks.ProjectGet.mockResolvedValue({
      project: null,
      directMembers: [{ agentID: 7, inherited: false }],
      inheritedMembers: [],
    });
    const props = renderHeader();

    await user.click(
      screen.getByRole("button", { name: "New conversation in Atlas" }),
    );

    await waitFor(() => expect(props.onNewSession).toHaveBeenCalledWith(1, 7));
    // 成员在**打开之前**取出来，所以不再有「弹出来又自己关掉」那一闪。
    expect(screen.queryByTestId("project-add-popover")).toBeNull();
  });

  it("Given two members, When ＋ is clicked, Then both are listed and the inherited one is marked", async () => {
    const user = setupUser();
    appMocks.ProjectGet.mockResolvedValue({
      project: null,
      directMembers: [{ agentID: 7, inherited: false, agentName: "Scout" }],
      inheritedMembers: [{ agentID: 9, inherited: true, agentName: "Relay" }],
    });
    const props = renderHeader();

    await user.click(
      screen.getByRole("button", { name: "New conversation in Atlas" }),
    );

    expect(
      await screen.findByTestId("project-member-option-7"),
    ).toBeInTheDocument();
    expect(screen.getByText("Inherited")).toBeInTheDocument();
    expect(props.onNewSession).not.toHaveBeenCalled();

    await user.click(screen.getByTestId("project-member-option-9"));
    expect(props.onNewSession).toHaveBeenCalledWith(1, 9);
  });

  it("Given an agent is picked, When the popover closes, Then focus is not pulled back to ＋ — the new tab's editor has already claimed it", async () => {
    // 回归：新建会话后输入框「拿到焦点又丢了」。Radix 关闭时默认把焦点还给 trigger，
    // 正好抹掉 ChatPanelHost setTimeout(0) 给编辑器的那次 focus。
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
      name: "New conversation in Atlas",
    });
    await user.click(trigger);
    await user.click(await screen.findByTestId("project-member-option-5"));

    await waitFor(() => expect(document.activeElement).not.toBe(trigger));
  });

  it("Given the member list was empty on first open, When ＋ is reopened, Then members are refetched", async () => {
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
      name: "New conversation in Atlas",
    });
    await user.click(trigger);
    // 空的那次给的是一条去加成员的路，不是一句空话。
    expect(
      await screen.findByTestId("project-add-empty-action"),
    ).toBeInTheDocument();

    await user.keyboard("{Escape}");
    await user.click(trigger);
    expect(
      await screen.findByTestId("project-member-option-6"),
    ).toBeInTheDocument();
  });
});

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
