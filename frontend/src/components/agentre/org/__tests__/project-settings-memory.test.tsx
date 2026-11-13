import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  ListChatAgents: vi.fn(),
  MemoryCountsByOwner: vi.fn(),
  ProjectAddMember: vi.fn(),
  ProjectClearLocalPath: vi.fn(),
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

vi.mock("../../../../../wailsjs/go/app/App", () => appMocks);

import { useChatAgentsStore } from "@/stores/chat-agents-store";

import { ProjectSettingsDrawer } from "../../project-settings-drawer";

// 桌面宿主的接缝：项目设置弹窗的「记忆 N 条」行取自 MemoryCountsByOwner，点击跳到
// 记忆页并选中这个项目；读数失败时弹窗照常，只是没有这一行。

function Where() {
  const loc = useLocation();
  return <div data-testid="where">{loc.pathname + loc.search}</div>;
}

function renderDrawer() {
  render(
    <MemoryRouter initialEntries={["/org"]}>
      <ProjectSettingsDrawer
        projectID={4}
        onClose={() => {}}
        onChanged={() => {}}
      />
      <Where />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  useChatAgentsStore.getState().__reset();
  appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
  appMocks.ProjectLocationList.mockResolvedValue([]);
  appMocks.RemoteDeviceList.mockResolvedValue([]);
  appMocks.ProjectListTree.mockResolvedValue([]);
  appMocks.ProjectGet.mockResolvedValue({
    project: {
      color: "agent-1",
      description: "",
      icon: "folder",
      id: 4,
      name: "harbor",
      path: "/tmp/harbor",
      localPathMissing: false,
    },
    directMembers: [],
    inheritedMembers: [],
  });
  Element.prototype.scrollIntoView = vi.fn();
});

describe("项目设置里的记忆行", () => {
  it("shows the project's count and routes to project:<id>", async () => {
    appMocks.MemoryCountsByOwner.mockResolvedValue([
      { owner: { kind: "project", id: 4 }, count: 5 },
      { owner: { kind: "agent", id: 4 }, count: 9 },
    ]);
    renderDrawer();
    const row = await screen.findByTestId("project-settings-memory");
    expect(row).toHaveTextContent("5 memories");
    await userEvent.setup({ pointerEventsCheck: 0 }).click(row);
    expect(screen.getByTestId("where")).toHaveTextContent(
      "/memory?owner=project:4",
    );
  });

  it("a failed count load keeps the dialog and drops the row", async () => {
    appMocks.MemoryCountsByOwner.mockRejectedValue(new Error("boom"));
    renderDrawer();
    await screen.findByTestId("project-section-identity");
    await waitFor(() =>
      expect(appMocks.MemoryCountsByOwner).toHaveBeenCalled(),
    );
    expect(screen.queryByTestId("project-settings-memory")).toBeNull();
  });
});
