import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  ListChatAgents: vi.fn(),
  ProjectAddMember: vi.fn(),
  ProjectGet: vi.fn(),
  ProjectLocationList: vi.fn(),
  ProjectLocationRemove: vi.fn(),
  ProjectLocationUpsert: vi.fn(),
  ProjectRemoveMember: vi.fn(),
  ProjectListTree: vi.fn(),
  ProjectMove: vi.fn(),
  ProjectSetLocalPath: vi.fn(),
  ProjectUpdate: vi.fn(),
  RemoteDeviceList: vi.fn(),
  RemoteFsListDir: vi.fn(),
  RemoteFsMkdir: vi.fn(),
  SelectDirectory: vi.fn(),
}));

vi.mock("../../../../wailsjs/go/app/App", () => appMocks);

import { useChatAgentsStore } from "@/stores/chat-agents-store";

import { ProjectSettingsDrawer } from "../project-settings-drawer";

/**
 * 桌面端这一侧的**接缝**（规格 2026-08-22 B 段）。
 *
 * 弹窗本身住在共享包里、在那边测过了；这里只问一件事：wails 那几个绑定翻成
 * `ProjectSettingsPorts` 有没有翻对。包的测试全绿证明不了宿主接对了
 * （`docs/frontend.md`：a green package suite alone does not prove either host
 * wired the ports correctly）。
 */

function setupUser() {
  return userEvent.setup({ pointerEventsCheck: 0 });
}

function renderDrawer(onChanged = vi.fn()) {
  render(
    <ProjectSettingsDrawer
      projectID={1}
      onClose={() => {}}
      onChanged={onChanged}
    />,
  );
  return { onChanged };
}

