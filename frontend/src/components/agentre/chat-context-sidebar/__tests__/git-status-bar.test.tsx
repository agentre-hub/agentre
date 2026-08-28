import "@testing-library/jest-dom/vitest";

import { render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

const listDirMock = vi.fn();
const gitChangesMock = vi.fn();
const workRootsMock = vi.fn();
const gitStateMock = vi.fn();
vi.mock("@/../wailsjs/go/app/App", () => ({
  OpenPath: vi.fn(),
  RevealPath: vi.fn(),
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
  WorkspaceFsSearchFiles: vi.fn(),
  WorkspaceFsWorkRoots: (sessionId: number) => workRootsMock(sessionId),
  WorkspaceFsGitState: (sessionId: number, root: string) =>
    gitStateMock(sessionId, root),
}));

import { useChatSidebarStore } from "@/stores/chat-sidebar-store";
import { useFilePreviewTabsStore } from "@/stores/file-preview-tabs-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { ChatContextSidebar } from "../index";

const CWD = "/Users/me/proj";

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

function renderSidebar(remote = false) {
  return render(
    <ChatContextSidebar
      sessionId={7}
      messages={[]}
      activeMessageId={null}
      onJumpToMessage={() => {}}
      cwd={CWD}
      remote={remote}
    />,
  );
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
  listDirMock.mockResolvedValue({ path: CWD, entries: [], truncated: false });
  gitChangesMock.mockReset();
  gitChangesMock.mockResolvedValue({
    notARepo: false,
    baseRef: "",
    truncated: false,
    changes: [],
  });
  workRootsMock.mockReset();
  workRootsMock.mockResolvedValue([
    { path: CWD, name: "proj", isWorktree: false, isPrimary: true },
  ]);
  gitStateMock.mockReset();
  gitStateMock.mockResolvedValue(gitState());
  sonnerMocks.toast.error.mockReset();
});

describe("目录页第二行的 git 状态条", () => {
  it("Given a branch with upstream divergence and uncommitted files, When the directory page renders, Then the bar shows the branch, then ahead/behind, then the uncommitted count", async () => {
    gitStateMock.mockResolvedValue(
      gitState({ branch: "refactor/shared-ui", ahead: 2, behind: 3, dirty: 7 }),
    );
    renderSidebar();

    const bar = await screen.findByTestId("git-status-bar");
    expect(within(bar).getByTestId("git-status-branch")).toHaveTextContent(
      "refactor/shared-ui",
    );
    expect(bar).toHaveTextContent("2");
    expect(bar).toHaveTextContent("3");
    expect(bar).toHaveTextContent("7");
    expect(gitStateMock).toHaveBeenCalledWith(7, "");
  });

  it("Given a clean branch with no upstream divergence, When the bar renders, Then only the branch name is shown", async () => {
    gitStateMock.mockResolvedValue(gitState({ branch: "main" }));
    renderSidebar();

    const bar = await screen.findByTestId("git-status-bar");
    expect(within(bar).getByTestId("git-status-branch")).toHaveTextContent(
      "main",
    );
    expect(within(bar).queryByTestId("git-status-ahead")).toBeNull();
    expect(within(bar).queryByTestId("git-status-behind")).toBeNull();
    expect(within(bar).queryByTestId("git-status-dirty")).toBeNull();
  });

  it("Given the working directory is not a git repository, When the directory page renders, Then the whole bar collapses", async () => {
    gitStateMock.mockResolvedValue(gitState({ notARepo: true, branch: "" }));
    renderSidebar();

    await waitFor(() => expect(gitStateMock).toHaveBeenCalled());
    await screen.findByRole("tab", { name: /Directory/ });
    expect(screen.queryByTestId("git-status-bar")).toBeNull();
  });

  it("Given the current work root is a worktree, When the bar renders, Then it does not repeat the worktree marking the switcher already carries", async () => {
    gitStateMock.mockResolvedValue(
      gitState({ branch: "feature/ia", worktree: "proj-ia" }),
    );
    renderSidebar();

    const bar = await screen.findByTestId("git-status-bar");
    expect(bar).toHaveTextContent("feature/ia");
    expect(bar).not.toHaveTextContent("worktree");
    expect(bar).not.toHaveTextContent("proj-ia");
  });

  it("Given a narrow sidebar, When the bar has to give up width, Then the branch name truncates while the numbers keep their own space", async () => {
    gitStateMock.mockResolvedValue(
      gitState({ branch: "very/long/branch/name", ahead: 1, dirty: 2 }),
    );
    renderSidebar();

    const bar = await screen.findByTestId("git-status-bar");
    const branch = within(bar).getByTestId("git-status-branch");
    expect(branch.className).toContain("truncate");
    expect(branch.className).toContain("min-w-0");
    expect(within(bar).getByTestId("git-status-ahead").className).toContain(
      "shrink-0",
    );
    expect(within(bar).getByTestId("git-status-dirty").className).toContain(
      "shrink-0",
    );
  });

  it("Given a remote agentred session, When the directory page renders, Then the bar goes through the same binding and shows the same facts as a local session", async () => {
    gitStateMock.mockResolvedValue(
      gitState({ branch: "main", ahead: 1, dirty: 4 }),
    );
    renderSidebar(true);

    const bar = await screen.findByTestId("git-status-bar");
    expect(within(bar).getByTestId("git-status-branch")).toHaveTextContent(
      "main",
    );
    expect(bar).toHaveTextContent("1");
    expect(bar).toHaveTextContent("4");
    expect(gitStateMock).toHaveBeenCalledWith(7, "");
  });

  it("Given the changes page, When it renders, Then it carries no git status bar (that row belongs to the directory page)", async () => {
    useChatSidebarStore.setState({ activeTab: "changes" });
    renderSidebar();

    await screen.findByRole("tablist", { name: "Change scope" });
    expect(screen.queryByTestId("git-status-bar")).toBeNull();
  });
});
