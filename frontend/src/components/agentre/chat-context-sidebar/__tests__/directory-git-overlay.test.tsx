import "@testing-library/jest-dom/vitest";

import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

const openPathMock = vi.fn();
const listDirMock = vi.fn();
const gitChangesMock = vi.fn();
vi.mock("@/../wailsjs/go/app/App", () => ({
  // 多工作根：本组用例都是单根会话，认领集合恒为空 → 根切换器不渲染，
  // 分支状态条整条收起，chrome 与本轮之前一致。
  WorkspaceFsWorkRoots: () => Promise.resolve([]),
  WorkspaceFsGitState: () =>
    Promise.resolve({
      branch: "",
      worktree: "",
      dirty: 0,
      ahead: 0,
      behind: 0,
      hasUpstream: false,
      notARepo: true,
      commonDir: "",
    }),
  OpenPath: (p: string) => openPathMock(p),
  RevealPath: vi.fn(),
  WorkspaceFsListDir: (
    sessionId: number,
    _root: string,
    relPath: string,
    ignored: boolean,
  ) => listDirMock(sessionId, relPath, ignored),
  WorkspaceFsGitChanges: (
    sessionId: number,
    _root: string,
    scope: string,
    baseRef: string,
  ) => gitChangesMock(sessionId, scope, baseRef),
}));

import { useChatSidebarStore } from "@/stores/chat-sidebar-store";
import { useFilePreviewTabsStore } from "@/stores/file-preview-tabs-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { ChatContextSidebar } from "../index";
import { DirectoryPanel } from "../views/directory-panel";

import type { workspace_fs_svc } from "@/../wailsjs/go/models";

const CWD = "/Users/me/proj";

type Change = workspace_fs_svc.ChangeView;

function change(seed: Partial<Change> & { path: string }): Change {
  return {
    oldPath: "",
    status: "modified",
    added: 0,
    deleted: 0,
    binary: false,
    ...seed,
  } as Change;
}

function changesView(
  seeds: Change[],
  extra: { notARepo?: boolean; baseRef?: string } = {},
) {
  return {
    notARepo: false,
    baseRef: "",
    truncated: false,
    ...extra,
    changes: seeds,
  };
}

function entry(name: string, isDir = false) {
  return { name, isDir, size: 12, mtime: 0, symlink: false, gitIgnored: false };
}

function listing(entries: ReturnType<typeof entry>[], truncated = false) {
  return { path: CWD, entries, truncated };
}

function dirRow(name: string): HTMLElement {
  const found = screen
    .getAllByTestId("directory-row")
    .find((el) => el.getAttribute("data-name") === name);
  if (!found) throw new Error(`no directory row named ${name}`);
  return found;
}

function renderPanelWithChanges(gitChanges: Change[] | null) {
  return render(
    <DirectoryPanel
      sessionId={7}
      cwd={CWD}
      root=""
      remote={false}
      gitChanges={gitChanges}
    />,
  );
}

function renderSidebar() {
  return render(
    <ChatContextSidebar
      sessionId={7}
      messages={[]}
      activeMessageId={null}
      onJumpToMessage={() => {}}
      cwd={CWD}
      remote={false}
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
  openPathMock.mockReset();
  openPathMock.mockResolvedValue(undefined);
  listDirMock.mockReset();
  listDirMock.mockResolvedValue(listing([]));
  gitChangesMock.mockReset();
  gitChangesMock.mockResolvedValue(changesView([]));
  sonnerMocks.toast.error.mockReset();
});

describe("目录页的 git 状态叠加：文件行", () => {
  it("colours a changed file's name and shows a screen-reader-hidden letter with a text label; an unchanged file stays plain", async () => {
    listDirMock.mockResolvedValue(listing([entry("turn.go"), entry("cwd.go")]));
    renderPanelWithChanges([change({ path: "turn.go", status: "modified" })]);

    const changedName = await screen.findByText("turn.go");
    expect(changedName).toHaveClass("text-status-waiting");
    const letter = dirRow("turn.go").querySelector("[data-status-letter]");
    expect(letter).toHaveTextContent("M");
    expect(letter).toHaveAttribute("aria-hidden", "true");
    expect(within(dirRow("turn.go")).getByText("Modified")).toHaveClass(
      "sr-only",
    );

    const unchangedName = screen.getByText("cwd.go");
    expect(unchangedName).not.toHaveClass("text-status-waiting");
    expect(dirRow("cwd.go").querySelector("[data-status-letter]")).toBeNull();
  });

  it("does not show a +N/-N diff badge in directory mode, even for a changed file with diff counts", async () => {
    listDirMock.mockResolvedValue(listing([entry("turn.go")]));
    renderPanelWithChanges([
      change({ path: "turn.go", status: "modified", added: 42, deleted: 7 }),
    ]);

    await screen.findByText("turn.go");
    expect(screen.queryByText("+42")).toBeNull();
    expect(screen.queryByText(/−7/)).toBeNull();
  });
});

