import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ChatPanelHost } from "../chat-tabs/chat-panel-host";
import { ShortcutsProvider } from "../shortcuts/shortcuts-provider";
import { useChatAgentsStore } from "@/stores/chat-agents-store";
import { useChatTabsStore } from "@/stores/chat-tabs-store";

vi.mock("../chat-panel", () => ({
  ChatPanel: () => <div data-testid="chat-panel" />,
  pruneChatPanelScrollState: vi.fn(),
}));

// TerminalPanel 已搬进共享包;只替换那一个导出,其余保持真实实现。
vi.mock("@agentre-hub/agentre-ui", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@agentre-hub/agentre-ui")>()),
  TerminalPanel: () => <div data-testid="terminal-panel" />,
}));

function LocationProbe() {
  const location = useLocation();
  return (
    <output data-testid="location">
      {location.pathname}|
      {String(
        (location.state as { settingsPage?: string } | null)?.settingsPage ??
          "",
      )}
    </output>
  );
}

type SeedAgent = {
  id: number;
  name: string;
  chattable: boolean;
  blockReason?: string;
};

function seedAgents(agents: SeedAgent[]) {
  useChatAgentsStore.setState({
    agents: agents.map((a) => ({
      id: a.id,
      name: a.name,
      avatarColor: "agent-1",
      backendType: "builtin",
      chattable: a.chattable,
      blockReason: a.blockReason ?? "",
      pinned: false,
      sessions: [],
      attentionSessions: [],
      sessionIds: [],
    })) as never,
    loading: false,
    error: null,
  });
}

function renderHost() {
  return render(
    <MemoryRouter initialEntries={["/chat"]}>
      <LocationProbe />
      <ChatPanelHost />
    </MemoryRouter>,
  );
}

/**
 * 标题里的步数必须与清单实际行数一致。
 * 这条守卫是有来历的：标题原写死「Two steps…」，清单从两行加到三行后标题没跟着改，
 * 界面上就成了「两步」配三行（真实缺陷）。改成从 steps 数组算之后，这里同时钉住
 * 两件事：文案里的数字、以及它跟渲染出来的行数对得上。
 */
function expectSetupTitleToMatchRows() {
  const rows = screen.getAllByTestId(/^setup-step-/);
  const title = screen.getByText(
    new RegExp(`^${rows.length} steps? to your first conversation$`),
  );
  expect(title).toBeInTheDocument();
}

