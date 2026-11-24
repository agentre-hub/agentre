import "@testing-library/jest-dom/vitest";

import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

const emitters = vi.hoisted(
  () => new Map<string, Array<(p: unknown) => void>>(),
);
vi.mock("@/../wailsjs/runtime/runtime", async () => ({
  ...(await vi.importActual("@/../wailsjs/runtime/runtime")),
  EventsOn: vi.fn((name: string, cb: (p: unknown) => void) => {
    emitters.set(name, [...(emitters.get(name) ?? []), cb]);
    return () => {
      emitters.set(
        name,
        (emitters.get(name) ?? []).filter((f) => f !== cb),
      );
    };
  }),
  EventsOff: vi.fn(),
}));

const api = vi.hoisted(() => ({
  IssueListBySession: vi.fn(),
  IssueSearchForSession: vi.fn(),
  IssueLinkSession: vi.fn(),
  IssueUnlinkSession: vi.fn(),
  IssueCreateLinked: vi.fn(),
  ListSessionDispatches: vi.fn(),
  ListSessionsSharingRoot: vi.fn(),
}));
vi.mock("@/../wailsjs/go/app/App", () => ({
  ...api,
  WorkspaceFsWorkRoots: () => Promise.resolve([]),
  WorkspaceFsGitState: () => Promise.resolve({ notARepo: true }),
  WorkspaceFsGitChanges: () =>
    Promise.resolve({ notARepo: true, changes: [], baseRef: "" }),
  WorkspaceFsListDir: () =>
    Promise.resolve({ path: "/repo", entries: [], truncated: false }),
  GetAppSetting: () => Promise.reject(new Error("nf")),
}));
vi.mock("../../../../../wailsjs/go/app/App", () => ({
  GetAppSetting: () => Promise.reject(new Error("nf")),
  UpdateAppSettings: vi.fn(),
}));

import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useSessionRootStore } from "@/stores/session-root-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { ChatContextToolWindows } from "../../chat-context-sidebar";
import {
  showOnly,
  stripButton,
} from "../../chat-context-sidebar/__tests__/tool-window-helpers";

const issue = (id: number, title: string, stage: string, other = 0) => ({
  id,
  title,
  stage,
  otherSessionCount: other,
});

function empty() {
  api.IssueListBySession.mockResolvedValue([]);
  api.ListSessionDispatches.mockResolvedValue({
    dispatchedBy: [],
    dispatched: [],
  });
  api.ListSessionsSharingRoot.mockResolvedValue([]);
  api.IssueSearchForSession.mockResolvedValue([]);
}

function LocationProbe() {
  const loc = useLocation();
  return <div data-testid="location">{loc.pathname + loc.search}</div>;
}

