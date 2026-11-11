import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useChatTabsStore } from "@/stores/chat-tabs-store";

// sync:applied / config:changed 由测试手动发：记忆页要在它们到达时就地重拉。
const runtimeHandlers = vi.hoisted(
  () => new Map<string, Set<(payload: unknown) => void>>(),
);
vi.mock("../../../../wailsjs/runtime/runtime", () => ({
  EventsOn: (name: string, cb: (payload: unknown) => void) => {
    const set =
      runtimeHandlers.get(name) ?? new Set<(payload: unknown) => void>();
    set.add(cb);
    runtimeHandlers.set(name, set);
    return () => set.delete(cb);
  },
}));

const app = vi.hoisted(() => ({
  LoadOrg: vi.fn(),
  ProjectListTree: vi.fn(),
  MemoryCountsByOwner: vi.fn(),
  MemoryRecentChanges: vi.fn(),
  MemoryListByOwner: vi.fn(),
  MemoryVisible: vi.fn(),
  MemorySearch: vi.fn(),
  MemoryCreate: vi.fn(),
  MemoryUpdate: vi.fn(),
  MemorySetPinned: vi.fn(),
  MemoryArchive: vi.fn(),
  MemoryUnarchive: vi.fn(),
  MemoryRestorePrevious: vi.fn(),
  MemoryMove: vi.fn(),
  MemoryDelete: vi.fn(),
  SetAgentMemoryEnabled: vi.fn(),
}));
vi.mock("../../../../wailsjs/go/app/App", async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  ...app,
}));

