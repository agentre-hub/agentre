import "@testing-library/jest-dom/vitest";

import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

const runtime = vi.hoisted(() => ({
  handlers: new Map<string, (payload: unknown) => void>(),
}));
vi.mock("@/../wailsjs/runtime/runtime", async () => ({
  ...(await vi.importActual("@/../wailsjs/runtime/runtime")),
  EventsOn: vi.fn((name: string, cb: (payload: unknown) => void) => {
    runtime.handlers.set(name, cb);
    return () => runtime.handlers.delete(name);
  }),
}));

const workRootsMock = vi.fn();
vi.mock("@/../wailsjs/go/app/App", () => ({
  OpenPath: vi.fn(),
  RevealPath: vi.fn(),
  WorkspaceFsWorkRoots: (sessionId: number) => workRootsMock(sessionId),
  WorkspaceFsGitState: () =>
    Promise.resolve({
      branch: "main",
      worktree: "",
      dirty: 0,
      ahead: 0,
      behind: 0,
      hasUpstream: false,
      notARepo: false,
      commonDir: "",
    }),
  WorkspaceFsGitChanges: () =>
    Promise.resolve({
      notARepo: false,
      baseRef: "",
      truncated: false,
      changes: [],
    }),
  WorkspaceFsListDir: () =>
    Promise.resolve({ path: "", entries: [], truncated: false }),
}));

import { useChatSidebarStore } from "@/stores/chat-sidebar-store";
import {
  selectActivePreviewTab,
  useFilePreviewTabsStore,
} from "@/stores/file-preview-tabs-store";
import { useSessionRootStore } from "@/stores/session-root-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { ChatContextToolWindows } from "../index";

import { showOnly } from "./tool-window-helpers";

import type { chat_svc } from "../../../../../wailsjs/go/models";

const CWD = "/work/demo-app";
const WT = "/work/demo-app-dark-mode";

type Msg = chat_svc.ChatMessage;

function editBlock(path: string): unknown {
  return {
    type: "tool_use",
    toolName: "Edit",
    toolInput: { file_path: path },
    canonical: {
      kind: "file.edit",
      fileEdit: {
        files: [{ path, kind: "modified", hunks: [], plus: 2, minus: 1 }],
      },
    },
  };
}

const messages: Msg[] = [
  { id: 1, role: "user", blocks: [{ type: "text", text: "go" }] },
  {
    id: 2,
    role: "assistant",
    blocks: [
      editBlock(`${WT}/src/settings/theme.ts`),
      editBlock(`${WT}/src/hooks/useColorScheme.ts`),
    ],
  },
] as unknown as Msg[];

function roots(withWorktree: boolean) {
  const main = {
    path: CWD,
    name: "demo-app",
    isPrimary: true,
    isWorktree: false,
    aiHere: !withWorktree,
  };
  if (!withWorktree) return [main];
  return [
    main,
    {
      path: WT,
      name: "dark-mode",
      isPrimary: false,
      isWorktree: true,
      aiHere: true,
    },
  ];
}

beforeEach(() => {
  localStorage.clear();
  showOnly("changes");
  useChatSidebarStore.setState({ changesScope: "session", showIgnored: false });
  useFilePreviewTabsStore.setState({ previewTabsBySession: {} });
  useSessionRootStore.setState({ bySession: {} });
  useSessionStatusStore.getState().__reset();
  runtime.handlers.clear();
  workRootsMock.mockReset();
  workRootsMock.mockResolvedValue(roots(true));
});

describe("变更 · 被移除的 worktree", () => {
  // Given 会话在 worktree 里改过文件, When 那个 worktree 被移除, Then「本次会话」里它的
  // 文件以「<名称>（已移除）」分组划掉显示，点一行仍按那个根打开（对话记录里的 diff）。
  it("groups a removed root's session files struck through and still opens them", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(
      <ChatContextToolWindows
        sessionId={7}
        messages={messages}
        activeMessageId={null}
        onJumpToMessage={() => {}}
        cwd={CWD}
        remote={false}
      />,
    );
    // 跟随：AI 在 worktree，变更窗列的是 worktree 的两个文件。
    await waitFor(() =>
      expect(screen.getAllByTestId("change-row")).toHaveLength(2),
    );
    expect(screen.queryByTestId("removed-root-group")).toBeNull();

    act(() =>
      runtime.handlers.get("chat:workroots:7")!({
        sessionId: 7,
        roots: roots(false),
      }),
    );

    const group = await screen.findByTestId("removed-root-group");
    expect(group).toHaveTextContent("dark-mode (removed)");
    const rows = within(group).getAllByTestId("change-row");
    expect(rows.map((r) => r.getAttribute("data-path")).sort()).toEqual([
      "src/hooks/useColorScheme.ts",
      "src/settings/theme.ts",
    ]);
    const theme = rows.find(
      (r) => r.getAttribute("data-path") === "src/settings/theme.ts",
    )!;
    expect(within(theme).getByText("theme.ts")).toHaveClass("line-through");

    await user.click(within(theme).getByText("theme.ts"));
    expect(
      selectActivePreviewTab(useFilePreviewTabsStore.getState(), 7),
    ).toMatchObject({ root: WT, path: "src/settings/theme.ts" });
  });

  // Given 没有任何根被移除, Then 不渲染已移除分组。
  it("renders no removed group while every root is still claimed", async () => {
    render(
      <ChatContextToolWindows
        sessionId={7}
        messages={messages}
        activeMessageId={null}
        onJumpToMessage={() => {}}
        cwd={CWD}
        remote={false}
      />,
    );
    await waitFor(() =>
      expect(screen.getAllByTestId("change-row")).toHaveLength(2),
    );
    expect(screen.queryByTestId("removed-root-group")).toBeNull();
  });
});
