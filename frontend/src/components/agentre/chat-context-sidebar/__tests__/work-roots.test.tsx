import "@testing-library/jest-dom/vitest";

import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

const listDirMock = vi.fn();
const gitChangesMock = vi.fn();
const workRootsMock = vi.fn();
const gitStateMock = vi.fn();
const searchFilesMock = vi.fn();
const revealPathMock = vi.fn();
vi.mock("@/../wailsjs/go/app/App", () => ({
  OpenPath: vi.fn(),
  RevealPath: (p: string) => revealPathMock(p),
  WorkspaceFsListDir: (
    sessionId: number,
    root: string,
    relPath: string,
    ignored: boolean,
  ) => listDirMock(sessionId, root, relPath, ignored),
  WorkspaceFsGitChanges: (
    sessionId: number,
    root: string,
    scope: string,
    baseRef: string,
  ) => gitChangesMock(sessionId, root, scope, baseRef),
  WorkspaceFsSearchFiles: (
    sessionId: number,
    root: string,
    query: string,
    ignored: boolean,
  ) => searchFilesMock(sessionId, root, query, ignored),
  WorkspaceFsWorkRoots: (sessionId: number) => workRootsMock(sessionId),
  WorkspaceFsGitState: (sessionId: number, root: string) =>
    gitStateMock(sessionId, root),
}));

import { useChatSidebarStore } from "@/stores/chat-sidebar-store";
import { useFilePreviewTabsStore } from "@/stores/file-preview-tabs-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { ChatContextSidebar } from "../index";

import type { chat_svc } from "../../../../../wailsjs/go/models";

const CWD = "/Users/me/proj";
const WT = "/Users/me/proj-ia";

type Msg = chat_svc.ChatMessage;

function userMsg(id: number): Msg {
  return {
    id,
    role: "user",
    blocks: [{ type: "text", text: "do it" }],
  } as unknown as Msg;
}

function editBlock(path: string, plus = 3, minus = 1): unknown {
  return {
    type: "tool_use",
    toolName: "Edit",
    toolInput: { file_path: path },
    canonical: {
      kind: "file.edit",
      fileEdit: {
        files: [{ path, kind: "modified", hunks: [], plus, minus }],
      },
    },
  };
}

function assistantMsg(id: number, blocks: unknown[]): Msg {
  return { id, role: "assistant", blocks } as unknown as Msg;
}

/** 两个根都被改过：主仓库两个文件、worktree 一个文件（最后一次写入落在主仓库）。 */
const twoRootMessages: Msg[] = [
  userMsg(1),
  assistantMsg(2, [editBlock(`${WT}/internal/turn.go`)]),
  userMsg(3),
  assistantMsg(4, [editBlock(`${CWD}/a.go`), editBlock(`${CWD}/docs/b.md`)]),
];

/** 最后一次写入落在 worktree —— 自动跟随的触发条件。 */
const wroteInWorktree: Msg[] = [
  ...twoRootMessages,
  userMsg(5),
  assistantMsg(6, [editBlock(`${WT}/internal/dispatcher.go`)]),
];

function root(
  path: string,
  name: string,
  extra: { isWorktree?: boolean; isPrimary?: boolean } = {},
) {
  return {
    path,
    name,
    isWorktree: extra.isWorktree ?? false,
    isPrimary: extra.isPrimary ?? false,
  };
}

const TWO_ROOTS = [
  root(CWD, "proj", { isPrimary: true }),
  root(WT, "proj-ia", { isWorktree: true }),
];

function gitState(extra: Record<string, unknown> = {}) {
  return {
    notARepo: false,
    branch: "main",
    worktree: "",
    dirty: 0,
    ahead: 0,
    behind: 0,
    hasUpstream: true,
    commonDir: `${CWD}/.git`,
    ...extra,
  };
}

function changesView(paths: string[] = []) {
  return {
    notARepo: false,
    baseRef: "",
    truncated: false,
    changes: paths.map((path) => ({
      path,
      oldPath: "",
      status: "modified",
      added: 1,
      deleted: 0,
      binary: false,
    })),
  };
}

function listing(entries: string[] = []) {
  return {
    path: CWD,
    entries: entries.map((name) => ({
      name,
      isDir: false,
      size: 1,
      mtime: 0,
      symlink: false,
      gitIgnored: false,
    })),
    truncated: false,
  };
}

