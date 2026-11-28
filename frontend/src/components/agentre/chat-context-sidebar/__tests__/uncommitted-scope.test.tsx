import "@testing-library/jest-dom/vitest";

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

const openPathMock = vi.fn();
const listDirMock = vi.fn();
const gitChangesMock = vi.fn();
// 工具窗骨架订阅会话的 chat:workroots 推送（useSessionRootSync）；happy-dom 下没有
// window.runtime，事件订阅桩成 no-op，其余 runtime 函数照旧。
vi.mock("@/../wailsjs/runtime/runtime", async () => ({
  ...(await vi.importActual("@/../wailsjs/runtime/runtime")),
  EventsOn: vi.fn(() => () => {}),
}));

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

import {
  selectActivePreviewTab,
  useFilePreviewTabsStore,
} from "@/stores/file-preview-tabs-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { ChatContextToolWindows } from "../index";

import { changesHeading, showOnly, stripButton } from "./tool-window-helpers";

import type { chat_svc } from "../../../../../wailsjs/go/models";

const CWD = "/Users/me/proj";

type ChangeSeed = {
  path: string;
  status?: string;
  added?: number;
  deleted?: number;
  binary?: boolean;
  oldPath?: string;
};

function change(seed: ChangeSeed) {
  return {
    oldPath: "",
    status: "modified",
    added: 0,
    deleted: 0,
    binary: false,
    ...seed,
  };
}

function changesView(
  seeds: ChangeSeed[],
  extra: { notARepo?: boolean; baseRef?: string; truncated?: boolean } = {},
) {
  return {
    notARepo: false,
    baseRef: "",
    truncated: false,
    ...extra,
    changes: seeds.map(change),
  };
}

function renderSidebar(
  props: Partial<React.ComponentProps<typeof ChatContextToolWindows>>,
) {
  return render(
    <ChatContextToolWindows
      sessionId={7}
      messages={[] as chat_svc.ChatMessage[]}
      activeMessageId={null}
      onJumpToMessage={() => {}}
      cwd={CWD}
      remote={false}
      {...props}
    />,
  );
}

function gitRow(path: string): HTMLElement {
  const found = screen
    .getAllByTestId("git-row")
    .find((el) => el.getAttribute("data-path") === path);
  if (!found) throw new Error(`no git row for ${path}`);
  return found;
}

/** 图标条上「变更」的角标数字；不显示角标时为 null。 */
function changesBadge(): string | null {
  return (
    within(stripButton("Changes")).queryByTestId("tool-window-badge")
      ?.textContent ?? null
  );
}

beforeEach(() => {
  localStorage.clear();
  showOnly("changes");
  useFilePreviewTabsStore.setState({ previewTabsBySession: {} });
  useSessionStatusStore.getState().__reset();
  openPathMock.mockReset();
  openPathMock.mockResolvedValue(undefined);
  listDirMock.mockReset();
  listDirMock.mockResolvedValue({ path: CWD, entries: [], truncated: false });
  gitChangesMock.mockReset();
  gitChangesMock.mockResolvedValue(changesView([]));
  sonnerMocks.toast.error.mockReset();
});