describe("目录页的 git 状态叠加：目录行子树计数", () => {
  it("shows the subtree change count in the status colour, hidden when the subtree has no changes", async () => {
    listDirMock.mockResolvedValue(
      listing([entry("internal", true), entry("assets", true)]),
    );
    renderPanelWithChanges([
      change({ path: "internal/a.go" }),
      change({ path: "internal/b.go" }),
    ]);

    await screen.findByRole("button", { name: /expand internal/i });
    const count = within(dirRow("internal")).getByTestId("dir-subtree-count");
    expect(count).toHaveTextContent("2");
    expect(count).toHaveClass("text-status-waiting");
    expect(
      within(dirRow("assets")).queryByTestId("dir-subtree-count"),
    ).toBeNull();
  });

  it("hides the bare count from screen readers and puts it in the row's accessible label instead", async () => {
    listDirMock.mockResolvedValue(
      listing([entry("internal", true), entry("assets", true)]),
    );
    renderPanelWithChanges([
      change({ path: "internal/a.go" }),
      change({ path: "internal/b.go" }),
    ]);

    // 着色 + 裸数字不单独承载信息（spec「键盘与无障碍」）：数字对读屏隐藏，
    // 子树变动数另有文字标签，且必须落在**行本身**的可访问名里 —— 行的主按钮
    // 有显式 aria-label，任何 sr-only 文本放进按钮内部都不会被读出来。
    await screen.findByRole("button", { name: /expand internal/i });
    expect(
      within(dirRow("internal")).getByTestId("dir-subtree-count"),
    ).toHaveAttribute("aria-hidden", "true");
    expect(
      screen.getByRole("button", { name: "Expand internal, 2 changed files" }),
    ).toBeInTheDocument();
    // 子树没有变动的目录行不带这一段。
    expect(
      screen.getByRole("button", { name: "Expand assets" }),
    ).toBeInTheDocument();
  });

  it("counts the whole compacted chain's subtree from its frontier segment", async () => {
    listDirMock.mockImplementation((_id: number, relPath: string) => {
      if (relPath === "")
        return Promise.resolve(listing([entry("internal", true)]));
      if (relPath === "internal")
        return Promise.resolve(listing([entry("service", true)]));
      if (relPath === "internal/service")
        return Promise.resolve(listing([entry("chat.go")]));
      throw new Error(`unexpected relPath ${relPath}`);
    });
    renderPanelWithChanges([
      change({ path: "internal/service/chat.go" }),
      change({ path: "internal/service/other.go" }),
    ]);

    const foldedStart = await screen.findByRole("button", {
      name: "Expand internal, 2 changed files",
    });
    await userEvent.click(foldedStart);
    await screen.findByText("chat.go");

    // 压缩行的可访问名同样带整条链的子树变动数。
    const foldedRow = screen.getByRole("button", {
      name: "Collapse internal/service, 2 changed files",
    });
    expect(
      within(foldedRow).getByTestId("dir-subtree-count"),
    ).toHaveTextContent("2");
  });

  it("still counts a change that sits in a segment the chain swallowed (a deleted file leaves no row of its own)", async () => {
    // 链压缩的前提「中间段只有一个子目录、不含文件」只对**磁盘上还在**的条目成
    // 立：listDir 看不到已删除的文件，git 却照报。此时 internal/ 被吸进链里，
    // internal/legacy.go 在整棵树上没有任何一行，压缩行的数字是它唯一的出口。
    listDirMock.mockImplementation((_id: number, relPath: string) => {
      if (relPath === "")
        return Promise.resolve(listing([entry("internal", true)]));
      if (relPath === "internal")
        return Promise.resolve(listing([entry("service", true)]));
      if (relPath === "internal/service")
        return Promise.resolve(listing([entry("chat.go")]));
      throw new Error(`unexpected relPath ${relPath}`);
    });
    renderPanelWithChanges([
      change({ path: "internal/service/chat.go" }),
      change({ path: "internal/legacy.go", status: "deleted" }),
    ]);

    const foldedStart = await screen.findByRole("button", {
      name: "Expand internal, 2 changed files",
    });
    await userEvent.click(foldedStart);
    await screen.findByText("chat.go");

    // 链一旦长到 internal/service，数字也不能从 2 悄悄掉到 1。
    const folded = screen.getByRole("button", {
      name: "Collapse internal/service, 2 changed files",
    });
    expect(within(folded).getByTestId("dir-subtree-count")).toHaveTextContent(
      "2",
    );
  });
});