function mockProject(over: Record<string, unknown> = {}) {
  appMocks.ProjectGet.mockResolvedValue({
    project: {
      color: "agent-1",
      description: "",
      icon: "folder",
      id: 1,
      name: "agentre",
      path: "/Users/me/Code/agentre",
      localPathMissing: false,
      ...over,
    },
    directMembers: [],
    inheritedMembers: [],
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  useChatAgentsStore.getState().__reset();
  appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
  appMocks.ProjectLocationList.mockResolvedValue([]);
  appMocks.RemoteDeviceList.mockResolvedValue([]);
  appMocks.ProjectListTree.mockResolvedValue([]);
  appMocks.ProjectMove.mockResolvedValue({ id: 1 });
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(() => {
  localStorage.clear();
});

describe("成员", () => {
  it("ProjectGet 带回来的显示名压过 Agent #id 那个兜底", async () => {
    mockProject();
    appMocks.ProjectGet.mockResolvedValue({
      project: {
        color: "agent-1",
        description: "",
        icon: "folder",
        id: 1,
        name: "Agentre",
        path: "/tmp/agentre",
      },
      directMembers: [
        {
          agentID: 5,
          agentName: "Builder",
          avatarColor: "agent-2",
          inherited: false,
        },
      ],
      inheritedMembers: [],
    });

    renderDrawer();

    expect(await screen.findByText("Builder")).toBeInTheDocument();
    expect(screen.queryByText("Agent #5")).not.toBeInTheDocument();
  });

  it("移除成员经 ProjectRemoveMember，成员 id 翻回数字", async () => {
    appMocks.ProjectGet.mockResolvedValue({
      project: {
        color: "agent-1",
        description: "",
        icon: "folder",
        id: 1,
        name: "Agentre",
        path: "",
      },
      directMembers: [{ agentID: 5, agentName: "Builder", inherited: false }],
      inheritedMembers: [],
    });

    renderDrawer();

    fireEvent.click(await screen.findByTestId("project-member-remove-5"));
    await waitFor(() =>
      expect(appMocks.ProjectRemoveMember).toHaveBeenCalledWith(1, 5),
    );
  });

  it("远端 Agent 的那台设备还没配路径时，候选留在列表里并说出原因", async () => {
    mockProject();
    appMocks.ListChatAgents.mockResolvedValue({
      agents: [
        {
          id: 7,
          name: "Remote",
          avatarColor: "agent-2",
          deviceID: "42",
          online: true,
        },
      ],
    });

    renderDrawer();

    const candidate = await screen.findByTestId("project-member-add-7");
    // 静默消失比一行灰字更让人找不着北。
    expect(candidate).toBeDisabled();
    expect(candidate.textContent).toContain("Configure Remote Paths first");
  });
});

describe("本机那一行", () => {
  it("路径来自 project.path，改它走 ProjectSetLocalPath 而不是 ProjectUpdate", async () => {
    const user = setupUser();
    mockProject();
    appMocks.ProjectSetLocalPath.mockResolvedValue({ id: 1 });

    renderDrawer();

    const input = await screen.findByDisplayValue("/Users/me/Code/agentre");
    await user.clear(input);
    await user.type(input, "/Users/me/Code/agentre-moved");
    fireEvent.blur(input);

    await waitFor(() =>
      expect(appMocks.ProjectSetLocalPath).toHaveBeenCalledWith({
        id: 1,
        path: "/Users/me/Code/agentre-moved",
      }),
    );
    expect(appMocks.ProjectUpdate).not.toHaveBeenCalled();
  });

  it("只改名字时本机路径纹丝不动", async () => {
    const user = setupUser();
    mockProject();
    appMocks.ProjectUpdate.mockResolvedValue({ id: 1 });

    renderDrawer();

    const name = await screen.findByTestId("project-settings-name");
    await user.clear(name);
    await user.type(name, "agentre2");
    fireEvent.blur(name);

    await waitFor(() =>
      // 包只递改动的那一格；这一端的 ProjectUpdate 收整份，adapter 负责合。
      expect(appMocks.ProjectUpdate).toHaveBeenCalledWith(
        expect.objectContaining({ id: 1, name: "agentre2", icon: "folder" }),
      ),
    );
    expect(appMocks.ProjectSetLocalPath).not.toHaveBeenCalled();
  });

  it("「选择…」走系统原生对话框，挑完**立刻落库**（即时保存，不再等一颗「保存」）", async () => {
    mockProject({ path: "", localPathMissing: true });
    appMocks.SelectDirectory.mockResolvedValue("/Users/me/Code/agentre-hub");
    appMocks.ProjectSetLocalPath.mockResolvedValue({ id: 1 });

    const { onChanged } = renderDrawer();

    fireEvent.click(await screen.findByTestId("project-path-choose-"));
    await waitFor(() => expect(appMocks.SelectDirectory).toHaveBeenCalled());
    await waitFor(() =>
      expect(appMocks.ProjectSetLocalPath).toHaveBeenCalledWith({
        id: 1,
        path: "/Users/me/Code/agentre-hub",
      }),
    );
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
  });

  it("原生对话框被取消时什么都不写", async () => {
    mockProject();
    appMocks.SelectDirectory.mockResolvedValue("");

    renderDrawer();

    fireEvent.click(await screen.findByTestId("project-path-choose-"));
    await waitFor(() => expect(appMocks.SelectDirectory).toHaveBeenCalled());
    expect(appMocks.ProjectSetLocalPath).not.toHaveBeenCalled();
  });

  it("后端拒绝时那句原文就地透出 —— 桌面端分不出类，所以不折成一句", async () => {
    const user = setupUser();
    mockProject();
    appMocks.ProjectSetLocalPath.mockRejectedValue("path does not exist");

    renderDrawer();

    const input = await screen.findByDisplayValue("/Users/me/Code/agentre");
    await user.clear(input);
    await user.type(input, "/Users/me/Code/gone");
    fireEvent.blur(input);

    expect(await screen.findByText(/path does not exist/)).toBeInTheDocument();
  });
});

describe("远端设备那几行", () => {
  it("写路径经 ProjectLocationUpsert；设备离线也配得了（位置表在本机的库里）", async () => {
    const user = setupUser();
    mockProject();
    appMocks.RemoteDeviceList.mockResolvedValue([
      { id: 42, name: "build-01", online: false },
    ]);
    appMocks.ProjectLocationUpsert.mockResolvedValue({});

    renderDrawer();

    const input = await screen.findByTestId("project-path-input-42");
    expect(input).not.toBeDisabled();
    await user.type(input, "/srv/work/atlas");
    fireEvent.blur(input);

    await waitFor(() =>
      expect(appMocks.ProjectLocationUpsert).toHaveBeenCalledWith(
        1,
        "42",
        "/srv/work/atlas",
      ),
    );
  });

  it("离线的设备答不出目录里有什么，所以只有「选择…」是停的", async () => {
    mockProject();
    appMocks.RemoteDeviceList.mockResolvedValue([
      { id: 42, name: "build-01", online: false },
    ]);

    renderDrawer();

    expect(await screen.findByTestId("project-path-choose-42")).toBeDisabled();
  });

  it("移除那台设备上的路径经 ProjectLocationRemove", async () => {
    mockProject();
    appMocks.RemoteDeviceList.mockResolvedValue([
      { id: 42, name: "build-01", online: true },
    ]);
    appMocks.ProjectLocationList.mockResolvedValue([
      {
        deviceId: "42",
        path: "/srv/work/atlas",
        deviceName: "build-01",
        online: true,
      },
    ]);
    appMocks.ProjectLocationRemove.mockResolvedValue(undefined);

    renderDrawer();

    fireEvent.click(await screen.findByTestId("project-path-remove-42"));
    await waitFor(() =>
      expect(appMocks.ProjectLocationRemove).toHaveBeenCalledWith(1, "42"),
    );
  });
});

describe("父项目", () => {
  it("列出树上其余项目，改它经 ProjectMove", async () => {
    mockProject();
    appMocks.ProjectListTree.mockResolvedValue([
      { project: { id: 1, name: "agentre" }, children: [] },
      { project: { id: 2, name: "platform" }, children: [] },
    ]);

    renderDrawer();

    const select = (await screen.findByTestId(
      "project-settings-parent",
    )) as HTMLSelectElement;
    // 候选里没有它自己 —— 指向自己是最短的那个环。
    expect(Array.from(select.options).map((o) => o.value)).toEqual(["", "2"]);

    fireEvent.change(select, { target: { value: "2" } });
    await waitFor(() =>
      expect(appMocks.ProjectMove).toHaveBeenCalledWith({ id: 1, parentID: 2 }),
    );
  });

  it("改父项目不顺手把别的字段一起重写 —— ProjectUpdate 不该被叫到", async () => {
    mockProject();
    appMocks.ProjectListTree.mockResolvedValue([
      { project: { id: 1, name: "agentre" }, children: [] },
      { project: { id: 2, name: "platform" }, children: [] },
    ]);

    renderDrawer();

    fireEvent.change(await screen.findByTestId("project-settings-parent"), {
      target: { value: "2" },
    });
    await waitFor(() => expect(appMocks.ProjectMove).toHaveBeenCalled());
    expect(appMocks.ProjectUpdate).not.toHaveBeenCalled();
  });

  it("树上只有它自己时那一格不画 —— 没有可选的父项目", async () => {
    mockProject();
    appMocks.ProjectListTree.mockResolvedValue([
      { project: { id: 1, name: "agentre" }, children: [] },
    ]);
    renderDrawer();
    await screen.findByTestId("project-section-basic");
    expect(screen.queryByTestId("project-settings-parent")).toBeNull();
  });
});

describe("这一端没有的那一格", () => {
  it("「危险」那一页没了，删除入口只剩组头 ⋮", async () => {
    mockProject();
    renderDrawer();
    await screen.findByTestId("project-section-basic");
    expect(
      screen.queryByRole("button", { name: /Delete Project/i }),
    ).toBeNull();
  });
});
