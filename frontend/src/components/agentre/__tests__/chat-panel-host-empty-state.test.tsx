import { act, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ChatPanelHost } from "../chat-tabs/chat-panel-host";
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

describe("ChatPanelHost empty chat state — setup guidance (task 5)", () => {
  beforeEach(() => {
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    useChatAgentsStore.getState().__reset();
    vi.spyOn(useChatAgentsStore.getState(), "reload").mockResolvedValue();
  });

  it("1B: no chattable Agent shows the two-step setup guide with both action buttons and the note", () => {
    seedAgents([
      { id: 1, name: "CEO", chattable: false, blockReason: "no-backend" },
    ]);
    renderHost();

    expect(
      screen.getByText("Before you start, complete two setup steps"),
    ).toBeInTheDocument();
    expect(screen.getByText("Configure Agent backend")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Go to settings → Agent backend" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Configure LLM provider")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Go to settings → LLM provider" }),
    ).toBeInTheDocument();
    // 保留快捷键提示 (文本节点与 kbd 混排, 用正则匹配)
    expect(screen.getByText(/Close Tab/)).toBeInTheDocument();
    expect(screen.getByText(/Switch Tab/)).toBeInTheDocument();
  });

  it("does not show setup guidance before the agent snapshot has loaded", () => {
    renderHost();

    expect(
      screen.queryByText("Before you start, complete two setup steps"),
    ).not.toBeInTheDocument();
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

    expect(
      screen.queryByText("Before you start, complete two setup steps"),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText("Choose an Agent or project session to start"),
    ).toBeInTheDocument();
  });

  it("1B: no Agents at all also shows the two-step setup guide", () => {
    seedAgents([]);
    renderHost();

    expect(
      screen.getByText("Before you start, complete two setup steps"),
    ).toBeInTheDocument();
  });

  it("1B: the backend step button navigates to /settings on the agent-backend page", () => {
    seedAgents([{ id: 1, name: "CEO", chattable: false }]);
    renderHost();

    act(() => {
      fireEvent.click(
        screen.getByRole("button", { name: "Go to settings → Agent backend" }),
      );
    });

    expect(screen.getByTestId("location")).toHaveTextContent(
      "/settings|agent-backend",
    );
  });

  it("1B: the provider step button navigates to /settings on the llm-providers page", () => {
    seedAgents([{ id: 1, name: "CEO", chattable: false }]);
    renderHost();

    act(() => {
      fireEvent.click(
        screen.getByRole("button", { name: "Go to settings → LLM provider" }),
      );
    });

    expect(screen.getByTestId("location")).toHaveTextContent(
      "/settings|llm-providers",
    );
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
      screen.queryByText(/complete two setup steps/i),
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