const toast = vi.hoisted(() => ({ error: vi.fn(), success: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

import { MemoryPage } from "../memory-page";

const NOW = Date.now();

type Owner = { kind: string; id: number };
type Item = {
  id: number;
  owner: Owner;
  summary: string;
  detail: string;
  pinned: boolean;
  state: string;
  hasPrevious: boolean;
  prevSummary: string;
  prevDetail: string;
  creator: { kind: string; agentId: number; sessionId: number };
  lastChange: {
    action: string;
    actor: { kind: string; agentId: number; sessionId: number };
    at: number;
  };
  createtime: number;
  updatetime: number;
};

const USER = { kind: "user", agentId: 0, sessionId: 0 };

function item(
  id: number,
  summary: string,
  owner: Owner,
  over: Partial<Item> = {},
): Item {
  return {
    id,
    owner,
    summary,
    detail: "",
    pinned: false,
    state: "normal",
    hasPrevious: false,
    prevSummary: "",
    prevDetail: "",
    creator: USER,
    lastChange: { action: "created", actor: USER, at: NOW },
    createtime: NOW,
    updatetime: NOW,
    ...over,
  };
}

const GLOBAL = { kind: "global", id: 0 };
const AGENT_EVA = { kind: "agent", id: 2 };
const DEPT_BACKEND = { kind: "department", id: 1 };

function list(owner: Owner, over: Record<string, unknown> = {}) {
  return {
    owner,
    budget: 2000,
    usedChars: 0,
    carried: [],
    overBudget: [],
    archived: [],
    memoryEnabled: true,
    ...over,
  };
}

function seed() {
  app.LoadOrg.mockResolvedValue({
    departments: [
      {
        id: 1,
        name: "Backend",
        parentId: 0,
        sortOrder: 1,
        icon: "",
        accentColor: "",
      },
      {
        id: 3,
        name: "Platform",
        parentId: 1,
        sortOrder: 1,
        icon: "",
        accentColor: "",
      },
    ],
    agents: [
      {
        id: 2,
        name: "Eva",
        departmentId: 1,
        departmentName: "Backend",
        sortOrder: 1,
        avatarColor: "agent-3",
        avatarIcon: "",
        avatarDataUrl: "",
      },
    ],
    availableTools: [],
  });
  app.ProjectListTree.mockResolvedValue([
    {
      project: { id: 5, name: "Harbor", icon: "", color: "", parentID: 0 },
      children: [],
    },
  ]);
  app.MemoryCountsByOwner.mockResolvedValue([
    { owner: GLOBAL, count: 3 },
    { owner: DEPT_BACKEND, count: 5 },
    { owner: AGENT_EVA, count: 7 },
  ]);
  app.MemoryRecentChanges.mockResolvedValue([]);
  app.MemorySearch.mockResolvedValue([]);
  app.MemoryVisible.mockResolvedValue({ layers: [] });
  app.MemoryListByOwner.mockImplementation(async (owner: Owner) => list(owner));
}

function LocationProbe() {
  const location = useLocation();
  return (
    <div data-testid="location">{location.pathname + location.search}</div>
  );
}

function renderPage(path = "/memory") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/memory" element={<MemoryPage />} />
        <Route path="/chat" element={<div>chat-page</div>} />
      </Routes>
      <LocationProbe />
    </MemoryRouter>,
  );
}

function emit(name: string) {
  act(() => {
    for (const cb of [...(runtimeHandlers.get(name) ?? [])]) cb(["memory"]);
  });
}

function sidebar() {
  return screen.getByRole("navigation", { name: "Memory owners" });
}

beforeEach(() => {
  for (const fn of Object.values(app)) fn.mockReset();
  toast.error.mockReset();
  runtimeHandlers.clear();
  useChatTabsStore.setState({ tabs: [], activeTabId: null });
  seed();
});

describe("Memory page · left column", () => {
  it("Given owners and counts, Then it lists recent changes, global, departments indented by the org tree, agents and projects with their normal-entry counts", async () => {
    renderPage();
    const nav = await screen.findByRole("navigation", {
      name: "Memory owners",
    });
    const owner = (name: string) =>
      within(nav).getByRole("button", { name: new RegExp(`^${name}`) });

    await waitFor(() => expect(owner("Global")).toHaveTextContent("3"));
    expect(owner("Recent changes")).toBeInTheDocument();
    expect(owner("Backend")).toHaveTextContent("5");
    expect(owner("Platform")).toHaveTextContent("0");
    expect(owner("Platform")).toHaveAttribute("data-depth", "1");
    expect(owner("Backend")).toHaveAttribute("data-depth", "0");
    expect(owner("Eva")).toHaveTextContent("7");
    expect(owner("Harbor")).toHaveTextContent("0");
    expect(
      within(nav)
        .getAllByRole("heading", { level: 2 })
        .map((h) => h.textContent),
    ).toEqual(["Departments", "Agents", "Projects"]);
  });

  it("Given no owner in the route, Then global is selected; clicking another owner selects it through the route", async () => {
    renderPage();
    const nav = await screen.findByRole("navigation", {
      name: "Memory owners",
    });
    await waitFor(() =>
      expect(app.MemoryListByOwner).toHaveBeenCalledWith(GLOBAL),
    );
    expect(
      within(nav).getByRole("button", { name: /^Global/ }),
    ).toHaveAttribute("aria-current", "true");

    await userEvent.click(within(nav).getByRole("button", { name: /^Eva/ }));
    expect(screen.getByTestId("location")).toHaveTextContent(
      "/memory?owner=agent:2",
    );
    await waitFor(() =>
      expect(app.MemoryListByOwner).toHaveBeenCalledWith(AGENT_EVA),
    );
  });

  it("Given a search keyword, Then it searches every layer and groups the results by owner", async () => {
    app.MemorySearch.mockResolvedValue([
      item(11, "Run lint before pushing", AGENT_EVA),
      item(12, "lint config follows the repo", DEPT_BACKEND),
    ]);
    renderPage();
    await screen.findByRole("navigation", { name: "Memory owners" });

    await userEvent.type(
      screen.getByRole("searchbox", { name: "Search all memories" }),
      "lint",
    );

    const results = await screen.findByRole("region", {
      name: "Search results",
    });
    await waitFor(() =>
      expect(app.MemorySearch).toHaveBeenLastCalledWith("lint"),
    );
    const groups = await within(results).findAllByRole("heading", {
      level: 3,
    });
    expect(groups.map((g) => g.textContent)).toEqual(["Eva", "Backend"]);
    await userEvent.click(
      within(results).getByRole("button", {
        name: "Run lint before pushing",
      }),
    );
    expect(screen.getByTestId("location")).toHaveTextContent(
      "/memory?owner=agent:2",
    );
  });
});

describe("Memory page · an owner", () => {
  it("Given a deep link to an agent, Then the header shows its name, budget usage and memory switch, and the groups include inherited layers", async () => {
    app.MemoryListByOwner.mockResolvedValue(
      list(AGENT_EVA, {
        usedChars: 2240,
        carried: [item(1, "Run lint before pushing", AGENT_EVA)],
        overBudget: [item(2, "Wildcard DNS lives on AdGuard", AGENT_EVA)],
        archived: [item(3, "Old fact", AGENT_EVA, { state: "archived" })],
      }),
    );
    app.MemoryVisible.mockResolvedValue({
      layers: [
        {
          owner: GLOBAL,
          budget: 2000,
          carried: [item(9, "g", GLOBAL)],
          overBudget: [],
        },
        {
          owner: DEPT_BACKEND,
          budget: 2000,
          carried: [item(8, "d", DEPT_BACKEND), item(7, "d2", DEPT_BACKEND)],
          overBudget: [],
        },
        { owner: AGENT_EVA, budget: 2000, carried: [], overBudget: [] },
      ],
    });
    renderPage("/memory?owner=agent:2");

    const main = await screen.findByRole("main");
    expect(
      await within(main).findByRole("heading", { level: 1, name: "Eva" }),
    ).toBeInTheDocument();
    expect(within(main).getByRole("meter")).toHaveAttribute(
      "data-over",
      "true",
    );
    expect(
      within(main).getByRole("switch", { name: "Memory enabled" }),
    ).toBeChecked();
    expect(app.MemoryVisible).toHaveBeenCalledWith(2, 0);
    expect(
      within(main)
        .getAllByRole("heading", { level: 3 })
        .map((h) => h.textContent),
    ).toEqual([
      "Own1",
      "Over budget, not carried this time1",
      "Archived1",
      "Inherited",
    ]);

    await userEvent.click(
      within(main).getByRole("button", { name: "Open Backend, 2 entries" }),
    );
    expect(screen.getByTestId("location")).toHaveTextContent(
      "/memory?owner=department:1",
    );
  });

  it("When the agent switch is turned off, Then it writes the agent's memory switch", async () => {
    app.SetAgentMemoryEnabled.mockImplementation(async () => {
      app.MemoryListByOwner.mockResolvedValue(
        list(AGENT_EVA, { memoryEnabled: false }),
      );
      return { id: 2, enabled: false };
    });
    renderPage("/memory?owner=agent:2");
    const toggle = await screen.findByRole("switch", {
      name: "Memory enabled",
    });
    await userEvent.click(toggle);
    expect(app.SetAgentMemoryEnabled).toHaveBeenCalledWith({
      id: 2,
      enabled: false,
    });
    await waitFor(() => expect(toggle).not.toBeChecked());
  });

  it("When the user types a summary and presses Enter, Then it is created in this owner as the user and the box clears", async () => {
    app.MemoryCreate.mockResolvedValue({
      item: item(20, "new", GLOBAL),
      existed: false,
    });
    renderPage();
    const box = await screen.findByRole("textbox", {
      name: "Add a memory, press Enter to save",
    });
    await userEvent.type(box, "Use pnpm, never npm{Enter}");
    expect(app.MemoryCreate).toHaveBeenCalledWith({
      owner: GLOBAL,
      summary: "Use pnpm, never npm",
      detail: "",
    });
    await waitFor(() => expect(box).toHaveValue(""));
  });

  it("Given creating fails, Then the typed summary stays and the reason is shown under the box", async () => {
    app.MemoryCreate.mockRejectedValue(
      "agentre-code:21109 Memory content looks like a credential",
    );
    renderPage();
    const box = await screen.findByRole("textbox", {
      name: "Add a memory, press Enter to save",
    });
    await userEvent.type(box, "token sk-xxx{Enter}");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Not added: Memory content looks like a credential",
    );
    expect(box).toHaveValue("token sk-xxx");
  });

  it("Given an inline edit whose save fails, Then the input keeps the typed text and says why", async () => {
    app.MemoryListByOwner.mockResolvedValue(
      list(GLOBAL, { carried: [item(1, "Run lint before pushing", GLOBAL)] }),
    );
    app.MemoryUpdate.mockRejectedValue(new Error("network down"));
    renderPage();
    await userEvent.click(
      await screen.findByRole("button", {
        name: "Edit Run lint before pushing",
      }),
    );
    const summary = screen.getByRole("textbox", { name: "Summary" });
    await userEvent.clear(summary);
    await userEvent.type(summary, "Run lint and tests");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(app.MemoryUpdate).toHaveBeenCalledWith({
      id: 1,
      summary: "Run lint and tests",
      detail: "",
    });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Not saved. Your text is still here; try again.",
    );
    expect(summary).toHaveValue("Run lint and tests");
  });

  it("When Delete is confirmed by a second click, Then the entry is deleted once and the list reloads", async () => {
    app.MemoryListByOwner.mockResolvedValueOnce(
      list(GLOBAL, { carried: [item(1, "Run lint before pushing", GLOBAL)] }),
    );
    app.MemoryDelete.mockResolvedValue(undefined);
    renderPage();
    const row = await screen.findByTestId("memory-row-1");
    await userEvent.click(
      within(row).getByRole("button", { name: "More actions" }),
    );
    await userEvent.click(
      await screen.findByRole("menuitem", { name: "Delete" }),
    );
    expect(app.MemoryDelete).not.toHaveBeenCalled();
    await userEvent.click(
      await screen.findByRole("menuitem", { name: "Confirm delete?" }),
    );
    expect(app.MemoryDelete).toHaveBeenCalledTimes(1);
    expect(app.MemoryDelete).toHaveBeenCalledWith(1);
    await waitFor(() =>
      expect(screen.queryByTestId("memory-row-1")).toBeNull(),
    );
  });

  it("When Move to… another layer is picked, Then the entry moves to that owner", async () => {
    app.MemoryListByOwner.mockResolvedValue(
      list(GLOBAL, { carried: [item(1, "Run lint before pushing", GLOBAL)] }),
    );
    app.MemoryMove.mockResolvedValue(
      item(1, "Run lint before pushing", AGENT_EVA),
    );
    renderPage();
    const row = await screen.findByTestId("memory-row-1");
    await userEvent.click(
      within(row).getByRole("button", { name: "More actions" }),
    );
    await userEvent.click(
      await screen.findByRole("menuitem", { name: "Move to…" }),
    );
    await userEvent.click(await screen.findByRole("menuitem", { name: "Eva" }));
    expect(app.MemoryMove).toHaveBeenCalledWith(1, AGENT_EVA);
  });

  it("Given an AI-written entry, When its session link is clicked, Then that session opens in chat", async () => {
    app.MemoryListByOwner.mockResolvedValue(
      list(AGENT_EVA, {
        carried: [
          item(1, "Logs mix with agent output", AGENT_EVA, {
            creator: { kind: "ai", agentId: 2, sessionId: 44 },
          }),
        ],
      }),
    );
    renderPage("/memory?owner=agent:2");
    const row = await screen.findByTestId("memory-row-1");
    expect(row).toHaveTextContent("From session");
    await userEvent.click(
      within(row).getByRole("button", { name: "Eva's session" }),
    );
    expect(screen.getByText("chat-page")).toBeInTheDocument();
    const { tabs, activeTabId } = useChatTabsStore.getState();
    expect(tabs.find((tab) => tab.id === activeTabId)?.meta).toEqual({
      kind: "session",
      sessionId: 44,
    });
  });
});

