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

// R10：基本页签的本机路径字段 —— 已配置照旧只读，未配置换成可指定入口。
describe("ProjectSettingsDrawer basic tab local path (R10)", () => {
  it("Given a configured project, Then the path field stays a readonly input", async () => {
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

    renderDrawer();

    const input = await screen.findByDisplayValue("/Users/me/Code/agentre");
    expect(input).toHaveAttribute("readonly");
    expect(screen.queryByText("Specify…")).not.toBeInTheDocument();
  });

  it("Given an unconfigured project, When a directory is picked, Then it is saved and the change is announced", async () => {
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
    await user.click(await screen.findByText("Specify…"));

    await waitFor(() => {
      expect(appMocks.ProjectSetLocalPath).toHaveBeenCalledWith({
        id: 1,
        path: "/Users/me/Code/agentre-hub",
      });
    });
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
  });
});
