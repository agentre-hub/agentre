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

// 嵌在主 checkout 里的 worktree（<repo>/.claude/worktrees/feat-x）：路径字符串落在主根
// 之内，被移除后它的文件也不能当主根的普通行列出来。
const NESTED = `${CWD}/.claude/worktrees/feat-x`;

const nestedMessages: Msg[] = [
  { id: 1, role: "user", blocks: [{ type: "text", text: "go" }] },
  {
    id: 2,
    role: "assistant",
    blocks: [editBlock(`${NESTED}/src/b.ts`)],
  },
] as unknown as Msg[];

function nestedRoots(withWorktree: boolean) {
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
      path: NESTED,
      name: "feat-x",
      isPrimary: false,
      isWorktree: true,
      aiHere: true,
    },
  ];
}

/**
 * 页面重新加载：内存里的 store 状态全丢，本机存储原样留着。先给存储拍快照，清空内存
 * 状态（store 若有持久化，这一步也会写存储），再放回快照；store 若有持久化层就像刚启动
 * 一样从存储读回。
 */
async function reloadSessionRootStore() {
  const saved: [string, string][] = [];
  for (let i = 0; i < localStorage.length; i++) {
    const k = localStorage.key(i);
    if (k !== null) saved.push([k, localStorage.getItem(k) ?? ""]);
  }
  useSessionRootStore.setState({ bySession: {} });
  localStorage.clear();
  for (const [k, v] of saved) localStorage.setItem(k, v);
  const persisted = useSessionRootStore as unknown as {
    persist?: { rehydrate: () => Promise<void> | void };
  };
  await persisted.persist?.rehydrate();
}

/** 后端在集合之后带回的已移除的 feat-x（认领行记着 removed_at）。 */
const removedNested = { ...nestedRoots(true)[1], aiHere: false, removed: true };

function renderNested() {
  return render(
    <ChatContextToolWindows
      sessionId={7}
      messages={nestedMessages}
      activeMessageId={null}
      onJumpToMessage={() => {}}
      cwd={CWD}
      remote={false}
    />,
  );
}

describe("变更 · 被移除的嵌套 worktree", () => {
  // Given AI 在嵌套 worktree feat-x 里写了 src/b.ts, When 同一页上 feat-x 被移除,
  // Then b.ts 落在「feat-x（已移除）」分组里，主根没有一行 .claude/worktrees/... 。
  it("groups the nested worktree's files as removed while the page stays open", async () => {
    workRootsMock.mockResolvedValue(nestedRoots(true));
    renderNested();
    await waitFor(() =>
      expect(screen.getAllByTestId("change-row")).toHaveLength(1),
    );

    act(() =>
      runtime.handlers.get("chat:workroots:7")!({
        sessionId: 7,
        roots: nestedRoots(false),
      }),
    );

    const group = await screen.findByTestId("removed-root-group");
    expect(group).toHaveTextContent("feat-x (removed)");
    expect(
      within(group)
        .getAllByTestId("change-row")
        .map((r) => r.getAttribute("data-path")),
    ).toEqual(["src/b.ts"]);
    expect(
      screen
        .getAllByTestId("change-row")
        .map((r) => r.getAttribute("data-path")),
    ).toEqual(["src/b.ts"]);
  });

  // Given feat-x 在这一页上被移除过, When 页面重新加载、后端集合里已没有 feat-x、在集合
  // 之后报告它已移除, Then「feat-x（已移除）」分组仍在，b.ts 不作为主根下
  // .claude/worktrees/feat-x/src 的行。
  it("keeps the removed group across a reload instead of listing the file under the main root", async () => {
    workRootsMock.mockResolvedValue(nestedRoots(true));
    const first = renderNested();
    await waitFor(() =>
      expect(screen.getAllByTestId("change-row")).toHaveLength(1),
    );
    act(() =>
      runtime.handlers.get("chat:workroots:7")!({
        sessionId: 7,
        roots: nestedRoots(false),
      }),
    );
    await screen.findByTestId("removed-root-group");
    first.unmount();

    await act(reloadSessionRootStore);
    workRootsMock.mockResolvedValue([...nestedRoots(false), removedNested]);
    renderNested();

    const group = await screen.findByTestId("removed-root-group");
    expect(group).toHaveTextContent("feat-x (removed)");
    expect(
      screen
        .getAllByTestId("change-row")
        .map((r) => r.getAttribute("data-path")),
    ).toEqual(["src/b.ts"]);
    expect(
      screen.queryByText(".claude/worktrees/feat-x/src"),
    ).not.toBeInTheDocument();
  });

  // Given 会话没开着时 feat-x 被移除（这一页从没见过它）, When 打开会话、后端取数在集合
  // 之后带回 removed=true 的 feat-x, Then 同样以「feat-x（已移除）」分组显示，主根没有
  // .claude/worktrees/... 的行，也不提示移除（这一页没有从它切走）。
  it("groups a nested worktree removed while the session was not open, from the backend's report", async () => {
    workRootsMock.mockResolvedValue([...nestedRoots(false), removedNested]);
    renderNested();

    const group = await screen.findByTestId("removed-root-group");
    expect(group).toHaveTextContent("feat-x (removed)");
    expect(
      screen
        .getAllByTestId("change-row")
        .map((r) => r.getAttribute("data-path")),
    ).toEqual(["src/b.ts"]);
    expect(useSessionRootStore.getState().bySession[7].roots).toHaveLength(1);
    expect(useSessionRootStore.getState().bySession[7].notice).toBeNull();
  });

  // Given 这一页上 feat-x 被移除过, When 页面重新加载、后端既不在集合里也不再报告它已移除
  // （会话的认领行已不在：例如认领回来后又被删了会话目录里的记录）, Then 不凭本机残留画
  // 「已移除」分组 —— 已移除清单只以后端的认领行为准。
  it("does not draw a removed group from leftover local state the backend no longer reports", async () => {
    workRootsMock.mockResolvedValue(nestedRoots(true));
    const first = renderNested();
    await waitFor(() =>
      expect(screen.getAllByTestId("change-row")).toHaveLength(1),
    );
    act(() =>
      runtime.handlers.get("chat:workroots:7")!({
        sessionId: 7,
        roots: nestedRoots(false),
      }),
    );
    await screen.findByTestId("removed-root-group");
    first.unmount();

    await act(reloadSessionRootStore);
    workRootsMock.mockResolvedValue(nestedRoots(false));
    renderNested();

    await waitFor(() =>
      expect(useSessionRootStore.getState().bySession[7]?.loaded).toBe(true),
    );
    expect(screen.queryByTestId("removed-root-group")).toBeNull();
  });
});
