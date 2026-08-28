import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  IssueList: vi.fn(),
  IssueListLabels: vi.fn(),
  IssueCreate: vi.fn(),
  IssueUpdate: vi.fn(),
  IssueDelete: vi.fn(),
  IssueMove: vi.fn(),
  IssueCreateLabel: vi.fn(),
  IssueUpdateLabel: vi.fn(),
  IssueDeleteLabel: vi.fn(),
  ProjectListTree: vi.fn(),
  ListChatAgents: vi.fn(),
  SyncStatus: vi.fn(),
  SyncAcknowledgeBoardJoinNotice: vi.fn(),
  ListAgentExecTargetAvailability: vi.fn(),
  ListAgentBackends: vi.fn(),
  ServerListDevices: vi.fn(),
  ListLLMProviders: vi.fn(),
  ListLLMModels: vi.fn(),
}));
vi.mock("../../../../wailsjs/go/app/App", () => appMocks);
vi.mock("../../../../wailsjs/runtime/runtime", () => ({
  EventsOn: () => () => {},
}));

import { useProjectListStore } from "@/stores/project-list-store";
import { useChatAgentsStore } from "@/stores/chat-agents-store";

import { IssuesPage } from "../issues-page";

const card = {
  id: 142,
  projectID: 4,
  title: "fix OAuth state loss",
  body: "the callback drops it",
  stage: "todo",
  position: 10,
  updatetime: 0,
  labels: [{ id: 1, name: "bug", tone: "red", usageCount: 2 }],
};

function boardResponse(patch: Record<string, unknown> = {}) {
  return {
    issues: [card],
    stageCounts: { todo: 1, doing: 0, review: 0, done: 0 },
    stageTotals: { todo: 9, doing: 2, review: 0, done: 0 },
    projectCounts: [{ projectID: 4, count: 3 }],
    ...patch,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  useProjectListStore.getState().__reset();
  useChatAgentsStore.getState().__reset();
  appMocks.IssueList.mockResolvedValue(boardResponse());
  appMocks.IssueListLabels.mockResolvedValue([
    { id: 1, name: "bug", tone: "red", usageCount: 2 },
  ]);
  appMocks.ProjectListTree.mockResolvedValue([
    {
      project: {
        id: 4,
        name: "Agentre",
        path: "/p",
        color: "agent-1",
        icon: "",
      },
      children: [],
    },
  ]);
  appMocks.ListChatAgents.mockResolvedValue([]);
  appMocks.SyncStatus.mockResolvedValue({
    enabled: true,
    boardJoinNoticePending: false,
  });
  appMocks.ListAgentExecTargetAvailability.mockResolvedValue([]);
  appMocks.ListAgentBackends.mockResolvedValue({ items: [] });
  appMocks.ServerListDevices.mockResolvedValue([]);
  appMocks.ListLLMProviders.mockResolvedValue({ items: [] });
  appMocks.ListLLMModels.mockResolvedValue({ items: [] });
});

