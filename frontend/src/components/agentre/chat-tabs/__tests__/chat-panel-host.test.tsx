import { act, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";

import { ChatPanelHost } from "../chat-panel-host";
import { useChatAgentsStore } from "@/stores/chat-agents-store";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useSessionIndexStore } from "@/stores/session-index-store";

const chatPanelRenderCounts = vi.hoisted(() => new Map<number, number>());

// TerminalPanel 已搬进共享包;这里只替换那一个导出,其余(pruneChatPanelScrollState
// 等)保持真实实现。
vi.mock("@agentre-ai/agentre-ui", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@agentre-ai/agentre-ui")>()),
  TerminalPanel: ({
    terminalID,
    active,
  }: {
    terminalID: string;
    active: boolean;
  }) => (
    <div data-testid="terminal-panel" data-terminal-id={terminalID}>
      terminal {terminalID} {active ? "active" : "inactive"}
    </div>
  ),
}));

// 把 onSidebarShouldReload 通过 data-attribute 暴露到 DOM 上, 这样回归测试可以
// 拿到这个回调并断言它真的去触发 store.reload (修复「新建会话不进左栏」的关键路径)。
type ChatPanelStub = {
  sessionId: number;
  newSessionAgent?: { id: number; name: string } | null;
  onSidebarShouldReload?: () => void;
  scrollStateKey?: string;
};
vi.mock("../../chat-panel", () => ({
  ChatPanel: ({
    sessionId,
    newSessionAgent,
    onSidebarShouldReload,
    scrollStateKey,
  }: ChatPanelStub) => {
    chatPanelRenderCounts.set(
      sessionId,
      (chatPanelRenderCounts.get(sessionId) ?? 0) + 1,
    );
    return (
      <div
        data-testid={`chat-panel-${sessionId}`}
        data-agent-id={newSessionAgent?.id ?? ""}
        data-scroll-state-key={scrollStateKey ?? ""}
      >
        <button
          type="button"
          data-testid={`fire-reload-${sessionId}`}
          onClick={() => onSidebarShouldReload?.()}
        >
          fire
        </button>
        {newSessionAgent ? <span>new agent {newSessionAgent.name}</span> : null}
        {newSessionAgent ? (
          <div
            role="textbox"
            data-testid="composer-editor"
            contentEditable
            suppressContentEditableWarning
            tabIndex={0}
          >
            editor
          </div>
        ) : null}
        session {sessionId}
      </div>
    );
  },
  pruneChatPanelScrollState: vi.fn(),
}));

