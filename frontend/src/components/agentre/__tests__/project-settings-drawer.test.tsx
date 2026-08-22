import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  ListChatAgents: vi.fn(),
  ProjectAddMember: vi.fn(),
  ProjectDelete: vi.fn(),
  ProjectGet: vi.fn(),
  ProjectLocationList: vi.fn(),
  ProjectLocationRemove: vi.fn(),
  ProjectLocationUpsert: vi.fn(),
  ProjectRemoveMember: vi.fn(),
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
 * 抽屉自己的测试家。合并前它挂在 project-page.test.tsx 里，随那个文件一起删掉了 ——
 * 组件本身一行没动，覆盖却归零。这里把它接回来。
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
      onDeleted={() => {}}
    />,
  );
  return { onChanged };
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  useChatAgentsStore.getState().__reset();
  appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
  appMocks.ProjectLocationList.mockResolvedValue([]);
  appMocks.RemoteDeviceList.mockResolvedValue([]);
});

afterEach(() => {
  localStorage.clear();
});

describe("ProjectSettingsDrawer members", () => {
  it("Given ProjectGet carries a member display name, Then it wins over the Agent #id fallback", async () => {
    const user = setupUser();
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
          avatarIcon: "hammer",
          inherited: false,
        },
      ],
      inheritedMembers: [],
    });

    renderDrawer();

    await user.click(await screen.findByRole("button", { name: "Members" }));

    expect(await screen.findByText("Builder")).toBeInTheDocument();
    expect(screen.queryByText("Agent #5")).not.toBeInTheDocument();
  });
});

// 基本页签的本机路径字段 —— 已配置/未配置统一为一个可编辑输入，改动随「保存」落库。
describe("ProjectSettingsDrawer basic tab local path", () => {
  function mockConfiguredProject() {
    appMocks.ProjectGet.mockResolvedValue({
      project: {
        color: "agent-1",
        description: "",
        icon: "folder",
        id: 1,
        name: "agentre",
        path: "/Users/me/Code/agentre",
        localPathMissing: false,
      },
      directMembers: [],
      inheritedMembers: [],
    });
  }

  it("Given a configured project, Then the path input is editable and offers the browse entry", async () => {
    mockConfiguredProject();

    renderDrawer();

    const input = await screen.findByDisplayValue("/Users/me/Code/agentre");
    expect(input).not.toHaveAttribute("readonly");
    expect(
      await screen.findByRole("button", { name: "Browse..." }),
    ).toBeInTheDocument();
  });

  it("Given a configured project, When the path is edited and saved, Then only ProjectSetLocalPath is called with the new path", async () => {
    const user = setupUser();
    mockConfiguredProject();
    appMocks.ProjectSetLocalPath.mockResolvedValue({
      id: 1,
      localPathMissing: false,
    });

    renderDrawer();

    const input = await screen.findByDisplayValue("/Users/me/Code/agentre");
    await user.clear(input);
    await user.type(input, "/Users/me/Code/agentre-moved");
    await user.click(await screen.findByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(appMocks.ProjectSetLocalPath).toHaveBeenCalledWith({
        id: 1,
        path: "/Users/me/Code/agentre-moved",
      });
    });
    expect(appMocks.ProjectUpdate).not.toHaveBeenCalled();
  });

  it("Given a configured project, When only the name changes, Then ProjectSetLocalPath stays untouched", async () => {
    const user = setupUser();
    mockConfiguredProject();

    renderDrawer();

    const nameInput = await screen.findByDisplayValue("agentre");
    await user.clear(nameInput);
    await user.type(nameInput, "agentre2");
    await user.click(await screen.findByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(appMocks.ProjectUpdate).toHaveBeenCalled();
    });
    expect(appMocks.ProjectSetLocalPath).not.toHaveBeenCalled();
  });

  it("Given a configured project, When the backend rejects the new path, Then the error surfaces", async () => {
    const user = setupUser();
    mockConfiguredProject();
    appMocks.ProjectSetLocalPath.mockRejectedValue("path does not exist");

    renderDrawer();

    const input = await screen.findByDisplayValue("/Users/me/Code/agentre");
    await user.clear(input);
    await user.type(input, "/Users/me/Code/gone");
    await user.click(await screen.findByRole("button", { name: "Save" }));

    expect(await screen.findByText("path does not exist")).toBeInTheDocument();
  });

  it("Given an unconfigured project, When a directory is picked and saved, Then it is saved and the change is announced", async () => {
    const user = setupUser();
    appMocks.ProjectGet.mockResolvedValue({
      project: {
        color: "agent-1",
        description: "",
        icon: "folder",
        id: 1,
        name: "agentre-hub",
        path: "",
        localPathMissing: true,
      },
      directMembers: [],
      inheritedMembers: [],
    });
    appMocks.SelectDirectory.mockResolvedValue("/Users/me/Code/agentre-hub");
    appMocks.ProjectSetLocalPath.mockResolvedValue({
      id: 1,
      localPathMissing: false,
    });

    const { onChanged } = renderDrawer();

    await screen.findByText(
      "This project was synced from your account — specify a directory to start chatting on this machine.",
    );
    await user.click(await screen.findByRole("button", { name: "Browse..." }));

    // 浏览只填入输入框，不落库；落库发生在「保存」。
    expect(
      await screen.findByDisplayValue("/Users/me/Code/agentre-hub"),
    ).toBeInTheDocument();
    expect(appMocks.ProjectSetLocalPath).not.toHaveBeenCalled();

    await user.click(await screen.findByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(appMocks.ProjectSetLocalPath).toHaveBeenCalledWith({
        id: 1,
        path: "/Users/me/Code/agentre-hub",
      });
    });
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
  });
});
