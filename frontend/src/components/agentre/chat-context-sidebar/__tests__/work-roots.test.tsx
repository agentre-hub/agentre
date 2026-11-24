import "@testing-library/jest-dom/vitest";

import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

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
import { useSessionRootStore } from "@/stores/session-root-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { ChatContextToolWindows } from "../index";

import { showOnly, stripButton } from "./tool-window-helpers";

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

function root(
  path: string,
  name: string,
  extra: { isWorktree?: boolean; isPrimary?: boolean; aiHere?: boolean } = {},
) {
  return {
    path,
    name,
    isWorktree: extra.isWorktree ?? false,
    isPrimary: extra.isPrimary ?? false,
    aiHere: extra.aiHere ?? false,
  };
}

/** 主仓库 + worktree；AI 在 `ai` 那个根。 */
function twoRoots(ai: "main" | "wt" = "main") {
  return [
    root(CWD, "proj", { isPrimary: true, aiHere: ai === "main" }),
    root(WT, "proj-ia", { isWorktree: true, aiHere: ai === "wt" }),
  ];
}

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
  props: Partial<React.ComponentProps<typeof ChatContextToolWindows>> = {},
) {
  return render(
    <ChatContextToolWindows
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

/** 会话 7 的根集合取回之后，把当前根手动切到 path（标题栏下拉的那个动作）。 */
async function pickRoot(path: string) {
  await waitFor(() =>
    expect(useSessionRootStore.getState().bySession[7]?.roots).toHaveLength(2),
  );
  act(() => useSessionRootStore.getState().select(7, path));
}

beforeEach(() => {
  localStorage.clear();
  showOnly("directory");
  useChatSidebarStore.setState({
    changesScope: "session",
    showIgnored: false,
  });
  useFilePreviewTabsStore.setState({ previewTabsBySession: {} });
  useSessionRootStore.setState({ bySession: {} });
  useSessionStatusStore.getState().__reset();
  runtime.handlers.clear();
  listDirMock.mockReset();
  listDirMock.mockResolvedValue(listing(["a.go"]));
  gitChangesMock.mockReset();
  gitChangesMock.mockResolvedValue(changesView([]));
  searchFilesMock.mockReset();
  searchFilesMock.mockResolvedValue({ hits: [], truncated: false });
  workRootsMock.mockReset();
  workRootsMock.mockResolvedValue(twoRoots());
  gitStateMock.mockReset();
  gitStateMock.mockResolvedValue(gitState());
  revealPathMock.mockReset();
  revealPathMock.mockResolvedValue(undefined);
  sonnerMocks.toast.error.mockReset();
});

describe("工具窗不再各带一个工作根切换器", () => {
  it("Given two work roots, When the Directory and Changes windows render, Then neither carries a root switcher or follow notice of its own", async () => {
    const user = userEvent.setup();
    renderSidebar();
    await waitFor(() => expect(workRootsMock).toHaveBeenCalledWith(7));
    await screen.findByRole("region", { name: "Directory" });
    await user.click(stripButton("Changes"));
    await screen.findByRole("region", { name: "Changes" });

    expect(screen.queryByTestId("root-switcher")).toBeNull();
    expect(screen.queryByTestId("root-follow-notice")).toBeNull();
    expect(screen.queryByRole("button", { name: /proj-ia/ })).toBeNull();
  });
});

describe("目录与变更按会话当前工作根取数", () => {
  it("Given the session root is switched to the worktree, When the directory and the changes page load, Then both are rooted at that worktree", async () => {
    const user = userEvent.setup();
    renderSidebar();
    await waitFor(() =>
      expect(listDirMock).toHaveBeenCalledWith(7, "", "", false),
    );
    await pickRoot(WT);

    await waitFor(() =>
      expect(listDirMock).toHaveBeenCalledWith(7, WT, "", false),
    );
    await user.click(stripButton("Changes"));
    await waitFor(() =>
      expect(gitChangesMock).toHaveBeenCalledWith(7, WT, "uncommitted", ""),
    );
  });

  it("Given following is on, When chat:workroots says the agent moved into the worktree mid-turn, Then the directory re-roots there without waiting for the turn to end", async () => {
    renderSidebar();
    await waitFor(() =>
      expect(listDirMock).toHaveBeenCalledWith(7, "", "", false),
    );
    await waitFor(() =>
      expect(runtime.handlers.has("chat:workroots:7")).toBe(true),
    );

    act(() =>
      runtime.handlers.get("chat:workroots:7")!({
        sessionId: 7,
        roots: twoRoots("wt"),
      }),
    );

    await waitFor(() =>
      expect(listDirMock).toHaveBeenCalledWith(7, WT, "", false),
    );
    expect(workRootsMock).toHaveBeenCalledTimes(1);
  });

  it("Given a root disappears, When the work roots are refetched at turn end, Then the directory falls back to the primary root instead of going blank", async () => {
    renderSidebar();
    await pickRoot(WT);
    await waitFor(() =>
      expect(listDirMock).toHaveBeenCalledWith(7, WT, "", false),
    );

    workRootsMock.mockResolvedValue([
      root(CWD, "proj", { isPrimary: true, aiHere: true }),
    ]);
    useSessionStatusStore.getState().bumpDone(7, { kind: "done" });

    await waitFor(() =>
      expect(listDirMock).toHaveBeenLastCalledWith(7, "", "", false),
    );
  });
});

describe("换根时在途的分支状态响应", () => {
  it("Given the previous root's git state is still in flight, When it lands after the session root switched, Then the bar keeps showing the root it is actually on", async () => {
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
    await pickRoot(WT);

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
  it("Given the session is rooted at the worktree, When a session change row is revealed, Then the absolute target is built from that worktree and not from the session cwd", async () => {
    const user = userEvent.setup();
    showOnly("changes");
    useChatSidebarStore.setState({ changesScope: "session" });
    renderSidebar();
    await pickRoot(WT);

    // 切到 worktree 之后这一档只剩 worktree 里的那一个文件。
    await waitFor(() =>
      expect(screen.getByTestId("change-row")).toHaveAttribute(
        "data-path",
        "internal/turn.go",
      ),
    );
    const row = screen.getByTestId("change-row");

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