describe("ChatPanelHost empty chat state — setup checklist (task 5)", () => {
  beforeEach(() => {
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    useChatAgentsStore.getState().__reset();
    vi.spyOn(useChatAgentsStore.getState(), "reload").mockResolvedValue();
  });

  it("1B: with no backend configured, the checklist marks the backend step current and the rest waiting", () => {
    seedAgents([
      { id: 1, name: "CEO", chattable: false, blockReason: "no-backend" },
    ]);
    renderHost();

    expectSetupTitleToMatchRows();

    const backend = screen.getByTestId("setup-step-backend");
    expect(backend).toHaveAttribute("data-status", "current");
    expect(
      within(backend).getByRole("button", { name: "Configure" }),
    ).toBeInTheDocument();

    const provider = screen.getByTestId("setup-step-provider");
    expect(provider).toHaveAttribute("data-status", "waiting");
    expect(
      within(provider).getByRole("button", { name: "Waiting" }),
    ).toBeDisabled();

    const start = screen.getByTestId("setup-step-start");
    expect(start).toHaveAttribute("data-status", "waiting");
    expect(
      within(start).getByRole("button", { name: "Waiting" }),
    ).toBeDisabled();

    // 保留快捷键提示 (文本节点与 kbd 混排, 用正则匹配)
    expect(screen.getByText(/Close Tab/)).toBeInTheDocument();
    expect(screen.getByText(/Switch Tab/)).toBeInTheDocument();
  });

  it("1B: with a backend but no provider, the backend step is done and the provider step becomes the only primary action", () => {
    seedAgents([
      {
        id: 1,
        name: "CEO",
        chattable: false,
        blockReason: "backend-requires-provider",
      },
    ]);
    renderHost();

    const backend = screen.getByTestId("setup-step-backend");
    expect(backend).toHaveAttribute("data-status", "done");
    expect(
      within(backend).getByRole("button", { name: "View" }),
    ).toBeInTheDocument();

    const provider = screen.getByTestId("setup-step-provider");
    expect(provider).toHaveAttribute("data-status", "current");
    expect(
      within(provider).getByRole("button", { name: "Configure" }),
    ).toBeInTheDocument();

    const start = screen.getByTestId("setup-step-start");
    expect(start).toHaveAttribute("data-status", "waiting");
    expect(
      within(start).getByRole("button", { name: "Waiting" }),
    ).toBeDisabled();
  });

  it("1B: shortcut hints follow the desktop platform", () => {
    seedAgents([]);

    render(
      <MemoryRouter initialEntries={["/chat"]}>
        <ShortcutsProvider platform="darwin">
          <ChatPanelHost />
        </ShortcutsProvider>
      </MemoryRouter>,
    );

    expect(screen.getByText("⌘1..⌘9")).toBeInTheDocument();
    expect(screen.getByText("⌘W")).toBeInTheDocument();
    expect(screen.getByText("⌘ Click")).toBeInTheDocument();
  });

  it("1B: shortcut hints use Ctrl on non-macOS platforms", () => {
    seedAgents([]);

    render(
      <MemoryRouter initialEntries={["/chat"]}>
        <ShortcutsProvider platform="windows">
          <ChatPanelHost />
        </ShortcutsProvider>
      </MemoryRouter>,
    );

    expect(screen.getByText("Ctrl+1..9")).toBeInTheDocument();
    expect(screen.getByText("Ctrl+W")).toBeInTheDocument();
    expect(screen.getByText("Ctrl+Click")).toBeInTheDocument();
  });

  it("does not show the checklist before the agent snapshot has loaded", () => {
    renderHost();

    expect(screen.queryByTestId("setup-step-backend")).not.toBeInTheDocument();
    expect(
      screen.getByText("Choose an Agent or project session to start"),
    ).toBeInTheDocument();
  });

  it("does not mislabel an agent-list load error as a setup gap", () => {
    useChatAgentsStore.setState({
      agents: [],
      loading: false,
      error: "ListChatAgents failed",
    });
    renderHost();

    expect(screen.queryByTestId("setup-step-backend")).not.toBeInTheDocument();
    expect(
      screen.getByText("Choose an Agent or project session to start"),
    ).toBeInTheDocument();
  });

  it("1B: no Agents at all also shows the checklist with the backend step current", () => {
    seedAgents([]);
    renderHost();

    expectSetupTitleToMatchRows();
    expect(screen.getByTestId("setup-step-backend")).toHaveAttribute(
      "data-status",
      "current",
    );
  });

  it("1B: the backend step action navigates to /settings on the agent-backend page", () => {
    seedAgents([
      { id: 1, name: "CEO", chattable: false, blockReason: "no-backend" },
    ]);
    renderHost();

    act(() => {
      fireEvent.click(
        within(screen.getByTestId("setup-step-backend")).getByRole("button", {
          name: "Configure",
        }),
      );
    });

    expect(screen.getByTestId("location")).toHaveTextContent(
      "/settings|agent-backend",
    );
  });

  it("1B: the provider step action navigates to /settings on the llm-providers page", () => {
    seedAgents([
      {
        id: 1,
        name: "CEO",
        chattable: false,
        blockReason: "backend-requires-provider",
      },
    ]);
    renderHost();

    act(() => {
      fireEvent.click(
        within(screen.getByTestId("setup-step-provider")).getByRole("button", {
          name: "Configure",
        }),
      );
    });

    expect(screen.getByTestId("location")).toHaveTextContent(
      "/settings|llm-providers",
    );
  });

  // 全部就绪 = 存在 chattable 的 agent。按 spec §7 这一档交给 1C 占位, 1B 清单
  // 自行退场 —— 因此第三行「发出第一轮对话」在 1B 里永远停在等待前置。
  it("1B hands off to the ready placeholder once a chattable Agent exists", () => {
    seedAgents([{ id: 1, name: "CEO", chattable: true }]);
    renderHost();

    expect(screen.queryByTestId("setup-step-backend")).not.toBeInTheDocument();
    expect(
      screen.getByText("Choose an Agent or project session to start"),
    ).toBeInTheDocument();
  });

  it("1C: keeps the current placeholder and adds the unconfigured row when some Agents cannot chat", () => {
    seedAgents([
      { id: 1, name: "CEO", chattable: false, blockReason: "no-backend" },
      { id: 2, name: "Eng", chattable: true },
    ]);
    renderHost();

    expect(
      screen.getByText("Choose an Agent or project session to start"),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/steps? to your first conversation/),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText("1 Agent(s) without a backend"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Go to org chart setup →" }),
    ).toBeInTheDocument();
  });

  it("1C: the org link navigates to /org", () => {
    seedAgents([
      { id: 1, name: "CEO", chattable: false, blockReason: "no-backend" },
      { id: 2, name: "Eng", chattable: true },
    ]);
    renderHost();

    act(() => {
      fireEvent.click(
        screen.getByRole("button", { name: "Go to org chart setup →" }),
      );
    });

    expect(screen.getByTestId("location")).toHaveTextContent("/org|");
  });

  it("1C: counts only Agents blocked by a missing backend", () => {
    seedAgents([
      { id: 1, name: "CEO", chattable: false, blockReason: "no-backend" },
      {
        id: 2,
        name: "Remote",
        chattable: false,
        blockReason: "gateway-not-running",
      },
      { id: 3, name: "Eng", chattable: true },
    ]);
    renderHost();

    expect(
      screen.getByText("1 Agent(s) without a backend"),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("2 Agent(s) without a backend"),
    ).not.toBeInTheDocument();
  });

  it("1C: no unconfigured row when every Agent can chat", () => {
    seedAgents([
      { id: 1, name: "CEO", chattable: true },
      { id: 2, name: "Eng", chattable: true },
    ]);
    renderHost();

    expect(screen.queryByText(/without a backend/i)).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Go to org chart setup →" }),
    ).not.toBeInTheDocument();
  });
});