function renderWindows(sessionId = 1) {
  return render(
    <MemoryRouter>
      <ChatContextToolWindows
        sessionId={sessionId}
        messages={[]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
        cwd="/repo"
      />
      <LocationProbe />
    </MemoryRouter>,
  );
}

const region = () => screen.getByRole("region", { name: "Relations" });

async function openRelations() {
  const view = renderWindows();
  await userEvent.setup().click(stripButton("Relations"));
  return view;
}

beforeEach(() => {
  localStorage.clear();
  emitters.clear();
  Object.values(api).forEach((f) => f.mockReset());
  sonnerMocks.toast.error.mockReset();
  empty();
  showOnly();
  useChatTabsStore.setState({ tabs: [], activeTabId: null });
  useSessionStatusStore.getState().__reset();
  useSessionRootStore.setState({ bySession: {} });
});

describe("关联工具窗 · 四组", () => {
  // Given 四组都有内容, Then 自上而下依次是任务 / 派发我的 / 我派发的 / 同一份代码上在跑,标题带数量(条目总数)。
  it("renders the four groups in order and counts every entry in the title", async () => {
    api.IssueListBySession.mockResolvedValue([
      issue(12, "Dark mode", "doing", 1),
      issue(15, "Icon contrast", "todo"),
    ]);
    api.ListSessionDispatches.mockResolvedValue({
      dispatchedBy: [
        {
          dispatchId: 1,
          sessionId: 4,
          title: "Plan dark mode",
          agentName: "Architect",
          method: "agrctl_send",
          createdAt: Date.now() - 2 * 3600_000,
        },
      ],
      dispatched: [
        {
          dispatchId: 2,
          sessionId: 21,
          task: "Check hardcoded colours",
          agentName: "Reviewer",
          method: "agrctl_send",
          status: "running",
          durationMs: 100_000,
          isolated: false,
          createdAt: 1,
        },
      ],
    });
    api.ListSessionsSharingRoot.mockResolvedValue([
      {
        sessionId: 8,
        title: "Safari blank page",
        agentName: "Codex",
        status: "running",
        rootName: "demo-app",
        overlapFirst: "",
        overlapCount: 0,
      },
    ]);
    await openRelations();

    const headings = await within(region()).findAllByRole("heading", {
      level: 4,
    });
    expect(headings.map((h) => h.textContent)).toEqual([
      "Tasks",
      "Dispatched by",
      "I dispatched",
      "Running on the same code",
    ]);
    expect(
      within(region()).getAllByRole("heading", { level: 3 })[0],
    ).toHaveTextContent("Relations5");
    // 任务行:编号、标题、阶段、另有 N 个会话。
    const task = within(region()).getByRole("button", {
      name: /#12.*Dark mode/,
    });
    expect(task).toHaveTextContent("In progress");
    expect(task).toHaveTextContent("1 other session");
    // 派发我的:会话标题、Agent、派发方式、相对时间。
    const by = within(region()).getByRole("button", { name: /Plan dark mode/ });
    expect(by).toHaveTextContent("Architect");
    expect(by).toHaveTextContent("agrctl send");
    expect(by).toHaveTextContent("2h ago");
    // 我派发的:任务、Agent、方式、状态(与子代理卡同词表)、耗时。
    const out = within(region()).getByRole("button", {
      name: /Check hardcoded colours/,
    });
    expect(out).toHaveTextContent("Reviewer");
    expect(within(out).getByRole("img", { name: "Running" })).toBeTruthy();
    expect(out).toHaveTextContent("1m 40s");
    // 同根:标题、Agent、根名。
    const same = within(region()).getByRole("button", {
      name: /Safari blank page/,
    });
    expect(same).toHaveTextContent("Codex");
    expect(same).toHaveTextContent("demo-app");
  });

  // Given 只有「我派发的」有内容, Then 其余三组整组不显示。
  it("hides every empty group", async () => {
    api.ListSessionDispatches.mockResolvedValue({
      dispatchedBy: [],
      dispatched: [
        {
          dispatchId: 2,
          sessionId: 21,
          task: "Only task",
          agentName: "R",
          method: "agent_call",
          status: "completed",
          durationMs: 5000,
          isolated: false,
          createdAt: 1,
        },
      ],
    });
    await openRelations();
    await within(region()).findByText("Only task");

    const headings = within(region()).getAllByRole("heading", { level: 4 });
    expect(headings.map((h) => h.textContent)).toEqual(["I dispatched"]);
  });

  // Given 四组都为空, Then 显示空态并保留「关联任务」「新建任务」两个按钮。
  it("shows an empty state that keeps the two task buttons", async () => {
    await openRelations();

    expect(
      await within(region()).findByText("Nothing related yet"),
    ).toBeTruthy();
    expect(within(region()).queryAllByRole("heading", { level: 4 })).toEqual(
      [],
    );
    expect(
      within(region()).getByRole("button", { name: "Link task" }),
    ).toBeTruthy();
    expect(
      within(region()).getByRole("button", { name: "New task" }),
    ).toBeTruthy();
    expect(
      within(region()).getAllByRole("heading", { level: 3 })[0],
    ).toHaveTextContent("Relations0");
  });
});

describe("关联工具窗 · 任务、关联弹层与新建", () => {
  beforeEach(() => {
    api.IssueListBySession.mockResolvedValue([issue(12, "Dark mode", "doing")]);
  });

  // When 点任务行, Then 打开看板并定位到该任务。
  it("opens the board focused on the task when its row is clicked", async () => {
    await openRelations();
    await userEvent
      .setup()
      .click(await within(region()).findByRole("button", { name: /#12/ }));

    expect(screen.getByTestId("location")).toHaveTextContent(
      "/issues?issue=12",
    );
  });

  // Given 搜索结果里 #12 已关联、#18 未关联, When 点它们, Then 已关联的取消、未关联的关联,勾随之切换。
  it("toggles links from the picker, with a check on linked items", async () => {
    api.IssueSearchForSession.mockResolvedValue([
      { id: 12, title: "Dark mode", stage: "doing", linked: true },
      { id: 18, title: "Flash on switch", stage: "review", linked: false },
    ]);
    api.IssueLinkSession.mockResolvedValue(undefined);
    api.IssueUnlinkSession.mockResolvedValue(undefined);
    const user = userEvent.setup();
    await openRelations();
    await user.click(
      await within(region()).findByRole("button", { name: "Link task" }),
    );

    const list = await screen.findByRole("listbox");
    const linked = within(list).getByRole("option", { name: /#12/ });
    const other = within(list).getByRole("option", { name: /#18/ });
    expect(linked).toHaveAttribute("aria-selected", "true");
    expect(other).toHaveAttribute("aria-selected", "false");
    expect(within(linked).getByTestId("task-linked-check")).toBeTruthy();
    expect(other).toHaveTextContent("Review");

    await user.click(other);
    await waitFor(() =>
      expect(api.IssueLinkSession).toHaveBeenCalledWith(1, 18),
    );
    await waitFor(() =>
      expect(
        within(screen.getByRole("listbox")).getByRole("option", {
          name: /#18/,
        }),
      ).toHaveAttribute("aria-selected", "true"),
    );

    await user.click(
      within(screen.getByRole("listbox")).getByRole("option", { name: /#12/ }),
    );
    await waitFor(() =>
      expect(api.IssueUnlinkSession).toHaveBeenCalledWith(1, 12),
    );
    await waitFor(() =>
      expect(
        within(screen.getByRole("listbox")).getByRole("option", {
          name: /#12/,
        }),
      ).toHaveAttribute("aria-selected", "false"),
    );
  });

  // Given 搜索词「dark」, Then 列表以「新建任务『dark』」结尾,点它用搜索词建任务并关联;没有搜索词时不出现。
  it("ends the list with a create item that uses the search term", async () => {
    api.IssueCreateLinked.mockResolvedValue({ id: 30 });
    api.IssueSearchForSession.mockResolvedValue([
      { id: 40, title: "Dark theme", stage: "todo", linked: false },
    ]);
    const user = userEvent.setup();
    await openRelations();
    await user.click(
      await within(region()).findByRole("button", { name: "Link task" }),
    );
    await screen.findByRole("listbox");
    expect(screen.queryByRole("button", { name: /New task “/ })).toBeNull();

    await user.type(screen.getByPlaceholderText("Search tasks"), "dark");
    await waitFor(() =>
      expect(api.IssueSearchForSession).toHaveBeenLastCalledWith(1, "dark"),
    );
    const create = await screen.findByRole("button", {
      name: "New task “dark”",
    });
    const options = await screen.findAllByRole("option");
    expect(
      options[options.length - 1].compareDocumentPosition(create) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();

    await user.click(create);
    await waitFor(() =>
      expect(api.IssueCreateLinked).toHaveBeenCalledWith(
        expect.objectContaining({ sessionID: 1, title: "dark" }),
      ),
    );
  });

  // When 点组下方「新建任务」, Then 不带标题调用(后端用会话标题作默认),随后任务组重取。
  it("creates a linked task with the default (session) title from the button", async () => {
    api.IssueCreateLinked.mockResolvedValue({ id: 31 });
    await openRelations();
    await within(region()).findByRole("button", { name: /#12/ });
    api.IssueListBySession.mockResolvedValue([
      issue(12, "Dark mode", "doing"),
      issue(31, "Session title", "todo"),
    ]);

    await userEvent
      .setup()
      .click(within(region()).getByRole("button", { name: "New task" }));

    await waitFor(() =>
      expect(api.IssueCreateLinked).toHaveBeenCalledWith(
        expect.objectContaining({ sessionID: 1, title: "" }),
      ),
    );
    expect(
      await within(region()).findByRole("button", {
        name: /#31.*Session title/,
      }),
    ).toBeTruthy();
  });

  // Given 新建失败, Then 报错提示,窗口保持原状。
  it("reports a failed create without changing the list", async () => {
    api.IssueCreateLinked.mockRejectedValue(new Error("boom"));
    await openRelations();
    await within(region()).findByRole("button", { name: /#12/ });

    await userEvent
      .setup()
      .click(within(region()).getByRole("button", { name: "New task" }));

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    expect(within(region()).getByRole("button", { name: /#12/ })).toBeTruthy();
  });
});

describe("关联工具窗 · 派发", () => {
  const dispatched = (over: Record<string, unknown>) => ({
    dispatchId: 2,
    sessionId: 21,
    task: "Task text",
    agentName: "Coder",
    method: "agrctl_send",
    status: "completed",
    durationMs: 42_000,
    isolated: false,
    createdAt: 1,
    ...over,
  });

  // When 点「派发我的」行, Then 打开那个会话。
  it("opens the dispatching session when a dispatched-by row is clicked", async () => {
    api.ListSessionDispatches.mockResolvedValue({
      dispatchedBy: [
        {
          dispatchId: 1,
          sessionId: 4,
          title: "Upstream",
          agentName: "Boss",
          method: "agent_call",
          createdAt: Date.now(),
        },
      ],
      dispatched: [],
    });
    await openRelations();
    const row = await within(region()).findByRole("button", {
      name: /Upstream/,
    });
    expect(row).toHaveTextContent("agent dispatch");

    await userEvent.setup().click(row);

    const tab = useChatTabsStore.getState().tabs[0];
    expect(tab.meta).toMatchObject({ kind: "session", sessionId: 4 });
  });

  // When 点可见的「我派发的」行, Then 打开那个会话。
  it("opens a visible dispatched session on click", async () => {
    api.ListSessionDispatches.mockResolvedValue({
      dispatchedBy: [],
      dispatched: [dispatched({ sessionId: 21 })],
    });
    await openRelations();

    await userEvent
      .setup()
      .click(
        await within(region()).findByRole("button", { name: /Task text/ }),
      );

    expect(useChatTabsStore.getState().tabs[0].meta).toMatchObject({
      kind: "session",
      sessionId: 21,
    });
  });

  // Given 隔离会话, Then 标「隔离」,点击不打开会话而是展开最后一条回复;再点收起。
  it("marks isolated sessions, never opens them, and expands the last reply", async () => {
    api.ListSessionDispatches.mockResolvedValue({
      dispatchedBy: [],
      dispatched: [
        dispatched({
          isolated: true,
          method: "agent_call",
          lastReply: "The final answer",
        }),
      ],
    });
    const user = userEvent.setup();
    await openRelations();
    const row = await within(region()).findByRole("button", {
      name: /Task text/,
    });
    expect(row).toHaveTextContent("Isolated");
    expect(row).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText("The final answer")).toBeNull();

    await user.click(row);

    expect(row).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText("The final answer")).toBeTruthy();
    expect(useChatTabsStore.getState().tabs).toEqual([]);

    await user.click(row);
    expect(screen.queryByText("The final answer")).toBeNull();
  });

  // Given 隔离会话还没有回复, Then 展开后说明还没有回复。
  it("says there is no reply yet for an isolated session still running", async () => {
    api.ListSessionDispatches.mockResolvedValue({
      dispatchedBy: [],
      dispatched: [dispatched({ isolated: true, status: "running" })],
    });
    await openRelations();
    await userEvent
      .setup()
      .click(
        await within(region()).findByRole("button", { name: /Task text/ }),
      );
    expect(screen.getByText("No reply yet")).toBeTruthy();
  });

  // 状态词表与子代理卡一致:等待 / 运行中 / 已完成 / 失败 / 已取消。
  it("uses the subagent-card status vocabulary", async () => {
    const statuses: Array<[string, string]> = [
      ["queued", "Waiting"],
      ["running", "Running"],
      ["completed", "Done"],
      ["failed", "Failed"],
      ["canceled", "Cancelled"],
    ];
    api.ListSessionDispatches.mockResolvedValue({
      dispatchedBy: [],
      dispatched: statuses.map(([status], i) =>
        dispatched({
          dispatchId: i + 1,
          sessionId: 100 + i,
          task: `t-${status}`,
          status,
        }),
      ),
    });
    await openRelations();
    for (const [status, label] of statuses) {
      const row = await within(region()).findByRole("button", {
        name: new RegExp(`t-${status}`),
      });
      expect(within(row).getByRole("img", { name: label })).toBeTruthy();
    }
  });
});

describe("关联工具窗 · 同一份代码上在跑", () => {
  const sharing = (over: Record<string, unknown>) => ({
    sessionId: 8,
    title: "Safari blank page",
    agentName: "Codex",
    status: "running",
    rootName: "demo-app",
    overlapFirst: "",
    overlapCount: 0,
    ...over,
  });

  // Given 对方与本会话写过同一个文件, Then 整行提醒色并写明文件,图标条上出现提醒色圆点(窗口收起时也在)。
  it("alerts the overlapping row and puts a dot on the strip icon", async () => {
    api.ListSessionsSharingRoot.mockResolvedValue([
      sharing({ overlapFirst: "src/styles/tokens.css", overlapCount: 1 }),
      sharing({ sessionId: 9, title: "Calm one" }),
    ]);
    renderWindows();

    // 窗口还收着:图标上就已经有圆点。
    await waitFor(() =>
      expect(
        within(stripButton("Relations")).getByTestId("tool-window-alert-dot"),
      ).toBeTruthy(),
    );
    await userEvent.setup().click(stripButton("Relations"));

    const row = await within(region()).findByRole("button", {
      name: /Safari blank page/,
    });
    expect(row).toHaveAttribute("data-alert", "true");
    expect(row).toHaveTextContent("Also edited src/styles/tokens.css");
    const calm = within(region()).getByRole("button", { name: /Calm one/ });
    expect(calm).not.toHaveAttribute("data-alert");
  });

  // 多个重叠文件:写第一个并附数量。
  it("writes the first file plus the count when several overlap", async () => {
    api.ListSessionsSharingRoot.mockResolvedValue([
      sharing({ overlapFirst: "a.css", overlapCount: 3 }),
    ]);
    await openRelations();
    expect(
      await within(region()).findByText("Also edited a.css and 3 files in all"),
    ).toBeTruthy();
  });

  // Given 在跑但没有重叠, Then 图标条上没有圆点。
  it("has no strip dot without any overlap", async () => {
    api.ListSessionsSharingRoot.mockResolvedValue([sharing({})]);
    await openRelations();
    await within(region()).findByRole("button", { name: /Safari blank page/ });
    expect(
      within(stripButton("Relations")).queryByTestId("tool-window-alert-dot"),
    ).toBeNull();
  });

  // When 点同根会话行, Then 打开它。
  it("opens the other session on click", async () => {
    api.ListSessionsSharingRoot.mockResolvedValue([sharing({})]);
    await openRelations();
    await userEvent.setup().click(
      await within(region()).findByRole("button", {
        name: /Safari blank page/,
      }),
    );
    expect(useChatTabsStore.getState().tabs[0].meta).toMatchObject({
      kind: "session",
      sessionId: 8,
    });
  });

  // 刷新:本会话与同伴的 chat:workroots 推送、其它会话的运行态变化都重取这一组。
  it("refetches on a workroots push and when some session's run state changes", async () => {
    api.ListSessionsSharingRoot.mockResolvedValue([sharing({})]);
    await openRelations();
    await within(region()).findByRole("button", { name: /Safari blank page/ });
    const calls = () => api.ListSessionsSharingRoot.mock.calls.length;
    const base = calls();

    // 本会话 AI 换了根(推送经会话根 store 到达)。
    act(() => {
      useSessionRootStore.getState().attach(1, "/repo");
      useSessionRootStore.getState().applyRoots(1, "/repo", [
        {
          path: "/repo",
          name: "repo",
          isWorktree: false,
          isPrimary: true,
          aiHere: false,
        },
        {
          path: "/repo-wt",
          name: "wt",
          isWorktree: true,
          isPrimary: false,
          aiHere: true,
        },
      ] as never);
    });
    await waitFor(() => expect(calls()).toBeGreaterThan(base));

    // 同伴(列表里的会话 8)换了根:它的推送也订阅。
    const afterSelf = calls();
    await act(async () => {
      emitters
        .get("chat:workroots:8")
        ?.forEach((f) => f({ sessionId: 8, roots: [] }));
    });
    await waitFor(() => expect(calls()).toBeGreaterThan(afterSelf));

    // 别的会话开始运行。
    const afterPeer = calls();
    act(() => {
      useSessionStatusStore
        .getState()
        .upsert(77, { agentStatus: "running", needsAttention: false });
    });
    await waitFor(() => expect(calls()).toBeGreaterThan(afterPeer));
  });

  // 取数失败只降级:一组取不到,其它组照常显示。
  it("degrades a failing group without blanking the others", async () => {
    api.ListSessionsSharingRoot.mockRejectedValue(new Error("boom"));
    api.IssueListBySession.mockResolvedValue([issue(12, "Dark mode", "doing")]);
    await openRelations();
    expect(
      await within(region()).findByRole("button", { name: /#12/ }),
    ).toBeTruthy();
  });
});
