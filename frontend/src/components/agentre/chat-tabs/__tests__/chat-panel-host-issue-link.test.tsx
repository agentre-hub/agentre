import { act, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  IssueLinkSession: vi.fn(),
}));
vi.mock("../../../../../wailsjs/go/app/App", () => appMocks);
vi.mock("../../../../../wailsjs/runtime/runtime", async (importOriginal) => ({
  ...(await importOriginal<
    typeof import("../../../../../wailsjs/runtime/runtime")
  >()),
  EventsOn: vi.fn(() => () => {}),
}));

type StubProps = {
  sessionId: number;
  newSessionAgent?: { id: number } | null;
  newSessionSeed?: { text: string; issueId?: number };
  onSessionCreated?: (sessionId: number, agentId: number) => void;
};
vi.mock("../../chat-panel", async () => {
  const { usePanelShown } = await import("../../chat-panel/panel-shown");
  return {
    ChatPanel: function ChatPanelStub({
      sessionId,
      newSessionAgent,
      newSessionSeed,
      onSessionCreated,
    }: StubProps) {
      usePanelShown();
      return (
        <div
          data-testid={`chat-panel-${sessionId}`}
          data-seed-text={newSessionSeed?.text ?? ""}
        >
          {newSessionAgent ? (
            <button
              type="button"
              data-testid="first-send"
              onClick={() => onSessionCreated?.(77, newSessionAgent.id)}
            >
              send
            </button>
          ) : null}
        </div>
      );
    },
    pruneChatPanelScrollState: vi.fn(),
  };
});

import { MemoryRouter } from "react-router-dom";

import { useChatAgentsStore } from "@/stores/chat-agents-store";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useIssueLinksStore } from "@/stores/issue-links-store";

import { ChatPanelHost } from "../chat-panel-host";

const agent = {
  id: 7,
  name: "Eng",
  avatarColor: "agent-1",
  backendType: "builtin",
  chattable: true,
  pinned: false,
  sessions: [],
  attentionSessions: [],
  sessionIds: [],
};

function renderHost() {
  return render(
    <MemoryRouter initialEntries={["/chat"]}>
      <ChatPanelHost />
    </MemoryRouter>,
  );
}

describe("ChatPanelHost 看板任务「开始会话」的关联时机", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    appMocks.IssueLinkSession.mockResolvedValue(undefined);
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    useChatAgentsStore.getState().__reset();
    useChatAgentsStore.setState({
      agents: [agent] as never,
      loading: false,
      error: null,
    });
    vi.spyOn(useChatAgentsStore.getState(), "reload").mockResolvedValue();
  });

  it("Given a seeded new-session tab, When it opens, Then the seed reaches the panel and nothing is linked yet", () => {
    useChatTabsStore
      .getState()
      .openNewSession(4, 7, "", { text: "标题\n\n正文", issueId: 142 });
    renderHost();

    expect(screen.getByTestId("chat-panel-0")).toHaveAttribute(
      "data-seed-text",
      "标题\n\n正文",
    );
    expect(appMocks.IssueLinkSession).not.toHaveBeenCalled();
  });

  it("Given a seeded tab closed without sending, When it is closed, Then no link is created", () => {
    useChatTabsStore
      .getState()
      .openNewSession(4, 7, "", { text: "x", issueId: 142 });
    renderHost();

    act(() => {
      const id = useChatTabsStore.getState().tabs[0].id;
      useChatTabsStore.getState().closeTab(id);
    });

    expect(appMocks.IssueLinkSession).not.toHaveBeenCalled();
  });

  it("Given a seeded tab, When the first message creates the session, Then it is linked to the task and the board is told", async () => {
    useChatTabsStore
      .getState()
      .openNewSession(4, 7, "", { text: "x", issueId: 142 });
    renderHost();
    const before = useIssueLinksStore.getState().revision;

    await act(async () => {
      screen.getByTestId("first-send").click();
    });

    expect(appMocks.IssueLinkSession).toHaveBeenCalledExactlyOnceWith(77, 142);
    expect(useIssueLinksStore.getState().revision).toBe(before + 1);
  });

  it("Given a plain new-session tab, When the first message creates the session, Then nothing is linked", async () => {
    useChatTabsStore.getState().openNewSession(4, 7, "");
    renderHost();

    await act(async () => {
      screen.getByTestId("first-send").click();
    });

    expect(appMocks.IssueLinkSession).not.toHaveBeenCalled();
  });
});