function renderSidebar(
  props: Partial<React.ComponentProps<typeof ChatContextSidebar>> = {},
) {
  return render(
    <ChatContextSidebar
      sessionId={7}
      messages={twoRootMessages}
      activeMessageId={null}
      onJumpToMessage={() => {}}
      cwd={CWD}
      remote={false}
      {...props}
    />,
  );
}

/** 根切换器的触发按钮。 */
function switcher(): HTMLElement {
  return screen.getByTestId("root-switcher");
}

async function openSwitcher(user: ReturnType<typeof userEvent.setup>) {
  await user.click(switcher());
  return await screen.findByRole("menu");
}

beforeEach(() => {
  localStorage.clear();
  useChatSidebarStore.setState({
    open: true,
    activeTab: "directory",
    changesScope: "session",
    showIgnored: false,
  });
  useFilePreviewTabsStore.setState({ previewTabsBySession: {} });
  useSessionStatusStore.getState().__reset();
  listDirMock.mockReset();
  listDirMock.mockResolvedValue(listing(["a.go"]));
  gitChangesMock.mockReset();
  gitChangesMock.mockResolvedValue(changesView([]));
  searchFilesMock.mockReset();
  searchFilesMock.mockResolvedValue({ hits: [], truncated: false });
  workRootsMock.mockReset();
  workRootsMock.mockResolvedValue(TWO_ROOTS);
  gitStateMock.mockReset();
  gitStateMock.mockResolvedValue(gitState());
  revealPathMock.mockReset();
  revealPathMock.mockResolvedValue(undefined);
  sonnerMocks.toast.error.mockReset();
});