describe("IssuesPage", () => {
  it("Given tasks from the binding, When the board renders, Then each card shows up under its column", async () => {
    render(<IssuesPage />);

    expect(await screen.findByText("fix OAuth state loss")).toBeInTheDocument();
    expect(screen.getByTestId("board-card-142")).toBeInTheDocument();
  });

  it("Given the stage totals, When the header renders, Then it counts tasks and work in progress, not open and closed", async () => {
    render(<IssuesPage />);

    expect(
      await screen.findByText("11 tasks · 2 in progress"),
    ).toBeInTheDocument();
  });

  it("Given a project is picked, When the scope changes, Then the next request is narrowed to that subtree", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<IssuesPage />);
    await screen.findByTestId("board-card-142");

    await user.click(screen.getByTestId("scope-trigger"));
    await user.click(await screen.findByTestId("scope-row-4"));

    await waitFor(() =>
      expect(appMocks.IssueList).toHaveBeenLastCalledWith(
        expect.objectContaining({ scope: "project", projectID: 4 }),
      ),
    );
  });

  it("Given a keyword, When the debounce elapses, Then the board refetches and the column head shows hits over total", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<IssuesPage />);
    await screen.findByTestId("board-card-142");

    await user.type(screen.getByRole("searchbox"), "oauth");

    await waitFor(() =>
      expect(appMocks.IssueList).toHaveBeenLastCalledWith(
        expect.objectContaining({ keyword: "oauth" }),
      ),
    );
    expect(
      await screen.findByTestId("board-column-count-todo"),
    ).toHaveTextContent("1 / 9");
  });

  it("Given the card menu, When a target column is chosen, Then the move is written and the board refetches", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    appMocks.IssueMove.mockResolvedValue({});
    render(<IssuesPage />);
    await screen.findByTestId("board-card-142");

    await user.click(screen.getByTestId("board-card-menu-142"));
    // Radix 的子菜单在 jsdom 里不认合成的 hover，键盘那条路才是真的能走通的一条
    // ——「卡片菜单的键盘可达」本来也是规格要求的验收点。
    await user.keyboard("{ArrowDown}{ArrowDown}{ArrowRight}");
    await user.click(
      await screen.findByRole("menuitem", { name: "In progress" }),
    );

    await waitFor(() =>
      expect(appMocks.IssueMove).toHaveBeenCalledWith(
        expect.objectContaining({ id: 142, stage: "doing" }),
      ),
    );
    await waitFor(() => expect(appMocks.IssueList).toHaveBeenCalledTimes(2));
  });

  it("Given an empty board with no filter, When it renders, Then it offers to create the first task", async () => {
    appMocks.IssueList.mockResolvedValue(
      boardResponse({ issues: [], stageCounts: {}, stageTotals: {} }),
    );
    render(<IssuesPage />);

    expect(
      await screen.findByText("No tasks in this project yet"),
    ).toBeInTheDocument();
  });

  it("Given a filter that matches nothing, When it renders, Then it offers to clear the filter instead", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<IssuesPage />);
    await screen.findByTestId("board-card-142");
    appMocks.IssueList.mockResolvedValue(
      boardResponse({ issues: [], stageCounts: {}, stageTotals: {} }),
    );

    await user.type(screen.getByRole("searchbox"), "nothing");

    expect(
      await screen.findByText("No tasks match your filters"),
    ).toBeInTheDocument();
  });

  it("Given a dragged card dropped on another column, When it lands, Then the move is written", async () => {
    appMocks.IssueMove.mockResolvedValue({});
    render(<IssuesPage />);
    await screen.findByTestId("board-card-142");

    const dragged = screen.getByTestId("board-card-body-142");
    const target = screen.getByTestId("board-column-scroll-review");
    fireEvent.dragStart(dragged);
    fireEvent.dragOver(target);
    fireEvent.drop(target);

    await waitFor(() =>
      expect(appMocks.IssueMove).toHaveBeenCalledWith(
        expect.objectContaining({ id: 142, stage: "review" }),
      ),
    );
  });

  it("Given the one-time board sync notice, When it is dismissed, Then it is acknowledged and disappears", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    appMocks.SyncStatus.mockResolvedValue({
      enabled: true,
      boardJoinNoticePending: true,
    });
    appMocks.SyncAcknowledgeBoardJoinNotice.mockResolvedValue(undefined);
    render(<IssuesPage />);

    const notice = await screen.findByTestId("board-join-notice");
    await user.click(within(notice).getByRole("button"));

    expect(appMocks.SyncAcknowledgeBoardJoinNotice).toHaveBeenCalled();
    await waitFor(() =>
      expect(screen.queryByTestId("board-join-notice")).not.toBeInTheDocument(),
    );
  });

  it("Given the new task button, When a title is typed and saved, Then the task is created with the three execution fields", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    appMocks.IssueCreate.mockResolvedValue({ id: 9 });
    render(<IssuesPage />);
    await screen.findByTestId("board-card-142");

    await user.click(screen.getByRole("button", { name: "New task" }));
    await user.type(await screen.findByTestId("task-title"), "ship it");
    await user.click(screen.getByTestId("task-form-submit"));

    await waitFor(() =>
      expect(appMocks.IssueCreate).toHaveBeenCalledWith(
        expect.objectContaining({
          title: "ship it",
          assigneeAgentID: 0,
          agentBackendID: 0,
          llmProviderKey: "",
          llmModelKey: "",
        }),
      ),
    );
  });

  it("Given the + on a column, When the form opens, Then it starts in that column and lands there", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    appMocks.IssueCreate.mockResolvedValue({ id: 10 });
    render(<IssuesPage />);
    await screen.findByTestId("board-card-142");

    await user.click(screen.getByTestId("board-column-add-review"));
    await user.type(await screen.findByTestId("task-title"), "review this");
    await user.click(screen.getByTestId("task-form-submit"));

    await waitFor(() =>
      expect(appMocks.IssueCreate).toHaveBeenCalledWith(
        expect.objectContaining({ title: "review this", stage: "review" }),
      ),
    );
  });

  it("Given a keyword that only the body carries, When the board renders, Then the card quotes one line of it", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<IssuesPage />);
    await screen.findByTestId("board-card-142");

    await user.type(screen.getByRole("searchbox"), "callback");

    const excerpt = await screen.findByTestId("board-card-excerpt-142");
    expect(excerpt).toHaveTextContent("the callback drops it");
  });

  it("Given no agent on the task form, When the execution pills render, Then the machine and model pills are disabled", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<IssuesPage />);
    await screen.findByTestId("board-card-142");

    await user.click(screen.getByRole("button", { name: "New task" }));

    expect(await screen.findByTestId("exec-target-pill")).toBeDisabled();
    expect(screen.getByTestId("model-pill")).toBeDisabled();
  });

  it("Given a task that already has an agent, machine and model, When the agent is cleared, Then none of the three is saved", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    appMocks.IssueList.mockResolvedValue(
      boardResponse({
        issues: [
          {
            ...card,
            assigneeAgentID: 7,
            agentBackendID: 11,
            llmProviderKey: "prov",
            llmModelKey: "model",
          },
        ],
      }),
    );
    appMocks.ListChatAgents.mockResolvedValue({
      agents: [
        { id: 7, name: "Ada", color: "agent-1", backendType: "claudecode" },
      ],
    });
    appMocks.ListAgentExecTargetAvailability.mockResolvedValue([
      {
        agentBackendId: 11,
        available: true,
        reason: "",
        hint: "",
        projectPath: "/p",
        kind: "local",
      },
    ]);
    appMocks.ListAgentBackends.mockResolvedValue({
      items: [{ id: 11, type: "claudecode", name: "本机", deviceId: "" }],
    });
    appMocks.IssueUpdate.mockResolvedValue({});
    render(<IssuesPage />);
    await screen.findByTestId("board-card-142");

    await user.click(screen.getByTestId("board-card-body-142"));
    await user.click(await screen.findByTestId("task-pill-agent"));
    await user.click(await screen.findByTestId("task-agent-none"));
    await user.click(screen.getByTestId("task-form-submit"));

    // 没有 Agent 就解不出机器与模型（那两颗此刻是禁用态）：把上一位的档一起存
    // 下去，读回来时说的是一件从来没成立过的事。
    await waitFor(() =>
      expect(appMocks.IssueUpdate).toHaveBeenCalledWith(
        expect.objectContaining({
          assigneeAgentID: 0,
          agentBackendID: 0,
          llmProviderKey: "",
          llmModelKey: "",
        }),
      ),
    );
  });

  it("Given a machine whose backend type differs from the agent's, When it is picked, Then the model pill follows the machine", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    appMocks.ListChatAgents.mockResolvedValue({
      agents: [
        { id: 7, name: "Ada", color: "agent-1", backendType: "claudecode" },
      ],
    });
    appMocks.ListAgentExecTargetAvailability.mockResolvedValue([
      {
        agentBackendId: 11,
        available: true,
        reason: "",
        hint: "",
        projectPath: "/p",
        kind: "local",
      },
    ]);
    appMocks.ListAgentBackends.mockResolvedValue({
      items: [{ id: 11, type: "openclaw", name: "OpenClaw", deviceId: "" }],
    });
    render(<IssuesPage />);
    await screen.findByTestId("board-card-142");

    await user.click(screen.getByRole("button", { name: "New task" }));
    await user.click(await screen.findByTestId("task-pill-agent"));
    await user.click(await screen.findByTestId("task-agent-7"));
    // claudecode 的 Agent → 模型可选；换到一台 openclaw 的机器 → 那一颗关掉。
    await waitFor(() =>
      expect(screen.getByTestId("board-model-pill")).not.toBeDisabled(),
    );

    await user.click(await screen.findByTestId("board-exec-target-pill"));
    await user.click(await screen.findByTestId("board-exec-target-row-11"));

    await waitFor(() =>
      expect(screen.getByTestId("board-model-pill")).toBeDisabled(),
    );
  });

  it("Given the minimum window width, When the header renders, Then the scope picker takes a full-width second row", async () => {
    render(<IssuesPage />);
    await screen.findByTestId("board-card-142");

    // 860px 是最小窗口宽度（`internal/desktop`）：三件东西挤一行会先把选择器压没。
    const trigger = screen.getByTestId("scope-trigger");
    expect(trigger.className).toContain("max-[860px]:order-last");
    expect(trigger.className).toContain("max-[860px]:w-full");
    expect(trigger.className).toContain("max-[860px]:max-w-none");
    expect(trigger.closest("header")?.className).toContain(
      "min-[861px]:flex-nowrap",
    );
  });

  it("Given a card is opened for editing, When the form loads, Then its stage is editable and prefilled", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<IssuesPage />);
    await screen.findByTestId("board-card-142");

    await user.click(screen.getByTestId("board-card-body-142"));

    expect(await screen.findByTestId("task-title")).toHaveValue(
      "fix OAuth state loss",
    );
    expect(screen.getByTestId("task-pill-stage")).toHaveTextContent("To do");
  });

  it("Given the label manager, When a label is created, Then the write lands with its tone", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    appMocks.IssueCreateLabel.mockResolvedValue({});
    render(<IssuesPage />);
    await screen.findByTestId("board-card-142");

    await user.click(screen.getByTestId("filter-trigger"));
    await user.click(await screen.findByText("Manage labels"));
    await user.type(await screen.findByTestId("label-new-name"), "ui");
    await user.click(screen.getByTestId("label-create"));

    await waitFor(() =>
      expect(appMocks.IssueCreateLabel).toHaveBeenCalledWith(
        expect.objectContaining({ name: "ui" }),
      ),
    );
  });
});
