import "@testing-library/jest-dom/vitest";

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/../wailsjs/runtime/runtime", async () => ({
  ...(await vi.importActual("@/../wailsjs/runtime/runtime")),
  EventsOn: vi.fn(() => () => {}),
  EventsOff: vi.fn(),
}));

const api = vi.hoisted(() => ({
  ListAgentBackends: vi.fn(),
  IssueListBySession: vi.fn(),
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

import type { chat_svc } from "../../../../../wailsjs/go/models";
import { ChatContextToolWindows } from "../../chat-context-sidebar";
import {
  showOnly,
  stripButton,
} from "../../chat-context-sidebar/__tests__/tool-window-helpers";

const session = {
  id: 1,
  agentName: "Architect",
  agentColor: "agent-3",
  backendType: "claudecode",
  agentBackendId: 11,
  deviceID: "",
  createtime: 1_700_000_000_000,
  providerSessionId: "cli-xyz",
  carriedMemory: [],
} as unknown as chat_svc.ChatSessionDetail;

const msg = (
  id: number,
  role: string,
  over: Partial<chat_svc.ChatMessage> = {},
) =>
  ({
    id,
    role,
    blocks: [],
    promptTokens: 0,
    completionTokens: 0,
    cachedTokens: 0,
    ...over,
  }) as unknown as chat_svc.ChatMessage;

function renderWindows(onCopy = vi.fn()) {
  return render(
    <MemoryRouter>
      <ChatContextToolWindows
        sessionId={1}
        messages={[
          msg(1, "user"),
          msg(2, "assistant", { promptTokens: 100, completionTokens: 30 }),
          msg(3, "user"),
          msg(4, "assistant", { promptTokens: 200, completionTokens: 70 }),
        ]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
        cwd="/repo"
        sessionInfo={{
          session,
          permissionMode: "plan",
          modelLabel: "claude-opus-4",
          reasoningEffort: "",
          contextUsage: { used: 10, max: 100 },
          onCopyLaunchCommand: onCopy,
        }}
      />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  localStorage.clear();
  Object.values(api).forEach((f) => f.mockReset());
  api.ListAgentBackends.mockResolvedValue({
    items: [{ id: 11, name: "Mine" }],
  });
  api.IssueListBySession.mockResolvedValue([]);
  api.ListSessionDispatches.mockResolvedValue({
    dispatchedBy: [],
    dispatched: [],
  });
  api.ListSessionsSharingRoot.mockResolvedValue([]);
  showOnly();
  useChatTabsStore.setState({ tabs: [], activeTabId: null });
  useSessionStatusStore.getState().__reset();
  useSessionRootStore.setState({ bySession: {} });
});

describe("会话信息工具窗 · 接入", () => {
  // Given 对话页, Then 图标条有「会话信息」，点开后默认落在右下槽位，行数据来自消息与会话。
  it("registers the Session info window and derives turns and usage from messages", async () => {
    renderWindows();
    await userEvent.setup().click(stripButton("Session info"));

    const region = screen.getByRole("region", { name: "Session info" });
    const usage = within(region).getByTestId("session-info-row-usage");
    expect(usage).toHaveTextContent("Input 300 · Output 100 · Cache hits 0");
    expect(
      within(region).getByTestId("session-info-row-created"),
    ).toHaveTextContent("2 turns");
    expect(
      within(region).getByTestId("session-info-row-permission"),
    ).toHaveTextContent("Plan");
    expect(
      within(region).getByTestId("session-info-row-cliSession"),
    ).toHaveTextContent("cli-xyz");
    await waitFor(() =>
      expect(
        within(region).getByTestId("session-info-row-backend"),
      ).toHaveTextContent("Mine"),
    );
  });
});