describe("根切换器：只在工作根 ≥ 2 时存在", () => {
  it("Given a session with a single work root, When the sidebar renders, Then no root switcher chrome appears at all", async () => {
    workRootsMock.mockResolvedValue([root(CWD, "proj", { isPrimary: true })]);
    renderSidebar();

    await waitFor(() => expect(workRootsMock).toHaveBeenCalledWith(7));
    await screen.findByRole("tab", { name: /Directory/ });
    expect(screen.queryByTestId("root-switcher")).toBeNull();
  });

  it("Given two work roots, When the sidebar renders, Then the switcher sits above the tab bar and names the current root, whether it is the main repo, and its change count", async () => {
    renderSidebar();

    const trigger = await screen.findByTestId("root-switcher");
    expect(trigger).toHaveTextContent("proj");
    expect(trigger).toHaveTextContent("Main repo");
    // 主仓库下本会话改了 a.go 与 docs/b.md 两个文件。
    expect(trigger).toHaveTextContent("2");

    const tabBar = screen
      .getByRole("tab", { name: /Directory/ })
      .closest('[role="tablist"]');
    expect(tabBar).not.toBeNull();
    expect(
      trigger.compareDocumentPosition(tabBar!) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("Given the switcher is opened, When the menu lists every work root, Then each option carries its own change count and worktree marking", async () => {
    const user = userEvent.setup();
    renderSidebar();
    await screen.findByTestId("root-switcher");

    const menu = await openSwitcher(user);
    const options = within(menu).getAllByTestId("root-option");
    expect(options.map((el) => el.getAttribute("data-root"))).toEqual([
      CWD,
      WT,
    ]);
    expect(options[0]).toHaveTextContent("2");
    expect(options[1]).toHaveTextContent("proj-ia");
    expect(options[1]).toHaveTextContent("worktree");
    expect(options[1]).toHaveTextContent("1");
  });
});

describe("换根时在途的分支状态响应", () => {
  it("Given the previous root's git state is still in flight, When it lands after the user switched roots, Then the bar keeps showing the root it is actually on", async () => {
    const user = userEvent.setup();
    let landMain: ((view: unknown) => void) | null = null;
    gitStateMock.mockImplementation((_sessionId: number, root: string) =>
      root === ""
        ? new Promise((resolve) => {
            landMain = resolve;
          })
        : Promise.resolve(
            gitState({ branch: "feature/ia", worktree: "proj-ia" }),
          ),
    );

    renderSidebar();
    await screen.findByTestId("root-switcher");
    const menu = await openSwitcher(user);
    await user.click(within(menu).getAllByTestId("root-option")[1]);

    await waitFor(() =>
      expect(screen.getByTestId("git-status-branch")).toHaveTextContent(
        "feature/ia",
      ),
    );

    // 主仓库那一发这才回来:它属于上一个根,必须被作废而不是摆到这个根上。
    expect(landMain).not.toBeNull();
    await act(async () => {
      landMain!(gitState({ branch: "main" }));
    });

    expect(screen.getByTestId("git-status-branch")).toHaveTextContent(
      "feature/ia",
    );
  });
});

describe("变更行的本机操作跟着当前工作根走", () => {
  it("Given the sidebar is rooted at the worktree, When a session change row is revealed, Then the absolute target is built from that worktree and not from the session cwd", async () => {
    const user = userEvent.setup();
    useChatSidebarStore.setState({
      activeTab: "changes",
      changesScope: "session",
    });
    renderSidebar();
    await screen.findByTestId("root-switcher");

    const menu = await openSwitcher(user);
    await user.click(within(menu).getAllByTestId("root-option")[1]);

    // 切到 worktree 之后这一档只剩 worktree 里的那一个文件。
    const row = await screen.findByTestId("change-row");
    expect(row).toHaveAttribute("data-path", "internal/turn.go");

    await user.click(
      within(row).getByRole("button", { name: /more actions/i }),
    );
    const rowMenu = await screen.findByRole("menu");
    await user.click(
      within(rowMenu).getByRole("menuitem", { name: "Show in file manager" }),
    );

    // cwd 与工作根同名文件同路径:少了这条断言,把工作根换回会话 cwd 也照样绿。
    expect(revealPathMock).toHaveBeenCalledWith(`${WT}/internal/turn.go`);
  });
});

describe("根切换器：手动切换、固定与跨页共享", () => {
  it("Given the user picks the worktree, When the directory and the changes page load, Then both are rooted at that worktree and the switcher marks itself pinned", async () => {
    const user = userEvent.setup();
    renderSidebar();
    await screen.findByTestId("root-switcher");

    const menu = await openSwitcher(user);
    await user.click(within(menu).getAllByTestId("root-option")[1]);

    await waitFor(() =>
      expect(listDirMock).toHaveBeenCalledWith(7, WT, "", false),
    );
    expect(switcher()).toHaveTextContent("proj-ia");
    expect(switcher()).toHaveTextContent("Pinned");

    // 切一级 tab 不改变工作根：「变更」页的未提交档对同一个根取数。
    await user.click(screen.getByRole("tab", { name: /Changes/ }));
    await waitFor(() =>
      expect(gitChangesMock).toHaveBeenCalledWith(7, WT, "uncommitted", ""),
    );
    expect(switcher()).toHaveTextContent("proj-ia");
  });

  it("Given the sidebar is following automatically, When nothing has been pinned, Then the switcher carries no pinned marking at all", async () => {
    renderSidebar();

    const trigger = await screen.findByTestId("root-switcher");
    expect(trigger).not.toHaveTextContent("Pinned");
  });

  it("Given a root disappears, When the work roots are refetched, Then the sidebar falls back to the primary root instead of going blank", async () => {
    const user = userEvent.setup();
    renderSidebar();
    await screen.findByTestId("root-switcher");

    const menu = await openSwitcher(user);
    await user.click(within(menu).getAllByTestId("root-option")[1]);
    await waitFor(() => expect(switcher()).toHaveTextContent("proj-ia"));

    workRootsMock.mockResolvedValue([root(CWD, "proj", { isPrimary: true })]);
    useSessionStatusStore.getState().bumpDone(7, { kind: "done" });

    await waitFor(() =>
      expect(screen.queryByTestId("root-switcher")).toBeNull(),
    );
    await waitFor(() =>
      expect(listDirMock).toHaveBeenLastCalledWith(7, "", "", false),
    );
  });
});

describe("自动跟随：AI 写进另一个根", () => {
  it("Given the agent writes into a claimed worktree, When the messages arrive, Then the sidebar switches to that root and offers an immediate undo", async () => {
    const { rerender } = renderSidebar();
    await screen.findByTestId("root-switcher");
    expect(switcher()).toHaveTextContent("proj");

    rerender(
      <ChatContextSidebar
        sessionId={7}
        messages={wroteInWorktree}
        activeMessageId={null}
        onJumpToMessage={() => {}}
        cwd={CWD}
        remote={false}
      />,
    );

    await waitFor(() => expect(switcher()).toHaveTextContent("proj-ia"));
    const notice = await screen.findByTestId("root-follow-notice");
    expect(
      within(notice).getByRole("button", { name: "Stay in the main repo" }),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(listDirMock).toHaveBeenCalledWith(7, WT, "", false),
    );
  });

  it("Given the automatic switch happened, When the user takes the undo, Then the sidebar returns to the main repo and stops following", async () => {
    const user = userEvent.setup();
    const { rerender } = renderSidebar({ messages: wroteInWorktree });

    await waitFor(() => expect(switcher()).toHaveTextContent("proj-ia"));
    const notice = await screen.findByTestId("root-follow-notice");
    await user.click(
      within(notice).getByRole("button", { name: "Stay in the main repo" }),
    );

    await waitFor(() => expect(switcher()).toHaveTextContent("proj"));
    expect(switcher()).toHaveTextContent("Pinned");
    expect(screen.queryByTestId("root-follow-notice")).toBeNull();

    // 撤销之后再写进 worktree 也不再跟过去。
    rerender(
      <ChatContextSidebar
        sessionId={7}
        messages={[
          ...wroteInWorktree,
          userMsg(7),
          assistantMsg(8, [editBlock(`${WT}/internal/again.go`)]),
        ]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
        cwd={CWD}
        remote={false}
      />,
    );
    await waitFor(() => expect(switcher()).toHaveTextContent("proj"));
    expect(screen.queryByTestId("root-follow-notice")).toBeNull();
  });

  it("Given the user already picked a root by hand, When the agent later writes into the other root, Then the sidebar stays where the user put it", async () => {
    const user = userEvent.setup();
    const { rerender } = renderSidebar();
    await screen.findByTestId("root-switcher");

    const menu = await openSwitcher(user);
    await user.click(within(menu).getAllByTestId("root-option")[0]);
    await waitFor(() => expect(switcher()).toHaveTextContent("Pinned"));

    rerender(
      <ChatContextSidebar
        sessionId={7}
        messages={wroteInWorktree}
        activeMessageId={null}
        onJumpToMessage={() => {}}
        cwd={CWD}
        remote={false}
      />,
    );

    await waitFor(() => expect(switcher()).toHaveTextContent("proj"));
    expect(switcher()).not.toHaveTextContent("proj-ia");
    expect(screen.queryByTestId("root-follow-notice")).toBeNull();
  });

  it("Given the agent writes back in the main checkout, When the sidebar follows there, Then the notice names that root without calling it a worktree", async () => {
    const { rerender } = renderSidebar({ messages: wroteInWorktree });
    await waitFor(() => expect(switcher()).toHaveTextContent("proj-ia"));

    rerender(
      <ChatContextSidebar
        sessionId={7}
        messages={[
          ...wroteInWorktree,
          userMsg(7),
          assistantMsg(8, [editBlock(`${CWD}/internal/turn.go`)]),
        ]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
        cwd={CWD}
        remote={false}
      />,
    );

    await waitFor(() => expect(switcher()).toHaveTextContent("proj"));
    const notice = await screen.findByTestId("root-follow-notice");
    expect(notice).toHaveTextContent(
      "Switched to the repo the agent is editing: proj",
    );
    expect(notice).not.toHaveTextContent("worktree");
  });

  it("Given the temporary notice is showing, When a few seconds pass, Then it collapses on its own without touching the selected root", async () => {
    vi.useFakeTimers();
    try {
      renderSidebar({ messages: wroteInWorktree });

      await vi.waitFor(() =>
        expect(screen.getByTestId("root-follow-notice")).toBeInTheDocument(),
      );
      // act 包住这次推进：happy-dom 里 React 的调度走 MessageChannel，假计时器
      // 推不到它，不进 act 的话计时器回调里那次 setState 到断言时还没被刷出来。
      await act(async () => {
        await vi.advanceTimersByTimeAsync(6000);
      });

      expect(screen.queryByTestId("root-follow-notice")).toBeNull();
      expect(switcher()).toHaveTextContent("proj-ia");
    } finally {
      vi.useRealTimers();
    }
  });
});