describe("目录页的 git 状态叠加：无叠加降级", () => {
  it("renders the tree without any overlay when there is no change list (component-level default)", async () => {
    listDirMock.mockResolvedValue(listing([entry("turn.go")]));
    renderPanelWithChanges(null);

    await screen.findByText("turn.go");
    expect(screen.queryByTestId("dir-subtree-count")).toBeNull();
    expect(dirRow("turn.go").querySelector("[data-status-letter]")).toBeNull();
  });
});

describe("目录页的 git 状态叠加：取数与去重（ChatContextSidebar 全链路）", () => {
  it("fetches the uncommitted scope once for the directory overlay and colours the matching row", async () => {
    listDirMock.mockResolvedValue(listing([entry("turn.go")]));
    gitChangesMock.mockResolvedValue(
      changesView([change({ path: "turn.go" })]),
    );
    renderSidebar();

    await screen.findByText("turn.go");
    await waitFor(() => expect(gitChangesMock).toHaveBeenCalledTimes(1));
    expect(gitChangesMock).toHaveBeenCalledWith(7, "uncommitted", "");
    await waitFor(() =>
      expect(screen.getByText("turn.go")).toHaveClass("text-status-waiting"),
    );
  });

  it("drops an in-flight overlay response when the session switches to one without a working directory", async () => {
    // 换到一个没有工作目录的会话时不再取数，但上一个仓库那次请求还在途：它回来
    // 若还能落盘，Git tab 的角标就会在新会话上显示旧仓库的变动数。
    listDirMock.mockResolvedValue(listing([entry("turn.go")]));
    let resolveChanges: ((v: unknown) => void) | null = null;
    gitChangesMock.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveChanges = resolve;
        }),
    );
    const { rerender } = renderSidebar();
    await waitFor(() => expect(gitChangesMock).toHaveBeenCalledTimes(1));

    rerender(
      <ChatContextSidebar
        sessionId={7}
        messages={[]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
        cwd=""
        remote={false}
      />,
    );
    const changesTab = screen.getByRole("tab", { name: /^Changes/ });
    expect(changesTab).not.toHaveTextContent("3");

    await act(async () => {
      resolveChanges!(
        changesView([
          change({ path: "a.go" }),
          change({ path: "b.go" }),
          change({ path: "c.go" }),
        ]),
      );
    });
    expect(screen.getByRole("tab", { name: /^Changes/ })).not.toHaveTextContent(
      "3",
    );
  });

  it("does not fetch again when the 未提交 scope is opened afterwards: both pages share one snapshot", async () => {
    listDirMock.mockResolvedValue(listing([entry("turn.go")]));
    gitChangesMock.mockResolvedValue(
      changesView([change({ path: "turn.go" })]),
    );
    renderSidebar();

    await screen.findByText("turn.go");
    await waitFor(() => expect(gitChangesMock).toHaveBeenCalledTimes(1));

    await userEvent.click(screen.getByRole("tab", { name: /^Changes/ }));
    await userEvent.click(screen.getByRole("tab", { name: "Uncommitted" }));
    await screen.findByText("turn.go");
    expect(gitChangesMock).toHaveBeenCalledTimes(1);
  });

  it("renders the directory tree without overlay when the git read fails", async () => {
    listDirMock.mockResolvedValue(listing([entry("turn.go")]));
    gitChangesMock.mockRejectedValue(new Error("boom"));
    renderSidebar();

    const row = await screen.findByText("turn.go");
    await waitFor(() => expect(gitChangesMock).toHaveBeenCalled());
    expect(row).not.toHaveClass("text-status-waiting");
    expect(dirRow("turn.go").querySelector("[data-status-letter]")).toBeNull();
  });

  it("renders the directory tree without overlay when the working directory is not a git repository", async () => {
    listDirMock.mockResolvedValue(listing([entry("turn.go")]));
    gitChangesMock.mockResolvedValue(changesView([], { notARepo: true }));
    renderSidebar();

    const row = await screen.findByText("turn.go");
    await waitFor(() => expect(gitChangesMock).toHaveBeenCalled());
    expect(row).not.toHaveClass("text-status-waiting");
  });
});