describe("ChatPanelHost", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    chatPanelRenderCounts.clear();
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    useChatAgentsStore.getState().__reset();
    useSessionIndexStore.getState().__reset();
  });

  it("空 tab 有可对话 Agent 时显示统一空态 hero", () => {
    // 空态两档化之后 (spec §7): 没有可对话 Agent 时显示两步配置引导, 只有
    // 存在可对话 Agent 时才保留原占位 —— 所以这里要 seed 一个可对话 Agent。
    // 空态组件会主动补一次 agents reload; 这里避免触发真实 wails 调用。
    useChatAgentsStore.setState({
      agents: [
        {
          id: 7,
          name: "Eng",
          avatarColor: "agent-1",
          backendType: "builtin",
          chattable: true,
          pinned: false,
          sessions: [],
          attentionSessions: [],
          sessionIds: [],
        },
      ] as never,
      loading: false,
      error: null,
    });
    // 必须在 setState 之后 spy 并在此测试内 restore —— setState 会替换 store
    // 的 state 对象, 若先 spy 再 setState, mockRestore 只还原旧对象, reload 的
    // mock 会泄漏到下一个测试。
    const reloadSpy = vi
      .spyOn(useChatAgentsStore.getState(), "reload")
      .mockResolvedValue();
    render(
      <MemoryRouter initialEntries={["/chat"]}>
        <ChatPanelHost />
      </MemoryRouter>,
    );
    expect(
      screen.getByText(/Choose an Agent or project session to start/),
    ).toBeInTheDocument();
    reloadSpy.mockRestore();
  });

  it("每个 kind:'session' tab 渲染一个 ChatPanel mount", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    render(<ChatPanelHost />);
    expect(screen.getByTestId("chat-panel-1")).toBeInTheDocument();
    expect(screen.getByTestId("chat-panel-2")).toBeInTheDocument();
  });

  it("passes the tab id as ChatPanel scrollStateKey", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    const tabId = useChatTabsStore.getState().tabs[0].id;

    render(<ChatPanelHost />);

    expect(screen.getByTestId("chat-panel-1")).toHaveAttribute(
      "data-scroll-state-key",
      tabId,
    );
  });

  it("Given three mounted session tabs, When active tab changes, Then unrelated hidden panels do not rerender", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    useChatTabsStore.getState().openSessionInNewTab(3);
    const secondId = useChatTabsStore.getState().tabs[1].id;
    render(<ChatPanelHost />);

    const unrelatedBefore = chatPanelRenderCounts.get(1) ?? 0;
    const oldActiveBefore = chatPanelRenderCounts.get(3) ?? 0;
    const newActiveBefore = chatPanelRenderCounts.get(2) ?? 0;

    act(() => {
      useChatTabsStore.getState().setActive(secondId);
    });

    expect(chatPanelRenderCounts.get(1)).toBe(unrelatedBefore);
    expect(chatPanelRenderCounts.get(2)).toBeGreaterThan(newActiveBefore);
    expect(chatPanelRenderCounts.get(3)).toBeGreaterThan(oldActiveBefore);
  });

  it("Given an inactive session tab is mounted, When ChatPanelHost renders, Then it stays out of interaction without display:none", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    const firstId = useChatTabsStore.getState().tabs[0].id;
    useChatTabsStore.getState().setActive(firstId);
    render(<ChatPanelHost />);
    const wrap2 = screen.getByTestId("chat-panel-2").parentElement!;
    expect(wrap2).not.toHaveStyle({ display: "none" });
    expect(wrap2).toHaveAttribute("aria-hidden", "true");
    expect(wrap2).toHaveClass("pointer-events-none");
  });

  it("Given terminal tabs, When the active tab changes, Then TerminalPanel receives active for focus management", () => {
    useChatTabsStore.getState().openTerminal(7, "", undefined);
    useChatTabsStore.getState().openTerminal(8, "", undefined);
    const firstId = useChatTabsStore.getState().tabs[0].id;

    const { rerender } = render(<ChatPanelHost />);
    const panels = screen.getAllByTestId("terminal-panel");
    expect(panels[0]).toHaveTextContent("inactive");
    expect(panels[1]).toHaveTextContent("active");

    act(() => {
      useChatTabsStore.getState().setActive(firstId);
    });
    rerender(<ChatPanelHost />);

    const nextPanels = screen.getAllByTestId("terminal-panel");
    expect(nextPanels[0]).toHaveTextContent("active");
    expect(nextPanels[1]).toHaveTextContent("inactive");
  });

  it("kind:'new' tab 从 chat-agents-store 取 agent 并渲染新会话面板", () => {
    useChatAgentsStore.setState({
      agents: [
        {
          id: 7,
          name: "Eng",
          avatarColor: "agent-1",
          backendType: "builtin",
          chattable: true,
          pinned: false,
          sessions: [],
          attentionSessions: [],
          sessionIds: [],
        },
      ] as never,
      loading: false,
      error: null,
    });
    useChatTabsStore.getState().openNewSession(0, 7, "");

    render(<ChatPanelHost />);

    const panel = screen.getByTestId("chat-panel-0");
    expect(panel).toHaveAttribute("data-agent-id", "7");
    expect(screen.getByText("new agent Eng")).toBeInTheDocument();
  });

  it("kind:'new' tab 暂时找不到 agent 时刷新 chat-agents-store 且不渲染空白", () => {
    const reload = vi
      .spyOn(useChatAgentsStore.getState(), "reload")
      .mockResolvedValue();
    useChatAgentsStore.setState({ agents: [], loading: true, error: null });
    useChatTabsStore.getState().openNewSession(0, 99, "");

    render(<ChatPanelHost />);

    expect(screen.getByText("Loading Agent info...")).toBeInTheDocument();
    expect(screen.getByText("Agent #99")).toBeInTheDocument();
    expect(screen.queryByTestId("chat-panel-0")).not.toBeInTheDocument();
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("Given a project new-session tab waits for agent data, When the agent resolves, Then focus lands on the composer editor", async () => {
    vi.spyOn(useChatAgentsStore.getState(), "reload").mockResolvedValue();
    useChatAgentsStore.setState({ agents: [], loading: true, error: null });
    useChatTabsStore.getState().openNewSession(11, 99, "");

    render(<ChatPanelHost />);
    expect(screen.getByText("Loading Agent info...")).toBeInTheDocument();

    act(() => {
      useChatAgentsStore.setState({
        agents: [
          {
            id: 99,
            name: "Project Agent",
            avatarColor: "agent-1",
            backendType: "builtin",
            chattable: true,
            pinned: false,
            sessions: [],
            attentionSessions: [],
            sessionIds: [],
          },
        ] as never,
        loading: false,
        error: null,
      });
    });

    const editor = await screen.findByTestId("composer-editor");
    await waitFor(() => {
      expect(editor).toHaveFocus();
    });
  });

  it("ChatPanel 触发 onSidebarShouldReload 同步刷新 chat-agents + 索引已加载的 scope (修复新建会话不进左栏)", () => {
    const chatReload = vi
      .spyOn(useChatAgentsStore.getState(), "reload")
      .mockResolvedValue();
    const indexReload = vi
      .spyOn(useSessionIndexStore.getState(), "reloadLoaded")
      .mockResolvedValue();
    useChatTabsStore.getState().openSessionInNewTab(42);
    render(<ChatPanelHost />);
    const chatCallsBeforeClick = chatReload.mock.calls.length;
    const indexCallsBeforeClick = indexReload.mock.calls.length;
    screen.getByTestId("fire-reload-42").click();
    // 两条来源都该被一次性触发 —— 新建会话 / turn 落定后左栏立刻能看到变化，
    // 不必等下一次 mount。
    expect(chatReload).toHaveBeenCalledTimes(chatCallsBeforeClick + 1);
    expect(indexReload).toHaveBeenCalledTimes(indexCallsBeforeClick + 1);
    chatReload.mockRestore();
    indexReload.mockRestore();
  });

  it("Given a terminal tab is open, When ChatPanelHost renders, Then it shows terminal-panel not a ChatPanel", () => {
    useChatTabsStore.getState().openTerminal(7, "", undefined);
    render(<ChatPanelHost />);
    expect(screen.getByTestId("terminal-panel")).toBeInTheDocument();
    expect(screen.queryByTestId("chat-panel-0")).not.toBeInTheDocument();
  });

  it("Given the active tab is scrolled, When tabs are reordered, Then the mounted panel keeps its DOM slot", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    useChatTabsStore.getState().openSessionInNewTab(3);
    render(<ChatPanelHost />);

    const activePanel = screen.getByTestId("chat-panel-3").parentElement!;
    const host = activePanel.parentElement!;
    const originalIndex = Array.from(host.children).indexOf(activePanel);
    activePanel.scrollTop = 123;

    act(() => {
      useChatTabsStore.getState().moveTab(2, 0);
    });

    expect(Array.from(host.children).indexOf(activePanel)).toBe(originalIndex);
    expect(activePanel.scrollTop).toBe(123);
  });
});
