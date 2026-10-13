// frontend/src/components/agentre/__tests__/session-index-drag-keyboard.test.tsx
//
// 项目轴的拖拽排序与键盘。**这里用真的 dnd-kit**：页面级那一份把 DndContext 整个
// 换成了透明容器（happy-dom 里没有布局，指针拖拽无从谈起），于是「拖拽的键盘监听
// 抢了组头自己的键」这件事在那边永远显不出来。
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  DeleteChatSession: vi.fn(),
  ListChatAgents: vi.fn(),
  ListChatIndexSessions: vi.fn(),
  ProjectCreate: vi.fn(),
  ProjectDelete: vi.fn(),
  ProjectDetectGitRepo: vi.fn(),
  ProjectGet: vi.fn(),
  ProjectListTree: vi.fn(),
  ProjectLocationList: vi.fn(),
  ProjectMerge: vi.fn(),
  ProjectReorder: vi.fn(),
  ProjectSetLocalPath: vi.fn(),
  ProjectUpdate: vi.fn(),
  RemoteDeviceList: vi.fn(),
  RenameChatSession: vi.fn(),
  SelectDirectory: vi.fn(),
  ServerListDevices: vi.fn(),
  SetAgentPinned: vi.fn(),
}));

vi.mock("../../../../wailsjs/runtime/runtime", async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  EventsOn: vi.fn(() => () => {}),
  EventsOff: vi.fn(),
}));
vi.mock("../../../../wailsjs/go/app/App", () => appMocks);

import { __resetProjectTreeForTesting } from "@/hooks/use-project-tree";
import { useChatAgentsStore } from "@/stores/chat-agents-store";
import { useSessionIndexStore } from "@/stores/session-index-store";
import { useSidebarAxisStore } from "@/stores/sidebar-axis-store";

import { SessionIndexPage } from "../session-index/index-page";

function project(id: number, name: string) {
  return {
    project: {
      color: "agent-1",
      icon: "folder",
      id,
      localPathMissing: false,
      name,
      parentID: 0,
      path: `/tmp/${name}`,
    },
    children: [],
  };
}

function toggleOf(name: string): HTMLElement {
  const toggle = screen.getByText(name).closest("button");
  if (!toggle) throw new Error(`no toggle for ${name}`);
  return toggle;
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  __resetProjectTreeForTesting();
  useSidebarAxisStore.getState().__reset();
  useChatAgentsStore.getState().__reset();
  useSessionIndexStore.getState().__reset();
  appMocks.ProjectListTree.mockResolvedValue([
    project(1, "Alpha"),
    project(2, "Beta"),
  ]);
  appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
  appMocks.ListChatIndexSessions.mockResolvedValue({
    sessions: [],
    total: 0,
    hasMore: false,
  });
  appMocks.ProjectGet.mockResolvedValue({
    project: null,
    directMembers: [],
    inheritedMembers: [],
  });
  appMocks.ProjectLocationList.mockResolvedValue([]);
  appMocks.RemoteDeviceList.mockResolvedValue([]);
  appMocks.ServerListDevices.mockResolvedValue([]);
  appMocks.ProjectReorder.mockResolvedValue(undefined);
});
afterEach(() => {
  localStorage.clear();
});

describe("SessionIndexPage project headers and the keyboard", () => {
  it("Given a focused project header, When Enter is pressed, Then the group folds — the drag listeners must not swallow the header's own key", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={["/chat"]}>
        <SessionIndexPage />
      </MemoryRouter>,
    );
    await screen.findByText("Alpha");
    expect(toggleOf("Alpha")).toHaveAttribute("aria-expanded", "true");

    toggleOf("Alpha").focus();
    await user.keyboard("{Enter}");

    expect(toggleOf("Alpha")).toHaveAttribute("aria-expanded", "false");
  });

  it("Given a focused project header, When Space is pressed, Then the project is picked up for reordering rather than folded", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={["/chat"]}>
        <SessionIndexPage />
      </MemoryRouter>,
    );
    await screen.findByText("Alpha");

    toggleOf("Alpha").focus();
    await user.keyboard(" ");

    expect(toggleOf("Alpha")).toHaveAttribute("aria-expanded", "true");
    // dnd-kit 拾起之后用一块 live region 播报，这是它唯一留在 DOM 里的痕迹。播的
    // 是「拾起」还是紧跟着的「移到了谁上面」取决于有没有算出落点，两句都算拾起了。
    expect(
      await screen.findByText(
        /Picked up draggable item project-1|Draggable item project-1 was moved/,
      ),
    ).toBeTruthy();
  });

  it("Given the ⋮ button is focused, When Enter is pressed, Then its menu opens instead of the project being picked up", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={["/chat"]}>
        <SessionIndexPage />
      </MemoryRouter>,
    );
    await screen.findByText("Alpha");

    screen.getByRole("button", { name: "More actions for Alpha" }).focus();
    await user.keyboard("{Enter}");

    expect(
      await screen.findByRole("menuitem", { name: "Project settings…" }),
    ).toBeInTheDocument();
  });
});