describe("「变更」页 · 未提交的文件", () => {
  it("asks for the uncommitted scope on entering the tab and renders flat rows", async () => {
    gitChangesMock.mockResolvedValue(
      changesView([
        {
          path: "internal/service/chat_svc/turn.go",
          status: "modified",
          added: 42,
          deleted: 7,
        },
        { path: "go.mod", status: "modified", added: 2 },
      ]),
    );
    renderSidebar({});

    await screen.findByText("turn.go");
    expect(gitChangesMock).toHaveBeenCalledWith(7, "uncommitted", "");

    // 扁平：basename 主显 + 灰色目录后缀（根目录下的文件后缀为空）。
    const turn = gitRow("internal/service/chat_svc/turn.go");
    expect(within(turn).getByText("turn.go")).toBeInTheDocument();
    expect(
      within(turn).getByText("internal/service/chat_svc"),
    ).toBeInTheDocument();
    expect(within(turn).getByText("+42")).toBeInTheDocument();
    expect(within(turn).getByText("−7")).toBeInTheDocument();

    const goMod = gitRow("go.mod");
    expect(within(goMod).queryByText("/")).toBeNull();
    expect(within(goMod).queryByText("−0")).toBeNull();
  });

  it("gives every status a readable label and hides the letter from screen readers", async () => {
    gitChangesMock.mockResolvedValue(
      changesView([
        { path: "a/m.go", status: "modified" },
        { path: "b/a.go", status: "added" },
        { path: "c/d.go", status: "deleted" },
        { path: "d/r.go", status: "renamed", oldPath: "d/old.go" },
        { path: "e/u.go", status: "untracked" },
      ]),
    );
    renderSidebar({});

    await screen.findByText("m.go");
    for (const [path, label] of [
      ["a/m.go", "Modified"],
      ["b/a.go", "Added"],
      ["c/d.go", "Deleted"],
      ["d/r.go", "Renamed"],
      ["e/u.go", "Untracked"],
    ]) {
      expect(within(gitRow(path)).getByText(label)).toHaveClass("sr-only");
    }
    expect(
      gitRow("a/m.go").querySelector("[data-status-letter]"),
    ).toHaveTextContent("M");
    expect(
      gitRow("a/m.go").querySelector("[data-status-letter]"),
    ).toHaveAttribute("aria-hidden", "true");
  });

  it("shows this scope's changed-file count on the Changes icon and title", async () => {
    gitChangesMock.mockResolvedValue(
      changesView([{ path: "a.go" }, { path: "b.go" }]),
    );
    renderSidebar({});

    await waitFor(() => expect(changesBadge()).toBe("2"));
    expect(changesHeading()).toHaveTextContent(/^Changes2$/);
  });

  it("shows no badge on the Changes icon until this scope's data has loaded", async () => {
    // 加载中不显角标（spec「图标条」：数据未就绪时不显示）。
    showOnly("outline");
    let resolve: (v: ReturnType<typeof changesView>) => void = () => {};
    gitChangesMock.mockReturnValue(
      new Promise((r) => {
        resolve = r;
      }),
    );
    renderSidebar({});

    await waitFor(() => expect(gitChangesMock).toHaveBeenCalled());
    expect(changesBadge()).toBeNull();
    resolve(changesView([{ path: "a.go" }, { path: "b.go" }]));
    await waitFor(() => expect(changesBadge()).toBe("2"));
  });

  it("keeps the uncommitted count on the Changes icon while the Changes window is hidden", async () => {
    // 角标数字是未提交的文件数——「侧栏关着也能看到改了几个文件」，所以变更窗
    // 收着也照样取数。
    showOnly("outline");
    gitChangesMock.mockResolvedValue(
      changesView([{ path: "a.go" }, { path: "b.go" }, { path: "c.go" }]),
    );
    renderSidebar({});

    await waitFor(() => expect(changesBadge()).toBe("3"));
    expect(gitChangesMock).toHaveBeenCalledWith(7, "uncommitted", "");
  });

  it("drops the previous session's count when the session changes behind another tab", async () => {
    gitChangesMock.mockResolvedValue(
      changesView([{ path: "a.go" }, { path: "b.go" }]),
    );
    const { rerender } = renderSidebar({});

    await waitFor(() => expect(changesBadge()).toBe("2"));

    await userEvent.click(stripButton("Outline"));
    // 新会话的快照还在路上：旧会话的数字不能留在角标上。
    gitChangesMock.mockReturnValue(new Promise(() => {}));
    rerender(
      <ChatContextToolWindows
        sessionId={8}
        messages={[]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
        cwd={CWD}
        remote={false}
      />,
    );

    await waitFor(() => expect(changesBadge()).toBeNull());
  });

  it("notes truncation with the actual number of listed files", async () => {
    gitChangesMock.mockResolvedValue(
      changesView([{ path: "a.go" }, { path: "b.go" }], { truncated: true }),
    );
    renderSidebar({});

    expect(
      await screen.findByText(/showing the first 2 changed files/i),
    ).toBeInTheDocument();
  });

  it("renders the shared file-type icon alongside the git status letter, and a precise pdf/zip icon sends its click to the external app", async () => {
    gitChangesMock.mockResolvedValue(
      changesView([
        { path: "internal/a.go", status: "modified" },
        { path: "report.pdf", status: "added" },
        { path: "archive.zip", status: "untracked" },
      ]),
    );
    renderSidebar({});
    await screen.findByText("a.go");

    const goRow = gitRow("internal/a.go");
    expect(goRow.querySelector("[data-status-letter]")).toHaveTextContent("M");
    expect(goRow.querySelector("[data-file-type]")).toHaveAttribute(
      "data-file-type",
      "go",
    );

    // 精确图标不授予预览能力：这两行单击开的是外部应用，不是预览标签。
    const pdfRow = gitRow("report.pdf");
    expect(pdfRow.querySelector("[data-file-type]")).toHaveAttribute(
      "data-file-type",
      "pdf",
    );

    const zipRow = gitRow("archive.zip");
    expect(zipRow.querySelector("[data-file-type]")).toHaveAttribute(
      "data-file-type",
      "archive",
    );

    await userEvent.click(
      within(zipRow).getByRole("button", { name: /archive\.zip/ }),
    );
    expect(selectActivePreviewTab(useFilePreviewTabsStore.getState(), 7)).toBe(
      null,
    );
    expect(openPathMock).toHaveBeenCalledWith(`${CWD}/archive.zip`);
  });

  it("opens the preview on row click instead of the system default app", async () => {
    gitChangesMock.mockResolvedValue(
      changesView([
        { path: "internal/a.go", status: "modified" },
        { path: "asset.zip", status: "added" },
      ]),
    );
    renderSidebar({});
    await screen.findByText("a.go");

    await userEvent.click(
      within(gitRow("internal/a.go")).getByRole("button", { name: /a\.go/ }),
    );
    expect(
      selectActivePreviewTab(useFilePreviewTabsStore.getState(), 7),
    ).toMatchObject({
      path: "internal/a.go",
      segment: null,
      sourceMode: "git",
    });
    expect(openPathMock).not.toHaveBeenCalled();

    // 不可预览的文件行单击交给外部应用，仍然不开预览标签。
    await userEvent.click(
      within(gitRow("asset.zip")).getByRole("button", { name: /asset\.zip/ }),
    );
    expect(openPathMock).toHaveBeenCalledWith(`${CWD}/asset.zip`);
    expect(
      selectActivePreviewTab(useFilePreviewTabsStore.getState(), 7),
    ).toMatchObject({ path: "internal/a.go" });
  });

  it("still previews on row click for a remote git session", async () => {
    gitChangesMock.mockResolvedValue(
      changesView([{ path: "internal/a.go", status: "modified" }]),
    );
    renderSidebar({ remote: true });
    await screen.findByText("a.go");

    await userEvent.click(
      within(gitRow("internal/a.go")).getByRole("button", { name: /a\.go/ }),
    );
    expect(
      selectActivePreviewTab(useFilePreviewTabsStore.getState(), 7),
    ).toMatchObject({ path: "internal/a.go", sourceMode: "git" });
    expect(openPathMock).not.toHaveBeenCalled();
  });
});

describe("「变更」页 · 空态、错误与自动重拉", () => {
  it("shows the clean-working-tree copy when nothing is uncommitted", async () => {
    renderSidebar({});

    expect(
      await screen.findByText(/the working tree is clean/i),
    ).toBeInTheDocument();
  });

  it("surfaces the backend failure verbatim with a retry that refetches", async () => {
    gitChangesMock.mockRejectedValueOnce(
      new Error("Remote agentred is too old; please upgrade to use this view"),
    );
    gitChangesMock.mockResolvedValue(changesView([{ path: "a.go" }]));
    renderSidebar({});

    expect(
      await screen.findByText(
        "Remote agentred is too old; please upgrade to use this view",
      ),
    ).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /retry/i }));
    expect(await screen.findByText("a.go")).toBeInTheDocument();
    expect(gitChangesMock).toHaveBeenCalledTimes(2);
  });

  it("falls back to a generic failure when the rejection carries no message", async () => {
    gitChangesMock.mockRejectedValue("");
    renderSidebar({});

    expect(
      await screen.findByText(/failed to read git changes/i),
    ).toBeInTheDocument();
  });

  it("refetches when the current session's turn ends", async () => {
    renderSidebar({});

    await waitFor(() => expect(gitChangesMock).toHaveBeenCalledTimes(1));

    useSessionStatusStore.getState().bumpDone(7, { kind: "done" });
    await waitFor(() => expect(gitChangesMock).toHaveBeenCalledTimes(2));

    // 别的会话结束不该惊动本面板。
    useSessionStatusStore.getState().bumpDone(9, { kind: "done" });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(gitChangesMock).toHaveBeenCalledTimes(2);
  });
});