describe("Memory page · states", () => {
  it("Given the owner's memories are still loading, Then a loading placeholder is shown", async () => {
    app.MemoryListByOwner.mockReturnValue(new Promise(() => {}));
    renderPage();
    const main = await screen.findByRole("main");
    expect(
      await within(main).findByRole("status", { name: "Loading memories" }),
    ).toBeInTheDocument();
  });

  it("Given an owner with no memories, Then the empty state says so", async () => {
    renderPage();
    expect(await screen.findByText("No memories yet")).toBeInTheDocument();
  });

  it("Given the owner's memories fail to load, Then it says so and Retry loads again", async () => {
    app.MemoryListByOwner.mockRejectedValueOnce(new Error("db closed"));
    renderPage();
    expect(
      await screen.findByText("Memories didn't load."),
    ).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("No memories yet")).toBeInTheDocument();
  });

  it("Given the owner directory fails to load, Then the page shows a page-level failure with Retry", async () => {
    app.LoadOrg.mockRejectedValueOnce(new Error("db closed"));
    renderPage();
    expect(
      await screen.findByText("The memory page didn't load."),
    ).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(
      await screen.findByRole("navigation", { name: "Memory owners" }),
    ).toBeInTheDocument();
  });
});

describe("Memory page · changes from other devices", () => {
  it("Given a row being edited, When sync:applied arrives with new data, Then other rows update but the draft is kept", async () => {
    app.MemoryListByOwner.mockResolvedValue(
      list(GLOBAL, {
        carried: [
          item(1, "Run lint before pushing", GLOBAL),
          item(2, "Use pnpm", GLOBAL),
        ],
      }),
    );
    renderPage();
    await userEvent.click(
      await screen.findByRole("button", {
        name: "Edit Run lint before pushing",
      }),
    );
    const summary = screen.getByRole("textbox", { name: "Summary" });
    await userEvent.clear(summary);
    await userEvent.type(summary, "my local edit");

    app.MemoryListByOwner.mockResolvedValue(
      list(GLOBAL, {
        carried: [
          item(1, "changed on another device", GLOBAL),
          item(2, "Use pnpm, never npm", GLOBAL),
        ],
      }),
    );
    app.MemoryCountsByOwner.mockResolvedValue([{ owner: GLOBAL, count: 2 }]);
    emit("sync:applied");

    expect(
      await screen.findByRole("button", { name: "Edit Use pnpm, never npm" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Summary" })).toHaveValue(
      "my local edit",
    );
    await waitFor(() =>
      expect(
        within(sidebar()).getByRole("button", { name: /^Global/ }),
      ).toHaveTextContent("2"),
    );
  });
});

describe("Memory page · recent changes", () => {
  it("Given recent changes, Then they list who did what and offer restore previous / restore", async () => {
    app.MemoryRecentChanges.mockResolvedValue([
      item(1, "Prod DB is older than dev", AGENT_EVA, {
        hasPrevious: true,
        lastChange: { action: "edited", actor: USER, at: NOW },
      }),
      item(2, "Icons use line style", GLOBAL, {
        state: "archived",
        lastChange: {
          action: "archived",
          actor: { kind: "ai", agentId: 2, sessionId: 9 },
          at: NOW - 1000,
        },
      }),
    ]);
    app.MemoryRestorePrevious.mockResolvedValue(item(1, "x", AGENT_EVA));
    app.MemoryUnarchive.mockResolvedValue(item(2, "x", GLOBAL));
    renderPage("/memory?view=recent");

    const main = await screen.findByRole("main");
    expect(
      await within(main).findByRole("heading", {
        level: 1,
        name: "Recent changes",
      }),
    ).toBeInTheDocument();
    const first = await within(main).findByTestId("memory-change-1");
    expect(first).toHaveTextContent("You edited");
    expect(first).toHaveTextContent("Eva");
    const second = within(main).getByTestId("memory-change-2");
    expect(second).toHaveTextContent("Eva archived");
    expect(second).toHaveTextContent("Global");

    await userEvent.click(
      within(first).getByRole("button", { name: "Restore previous version" }),
    );
    expect(app.MemoryRestorePrevious).toHaveBeenCalledWith(1);
    await userEvent.click(
      within(second).getByRole("button", { name: "Restore" }),
    );
    expect(app.MemoryUnarchive).toHaveBeenCalledWith(2);
  });
});
