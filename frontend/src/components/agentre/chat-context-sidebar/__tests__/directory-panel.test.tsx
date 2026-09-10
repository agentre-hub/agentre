import "@testing-library/jest-dom/vitest";

import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, onTestFinished, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

const openPathMock = vi.fn();
const listDirMock = vi.fn();
const selectDirectoryMock = vi.fn();
const setLocalPathMock = vi.fn();
const searchMock = vi.fn();
// git 绑定这里只需要能被安全调用（本文件不断言它），断言在
// files-panel-git.test.tsx。
const gitMocks = vi.hoisted(() => ({
  changes: async () => ({
    notARepo: false,
    baseRef: "",
    changes: [],
    truncated: false,
  }),
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
  WorkspaceFsGitChanges: gitMocks.changes,
  SelectDirectory: (title: string) => selectDirectoryMock(title),
  ProjectSetLocalPath: (req: unknown) => setLocalPathMock(req),
  WorkspaceFsSearchFiles: (
    sessionId: number,
    _root: string,
    query: string,
    includeIgnored: boolean,
  ) => searchMock(sessionId, query, includeIgnored),
}));

import { useChatSidebarStore } from "@/stores/chat-sidebar-store";
import {
  selectActivePreviewTab,
  useFilePreviewTabsStore,
} from "@/stores/file-preview-tabs-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { DirectoryPanel } from "../views/directory-panel";

const CWD = "/Users/me/proj";

function entry(name: string, isDir = false, gitIgnored = false) {
  return { name, isDir, size: 12, mtime: 0, symlink: false, gitIgnored };
}

function listing(entries: ReturnType<typeof entry>[], truncated = false) {
  return { path: CWD, entries, truncated };
}

function renderPanel(
  props: Partial<React.ComponentProps<typeof DirectoryPanel>>,
) {
  return render(
    <DirectoryPanel
      sessionId={7}
      cwd={CWD}
      root=""
      remote={false}
      {...props}
    />,
  );
}

function row(name: string): HTMLElement {
  const found = screen
    .getAllByTestId("directory-row")
    .find((el) => el.getAttribute("data-name") === name);
  if (!found) throw new Error(`no directory row named ${name}`);
  return found;
}

/** 行内按钮 → 它所在的那一行（treeitem 才是带 aria-expanded 的那个元素）。 */
function rowOf(el: HTMLElement): HTMLElement {
  const found = el.closest<HTMLElement>('[role="treeitem"]');
  if (!found) throw new Error("button is not inside a treeitem");
  return found;
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
  selectDirectoryMock.mockReset();
  setLocalPathMock.mockReset();
  searchMock.mockReset();
  searchMock.mockResolvedValue({ hits: [], truncated: false });
  sonnerMocks.toast.error.mockReset();
});

describe("DirectoryPanel 目录页", () => {
  it("shows the icon-only 显示忽略项 button expressing its pressed state", async () => {
    renderPanel({});

    const toggle = await screen.findByRole("button", { name: /show ignored/i });
    // 纯图标：可见文本为空，可达名靠 aria-label 而不是可见 label。
    expect(toggle).toHaveTextContent("");
    expect(toggle).toHaveAttribute("aria-pressed", "false");

    await userEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-pressed", "true");
  });

  it("loads the root level once with includeIgnored=false and sorts dirs before files", async () => {
    listDirMock.mockResolvedValue(
      listing([
        entry("main.go"),
        entry("internal", true),
        entry("README.md"),
        entry("frontend", true),
      ]),
    );
    renderPanel({});

    await screen.findByRole("button", { name: /expand internal/i });
    expect(listDirMock).toHaveBeenCalledTimes(1);
    expect(listDirMock).toHaveBeenCalledWith(7, "", false);

    const names = screen
      .getAllByTestId("directory-row")
      .map((row) => row.getAttribute("data-name"));
    // 目录在前、各自 localeCompare 字母序（与 deriveFileTree 同一套排序约定）。
    expect(names).toEqual(["frontend", "internal", "main.go", "README.md"]);
  });

  it("shows a loading state while the root level is in flight", async () => {
    listDirMock.mockReturnValue(new Promise(() => {}));
    renderPanel({});

    expect(screen.getByText(/loading directory/i)).toBeInTheDocument();
  });

  it("lazily loads a folder on first expand, spins that row, and does not refetch on re-expand", async () => {
    let resolveChild: ((v: unknown) => void) | null = null;
    listDirMock.mockImplementation((_id: number, relPath: string) => {
      if (relPath === "") return Promise.resolve(listing([entry("app", true)]));
      return new Promise((resolve) => {
        resolveChild = resolve;
      });
    });
    renderPanel({});

    const appRow = await screen.findByRole("button", { name: /expand app/i });
    await userEvent.click(appRow);

    expect(listDirMock).toHaveBeenCalledWith(7, "app", false);
    expect(
      within(appRow).getByRole("status", { name: /loading directory/i }),
    ).toBeInTheDocument();

    resolveChild!(listing([entry("app.go")]));
    expect(await screen.findByText("app.go")).toBeInTheDocument();

    // 收起再展开只读缓存，不再打后端。
    await userEvent.click(
      screen.getByRole("button", { name: /collapse app/i }),
    );
    expect(screen.queryByText("app.go")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: /expand app/i }));
    expect(screen.getByText("app.go")).toBeInTheDocument();
    expect(listDirMock).toHaveBeenCalledTimes(2);
  });

  it("re-requests with includeIgnored=true when 显示忽略项 is switched on, persists it, and dims ignored rows", async () => {
    listDirMock.mockImplementation(
      (_id: number, _relPath: string, ignored: boolean) =>
        Promise.resolve(
          listing(
            ignored
              ? [entry("node_modules", true, true), entry("main.go")]
              : [entry("main.go")],
          ),
        ),
    );
    renderPanel({});

    await screen.findByText("main.go");
    await userEvent.click(
      screen.getByRole("button", { name: /show ignored/i }),
    );

    await waitFor(() => expect(listDirMock).toHaveBeenCalledWith(7, "", true));
    expect(useChatSidebarStore.getState().showIgnored).toBe(true);

    await screen.findByRole("button", { name: /expand node_modules/i });
    expect(row("node_modules")).toHaveAttribute("data-git-ignored", "true");
    expect(row("main.go")).not.toHaveAttribute("data-git-ignored");
  });

  it("notes the per-level truncation with the actual cap", async () => {
    listDirMock.mockResolvedValue(
      listing([entry("a.go"), entry("b.go"), entry("c.go")], true),
    );
    renderPanel({});

    expect(
      await screen.findByText(/showing the first 3 entries/i),
    ).toBeInTheDocument();
  });

  it("shows the empty copy for an empty directory", async () => {
    listDirMock.mockResolvedValue(listing([]));
    renderPanel({});

    expect(
      await screen.findByText(/this directory is empty/i),
    ).toBeInTheDocument();
  });

  it("shows the no-working-directory state without calling the backend", async () => {
    renderPanel({ cwd: "" });

    expect(
      await screen.findByText(/this session has no working directory/i),
    ).toBeInTheDocument();
    expect(listDirMock).not.toHaveBeenCalled();
  });

  // R10: 本机未配置路径不复用 WorkspaceFsNoCwd 那句笼统提示，专用空态给出可以
  // 就地指定路径的入口，指定成功后回调让调用方重新 LoadSession。
  it("shows the R10 local-path-missing state instead of the generic no-cwd copy, and specifying a path calls ProjectSetLocalPath + onCwdSpecified", async () => {
    selectDirectoryMock.mockResolvedValue("/Users/me/Code/agentre-hub");
    setLocalPathMock.mockResolvedValue({ id: 9, localPathMissing: false });
    const onCwdSpecified = vi.fn();

    renderPanel({
      cwd: "",
      cwdUnavailableReason: "local-path-missing",
      projectId: 9,
      onCwdSpecified,
    });

    expect(
      await screen.findByText(
        /this computer hasn't configured a path for this project yet/i,
      ),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/this session has no working directory/i),
    ).not.toBeInTheDocument();
    expect(listDirMock).not.toHaveBeenCalled();

    await userEvent.click(
      screen.getByRole("button", { name: /specify local path/i }),
    );

    await waitFor(() => {
      expect(setLocalPathMock).toHaveBeenCalledWith({
        id: 9,
        path: "/Users/me/Code/agentre-hub",
      });
    });
    expect(onCwdSpecified).toHaveBeenCalledTimes(1);
  });

  it("surfaces a remote-offline failure with a retry that re-requests", async () => {
    listDirMock.mockRejectedValueOnce("Remote device offline");
    listDirMock.mockResolvedValue(listing([entry("main.go")]));
    renderPanel({});

    expect(
      await screen.findByText("Remote device offline"),
    ).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /retry/i }));
    expect(await screen.findByText("main.go")).toBeInTheDocument();
    expect(listDirMock).toHaveBeenCalledTimes(2);
  });

  it("surfaces the daemon-outdated message verbatim instead of a generic failure", async () => {
    listDirMock.mockRejectedValue(
      new Error("Remote agentred is too old; please upgrade to use this view"),
    );
    renderPanel({});

    expect(
      await screen.findByText(
        "Remote agentred is too old; please upgrade to use this view",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText(/failed to read the directory/i)).toBeNull();
  });

  it("falls back to a generic read failure when the rejection carries no message", async () => {
    listDirMock.mockRejectedValue("");
    renderPanel({});

    expect(
      await screen.findByText(/failed to read the directory/i),
    ).toBeInTheDocument();
  });

  it("keeps the rest of the tree usable when one sub-directory fails to read, and retries it on re-expand", async () => {
    let subFails = true;
    listDirMock.mockImplementation((_id: number, relPath: string) => {
      if (relPath === "")
        return Promise.resolve(listing([entry("secret", true), entry("a.go")]));
      if (subFails) return Promise.reject(new Error("permission denied"));
      return Promise.resolve(listing([entry("key.txt")]));
    });
    renderPanel({});

    await userEvent.click(
      await screen.findByRole("button", { name: /expand secret/i }),
    );

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "permission denied",
    );
    expect(screen.getByText("a.go")).toBeInTheDocument();

    // 失败的那一层没有缓存可读，收起再展开就是它的重试。
    subFails = false;
    await userEvent.click(
      screen.getByRole("button", { name: /collapse secret/i }),
    );
    await userEvent.click(
      screen.getByRole("button", { name: /expand secret/i }),
    );
    expect(await screen.findByText("key.txt")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("resets expansion and reloads the root when the session changes", async () => {
    listDirMock.mockImplementation((_id: number, relPath: string) =>
      Promise.resolve(
        relPath === ""
          ? listing([entry("app", true)])
          : listing([entry("app.go")]),
      ),
    );
    const { rerender } = renderPanel({});

    await userEvent.click(
      await screen.findByRole("button", { name: /expand app/i }),
    );
    expect(await screen.findByText("app.go")).toBeInTheDocument();

    rerender(<DirectoryPanel sessionId={8} cwd={CWD} root="" remote={false} />);

    await waitFor(() => expect(listDirMock).toHaveBeenCalledWith(8, "", false));
    expect(rowOf(await screen.findByRole("button", { name: /expand app/i })))
      // 展开态挂在行（treeitem）上，不在行内按钮上。
      .toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText("app.go")).toBeNull();
  });

  // 决策 13：目录与 Git 两个模式的数据都是快照，「当前会话轮次结束」是「文件可能
  // 变了」的唯一强信号，两个模式都要在那时自动重拉（没有手动刷新按钮可以补救）。
  it("refetches the loaded levels when the current session's turn ends", async () => {
    listDirMock.mockImplementation((_id: number, relPath: string) =>
      Promise.resolve(
        relPath === ""
          ? listing([entry("app", true)])
          : listing([entry("app.go")]),
      ),
    );
    renderPanel({});

    await userEvent.click(
      await screen.findByRole("button", { name: /expand app/i }),
    );
    expect(await screen.findByText("app.go")).toBeInTheDocument();
    expect(listDirMock).toHaveBeenCalledTimes(2);

    useSessionStatusStore.getState().bumpDone(7, { kind: "done" });

    // 根与已展开的那一层各重拉一遍，展开态保留。
    await waitFor(() => expect(listDirMock).toHaveBeenCalledTimes(4));
    expect(listDirMock).toHaveBeenCalledWith(7, "app", false);
    expect(
      rowOf(await screen.findByRole("button", { name: /collapse app/i })),
    ).toHaveAttribute("aria-expanded", "true");

    // 别的会话结束不该惊动本面板。
    useSessionStatusStore.getState().bumpDone(9, { kind: "done" });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(listDirMock).toHaveBeenCalledTimes(4);
  });

  it("opens a file with the cwd-joined path from the row menu, only for local sessions", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    listDirMock.mockImplementation((_id: number, relPath: string) =>
      Promise.resolve(
        relPath === ""
          ? listing([entry("internal", true)])
          : listing([entry("chat.go")]),
      ),
    );
    renderPanel({});

    await user.click(
      await screen.findByRole("button", { name: /expand internal/i }),
    );
    await user.click(
      within(row("chat.go")).getByRole("button", { name: /more actions/i }),
    );
    await user.click(
      within(await screen.findByRole("menu")).getByRole("menuitem", {
        name: "Open with default app",
      }),
    );
    expect(openPathMock).toHaveBeenCalledWith(`${CWD}/internal/chat.go`);
  });

  it("drops the local-only menu items for a remote session", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    listDirMock.mockResolvedValue(listing([entry("main.go")]));
    renderPanel({ remote: true });

    await screen.findByText("main.go");
    await user.click(
      within(row("main.go")).getByRole("button", { name: /more actions/i }),
    );
    const menu = await screen.findByRole("menu");
    expect(
      within(menu)
        .getAllByRole("menuitem")
        .map((el) => el.textContent),
    ).toEqual([
      "Preview",
      "Preview in a new tab",
      "Copy relative path",
      "Copy file name",
    ]);
  });

  it("previews a previewable file row on click, by relPath", async () => {
    listDirMock.mockImplementation((_id: number, relPath: string) =>
      Promise.resolve(
        relPath === ""
          ? listing([entry("internal", true), entry("logo.png")])
          : listing([entry("chat.go"), entry("doc.pdf")]),
      ),
    );
    renderPanel({});

    await userEvent.click(
      await screen.findByRole("button", { name: /expand internal/i }),
    );
    // 不可预览的文件行不是按钮，单击没有反应。
    expect(
      within(row("doc.pdf")).queryByRole("button", { name: /doc\.pdf/ }),
    ).toBeNull();

    await userEvent.click(
      within(row("chat.go")).getByRole("button", { name: /chat\.go/ }),
    );
    expect(
      selectActivePreviewTab(useFilePreviewTabsStore.getState(), 7),
    ).toMatchObject({
      path: "internal/chat.go",
      segment: null,
      sourceMode: "directory",
    });
    expect(openPathMock).not.toHaveBeenCalled();

    await userEvent.click(
      within(row("logo.png")).getByRole("button", { name: /logo\.png/ }),
    );
    expect(
      selectActivePreviewTab(useFilePreviewTabsStore.getState(), 7),
    ).toMatchObject({
      path: "logo.png",
      segment: null,
      sourceMode: "directory",
    });
  });

  it("still previews on click for a remote directory session", async () => {
    listDirMock.mockResolvedValue(listing([entry("main.go"), entry("a.zip")]));
    renderPanel({ remote: true });

    await screen.findByText("main.go");
    await userEvent.click(
      within(row("main.go")).getByRole("button", { name: /main\.go/ }),
    );
    expect(
      selectActivePreviewTab(useFilePreviewTabsStore.getState(), 7),
    ).toMatchObject({ path: "main.go", sourceMode: "directory" });
  });

  it("gives PDF/ZIP precise shared icons without granting preview interaction", async () => {
    listDirMock.mockResolvedValue(
      listing([entry("doc.pdf"), entry("archive.zip")]),
    );
    renderPanel({});

    await screen.findByText("doc.pdf");

    const pdf = row("doc.pdf");
    expect(pdf.querySelector("[data-file-type]")).toHaveAttribute(
      "data-file-type",
      "pdf",
    );
    expect(within(pdf).queryByRole("button", { name: /doc\.pdf/ })).toBeNull();

    const zip = row("archive.zip");
    expect(zip.querySelector("[data-file-type]")).toHaveAttribute(
      "data-file-type",
      "archive",
    );
    expect(
      within(zip).queryByRole("button", { name: /archive\.zip/ }),
    ).toBeNull();
  });
});

describe("DirectoryPanel 目录搜索", () => {
  beforeEach(() => {
    listDirMock.mockResolvedValue(listing([]));
  });

  function searchToggle() {
    return screen.getByRole("button", { name: /^search$/i });
  }

  function searchInput() {
    return screen.getByRole("textbox", { name: /search files/i });
  }

  it("(a) shows the icon-only 搜索 button whenever there is a cwd, toggles a pressed filter state, and the input row exists only while active with focus landing in it", async () => {
    renderPanel({});
    const toggle = await screen.findByRole("button", { name: /^search$/i });
    // 纯图标：可见文本为空，可达名靠 aria-label。
    expect(toggle).toHaveTextContent("");
    expect(toggle).toHaveAttribute("aria-pressed", "false");
    expect(screen.queryByRole("textbox", { name: /search files/i })).toBeNull();

    await userEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-pressed", "true");
    const input = await screen.findByRole("textbox", { name: /search files/i });
    expect(input).toHaveFocus();
  });

  it("(a) does not render the 搜索 entry when the session has no working directory", async () => {
    renderPanel({ cwd: "" });
    expect(screen.queryByRole("button", { name: /^search$/i })).toBeNull();
  });

  it("(b) returns to the tree with expansion state intact when the filter is closed via the toggle or the clear button; activating search does not itself call the backend again", async () => {
    listDirMock.mockImplementation((_id: number, relPath: string) =>
      Promise.resolve(
        relPath === ""
          ? listing([entry("app", true)])
          : listing([entry("app.go")]),
      ),
    );
    renderPanel({});
    await userEvent.click(
      await screen.findByRole("button", { name: /expand app/i }),
    );
    expect(await screen.findByText("app.go")).toBeInTheDocument();
    expect(listDirMock).toHaveBeenCalledTimes(2);

    await userEvent.click(searchToggle());
    expect(screen.queryByText("app.go")).toBeNull();
    expect(listDirMock).toHaveBeenCalledTimes(2);

    fireEvent.change(searchInput(), { target: { value: "zzz" } });
    vi.useFakeTimers();
    onTestFinished(() => {
      vi.clearAllTimers();
      vi.useRealTimers();
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    vi.useRealTimers();

    // 关闭过滤态：树照原样出现，展开态不受影响，没有多打后端。
    await userEvent.click(searchToggle());
    expect(screen.getByText("app.go")).toBeInTheDocument();
    expect(
      rowOf(screen.getByRole("button", { name: /collapse app/i })),
    ).toHaveAttribute("aria-expanded", "true");
    expect(listDirMock).toHaveBeenCalledTimes(2);

    // 重新激活、输入，再用清除按钮：同样回到树。
    await userEvent.click(searchToggle());
    fireEvent.change(searchInput(), { target: { value: "zzz" } });
    await userEvent.click(
      screen.getByRole("button", { name: /clear search/i }),
    );
    expect(screen.getByText("app.go")).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: /search files/i })).toBeNull();
  });

  it("(c) debounces rapid input changes into a single backend call carrying sessionId, the final query and the current showIgnored", async () => {
    renderPanel({});
    await userEvent.click(
      screen.getByRole("button", { name: /show ignored/i }),
    );
    await userEvent.click(searchToggle());

    vi.useFakeTimers();
    onTestFinished(() => {
      vi.clearAllTimers();
      vi.useRealTimers();
    });
    const input = searchInput();
    fireEvent.change(input, { target: { value: "a" } });
    fireEvent.change(input, { target: { value: "ab" } });
    fireEvent.change(input, { target: { value: "abc" } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(299);
    });
    expect(searchMock).not.toHaveBeenCalled();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(searchMock).toHaveBeenCalledTimes(1);
    expect(searchMock).toHaveBeenCalledWith(7, "abc", true);
  });

  it("(c) a stale in-flight response cannot overwrite a newer one", async () => {
    let resolveFirst: ((v: unknown) => void) | null = null;
    let resolveSecond: ((v: unknown) => void) | null = null;
    searchMock.mockImplementation((_id: number, query: string) => {
      if (query === "ab") {
        return new Promise((resolve) => {
          resolveFirst = resolve;
        });
      }
      if (query === "abc") {
        return new Promise((resolve) => {
          resolveSecond = resolve;
        });
      }
      throw new Error(`unexpected query ${query}`);
    });
    renderPanel({});
    await userEvent.click(searchToggle());

    vi.useFakeTimers();
    onTestFinished(() => {
      vi.clearAllTimers();
      vi.useRealTimers();
    });
    const input = searchInput();
    fireEvent.change(input, { target: { value: "ab" } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    expect(searchMock).toHaveBeenCalledTimes(1);

    fireEvent.change(input, { target: { value: "abc" } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    expect(searchMock).toHaveBeenCalledTimes(2);

    // 更新的那次先回：结果应该反映它。命中的 basename 被高亮拆成了 <mark> +
    // 纯文本几个兄弟节点，getByText 按单节点精确匹配会失配，因此用不受高亮
    // 影响的 data-path 断言（与 data-name 一样直接取自 SidebarRow 的字符串
    // prop，不经过 nameChildren 的拆分渲染）。
    function resultPaths(): string[] {
      return screen
        .getAllByTestId("search-row")
        .map((el) => el.getAttribute("data-path") ?? "");
    }
    await act(async () => {
      resolveSecond!({
        hits: [{ path: "src/abc.ts", isDir: false }],
        truncated: false,
      });
    });
    expect(resultPaths()).toEqual(["src/abc.ts"]);

    // 旧的那次后回：不能覆盖新结果。
    await act(async () => {
      resolveFirst!({
        hits: [{ path: "src/ab.ts", isDir: false }],
        truncated: false,
      });
    });
    expect(resultPaths()).toEqual(["src/abc.ts"]);
  });

  it("(c2) a response that lands after the query is cleared cannot resurrect the result list", async () => {
    let resolveFirst: ((v: unknown) => void) | null = null;
    searchMock.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveFirst = resolve;
        }),
    );
    renderPanel({});
    await userEvent.click(searchToggle());

    vi.useFakeTimers();
    onTestFinished(() => {
      vi.clearAllTimers();
      vi.useRealTimers();
    });
    const input = searchInput();
    fireEvent.change(input, { target: { value: "ab" } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    expect(searchMock).toHaveBeenCalledTimes(1);

    // 请求还在途时把输入清空：面板回到「输入以搜索」的引导态。
    fireEvent.change(input, { target: { value: "" } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    expect(screen.queryAllByTestId("search-row")).toHaveLength(0);

    // 那次已经作废的请求回来了：不能把结果列表与命中计数再摆回空查询之上。
    await act(async () => {
      resolveFirst!({
        hits: [{ path: "src/ab.ts", isDir: false }],
        truncated: false,
      });
    });
    expect(screen.queryAllByTestId("search-row")).toHaveLength(0);
    expect(screen.queryByTestId("search-hit-count")).toBeNull();
  });

  it("(d) renders results as flat rows (basename + head-truncated directory suffix, highlighted match) sharing the row and menu with other modes", async () => {
    searchMock.mockResolvedValue({
      hits: [
        { path: "internal/service/chat_svc/chat.go", isDir: false },
        { path: "README.md", isDir: false },
      ],
      truncated: false,
    });
    renderPanel({});
    await userEvent.click(searchToggle());

    vi.useFakeTimers();
    onTestFinished(() => {
      vi.clearAllTimers();
      vi.useRealTimers();
    });
    fireEvent.change(searchInput(), { target: { value: "chat" } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    vi.useRealTimers();

    const rows = screen.getAllByTestId("search-row");
    expect(rows.map((r) => r.getAttribute("data-name"))).toEqual([
      "chat.go",
      "README.md",
    ]);
    expect(screen.getByTestId("search-hit-count")).toHaveTextContent(
      "2 results",
    );

    const chatRow = rows.find(
      (r) => r.getAttribute("data-name") === "chat.go",
    )!;
    // 命中的子串高亮。
    expect(within(chatRow).getByText("chat").tagName).toBe("MARK");
    // 头截断的目录后缀。
    expect(
      within(chatRow).getByText("internal/service/chat_svc"),
    ).toBeInTheDocument();
    expect(chatRow).toHaveAttribute(
      "title",
      "internal/service/chat_svc/chat.go",
    );
    // 搜索结果行同样通过共享组件渲染文件身份图标。
    expect(chatRow.querySelector("[data-file-type]")).toHaveAttribute(
      "data-file-type",
      "go",
    );

    // 行的点击与其他模式一致：预览。
    await userEvent.click(
      within(chatRow).getByRole("button", { name: /chat\.go/ }),
    );
    expect(
      selectActivePreviewTab(useFilePreviewTabsStore.getState(), 7),
    ).toMatchObject({
      path: "internal/service/chat_svc/chat.go",
      sourceMode: "directory",
    });

    // ⋯ 菜单与其他模式的文件行完全一致。
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    await user.click(
      within(chatRow).getByRole("button", { name: /more actions/i }),
    );
    const menu = await screen.findByRole("menu");
    expect(
      within(menu)
        .getAllByRole("menuitem")
        .map((el) => el.textContent),
    ).toEqual([
      "Preview",
      "Preview in a new tab",
      "Open with default app",
      "Show in file manager",
      "Copy relative path",
      "Copy absolute path",
      "Copy file name",
    ]);
  });

  it("(d) keeps a highlighted hit's accessible name intact on rows that are not previewable", async () => {
    // 高亮把 basename 拆成 <mark> + 纯文本几个兄弟节点，无障碍名计算会在节点之间
    // 插入空格（"no tes"）。目录命中与不在 allowlist 内的文件恰恰是「不可预览」
    // 那一类行——也就是搜索结果里最常见的形态，这一类行同样必须给出未拆分的
    // 可访问名，否则读屏念出的文件名是碎的。
    searchMock.mockResolvedValue({
      hits: [{ path: "src/notes", isDir: true }],
      truncated: false,
    });
    renderPanel({});
    await userEvent.click(searchToggle());

    vi.useFakeTimers();
    onTestFinished(() => {
      vi.clearAllTimers();
      vi.useRealTimers();
    });
    fireEvent.change(searchInput(), { target: { value: "no" } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });

    const row = screen.getByTestId("search-row");
    expect(within(row).getByText("no").tagName).toBe("MARK");
    expect(screen.getByRole("option", { name: "notes" })).toBe(row);
  });

  it("(e) shows a hint and does not call the backend while the query is empty; backspacing to empty stays in search mode instead of falling back to the tree", async () => {
    renderPanel({});
    await userEvent.click(searchToggle());

    expect(
      await screen.findByText(/type to search files by name/i),
    ).toBeInTheDocument();
    expect(searchMock).not.toHaveBeenCalled();

    const input = searchInput();
    fireEvent.change(input, { target: { value: "a" } });
    fireEvent.change(input, { target: { value: "" } });
    expect(
      screen.getByText(/type to search files by name/i),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("textbox", { name: /search files/i }),
    ).toBeInTheDocument();
  });

  it("(e) shows a no-hit empty state noting ignored files are excluded by default", async () => {
    searchMock.mockResolvedValue({ hits: [], truncated: false });
    renderPanel({});
    await userEvent.click(searchToggle());

    vi.useFakeTimers();
    onTestFinished(() => {
      vi.clearAllTimers();
      vi.useRealTimers();
    });
    fireEvent.change(searchInput(), { target: { value: "zzz" } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });

    expect(screen.getByText(/no files match "zzz"/i)).toBeInTheDocument();
    expect(
      screen.getByText(/ignored files are excluded from search by default/i),
    ).toBeInTheDocument();
  });

  it("(e) notes truncation with the actual number of returned hits", async () => {
    searchMock.mockResolvedValue({
      hits: [
        { path: "a.go", isDir: false },
        { path: "b.go", isDir: false },
      ],
      truncated: true,
    });
    renderPanel({});
    await userEvent.click(searchToggle());

    vi.useFakeTimers();
    onTestFinished(() => {
      vi.clearAllTimers();
      vi.useRealTimers();
    });
    fireEvent.change(searchInput(), { target: { value: "go" } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });

    expect(
      screen.getByText(/showing the first 2 results only/i),
    ).toBeInTheDocument();
  });

  it("(e) does not claim 'no match' when the traversal budget ran out before any hit was found", async () => {
    // 后端的目录数预算打满时会回 truncated=true 且 hits 为空
    // （internal/daemon/workspacefs/handler_search_test.go 的
    // TestSearchFiles_TruncatedByDirBudget 就是这个形状）。这份结果**不完整**，
    // 光说「没有匹配的文件」等于把「没找到」与「没找完」混为一谈。
    searchMock.mockResolvedValue({ hits: [], truncated: true });
    renderPanel({});
    await userEvent.click(searchToggle());

    vi.useFakeTimers();
    onTestFinished(() => {
      vi.clearAllTimers();
      vi.useRealTimers();
    });
    fireEvent.change(searchInput(), { target: { value: "zzz" } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });

    expect(
      screen.getByText(/search stopped before it finished/i),
    ).toBeInTheDocument();
    // 「默认不搜索被忽略的文件」那条常规提示在这里是误导：结果不完整与忽略项无关。
    expect(
      screen.queryByText(/ignored files are excluded/i),
    ).not.toBeInTheDocument();
  });

  it("(e) shows a loading state right after typing, before the debounced call fires", async () => {
    renderPanel({});
    await userEvent.click(searchToggle());

    fireEvent.change(searchInput(), { target: { value: "main" } });
    expect(screen.getByText(/searching/i)).toBeInTheDocument();
    expect(searchMock).not.toHaveBeenCalled();
  });

  it("(e) shows an error state with a retry that re-issues the same query", async () => {
    searchMock.mockRejectedValueOnce(new Error("boom"));
    searchMock.mockResolvedValue({
      hits: [{ path: "main.go", isDir: false }],
      truncated: false,
    });
    renderPanel({});
    await userEvent.click(searchToggle());

    vi.useFakeTimers();
    onTestFinished(() => {
      vi.clearAllTimers();
      vi.useRealTimers();
    });
    fireEvent.change(searchInput(), { target: { value: "main" } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    expect(screen.getByText("boom")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /retry/i }));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    // basename 被高亮拆成了兄弟节点，用不受影响的 data-path 断言命中。
    expect(screen.getByTestId("search-row")).toHaveAttribute(
      "data-path",
      "main.go",
    );
    expect(searchMock).toHaveBeenCalledTimes(2);
  });

  it("(e) surfaces the daemon-outdated message verbatim for search failures too", async () => {
    searchMock.mockRejectedValue(
      new Error("Remote agentred is too old; please upgrade to use this view"),
    );
    renderPanel({ remote: true });
    await userEvent.click(searchToggle());

    vi.useFakeTimers();
    onTestFinished(() => {
      vi.clearAllTimers();
      vi.useRealTimers();
    });
    fireEvent.change(searchInput(), { target: { value: "main" } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });

    expect(
      screen.getByText(
        "Remote agentred is too old; please upgrade to use this view",
      ),
    ).toBeInTheDocument();
  });
});

describe("DirectoryPanel 链压缩", () => {
  it("folds only what is already loaded, requests no unopened level up front, and cascades a single expand through every level as they arrive", async () => {
    // internal -> service -> chat_svc 是一条单子目录、不直接含文件的链；
    // chat_svc 自己才直接含文件，链在那里终止。
    listDirMock.mockImplementation((_id: number, relPath: string) => {
      if (relPath === "")
        return Promise.resolve(listing([entry("internal", true)]));
      if (relPath === "internal")
        return Promise.resolve(listing([entry("service", true)]));
      if (relPath === "internal/service")
        return Promise.resolve(listing([entry("chat_svc", true)]));
      if (relPath === "internal/service/chat_svc")
        return Promise.resolve(listing([entry("chat.go")]));
      throw new Error(`unexpected relPath ${relPath}`);
    });
    renderPanel({});

    // 根加载完之后，链还没有任何一层被展开过——只折叠出「internal」自己
    // 这一行，不为了探链去多打一次后端（「不触发额外的即时加载」）。
    const foldedStart = await screen.findByRole("button", {
      name: "Expand internal",
    });
    expect(listDirMock).toHaveBeenCalledTimes(1);
    expect(listDirMock).toHaveBeenCalledWith(7, "", false);

    // 用户只展开一次；链背后的多级懒加载逐层到达后应自动延展，直到探到
    // chat_svc 直接含文件为止，压缩行的整体展开态对用户始终只是「一次点击」。
    await userEvent.click(foldedStart);
    const chatGo = await screen.findByText("chat.go");
    expect(chatGo).toBeInTheDocument();

    const foldedRow = screen.getByRole("button", {
      name: "Collapse internal/service/chat_svc",
    });
    expect(within(foldedRow).getByTestId("chain-prefix")).toHaveTextContent(
      "internal/service/",
    );
    expect(listDirMock).toHaveBeenCalledWith(7, "internal", false);
    expect(listDirMock).toHaveBeenCalledWith(7, "internal/service", false);
    expect(listDirMock).toHaveBeenCalledWith(
      7,
      "internal/service/chat_svc",
      false,
    );
    expect(listDirMock).toHaveBeenCalledTimes(4);

    // chat.go 只比压缩行多缩进一级，尽管链吸收了三段。
    const chatGoRow = row("chat.go");
    const folded = row("chat_svc");
    expect(chatGoRow.style.paddingLeft).toBe(
      `${Number.parseInt(folded.style.paddingLeft, 10) + 14}px`,
    );
  });

  it("does not fold a directory whose own loaded level directly contains a file", async () => {
    listDirMock.mockImplementation((_id: number, relPath: string) =>
      Promise.resolve(
        relPath === ""
          ? listing([entry("app", true)])
          : listing([entry("app.go")]),
      ),
    );
    renderPanel({});

    const appRow = await screen.findByRole("button", { name: "Expand app" });
    await userEvent.click(appRow);
    expect(await screen.findByText("app.go")).toBeInTheDocument();
    // app 直接含 app.go，链不延展；不应该出现任何压缩前缀。
    expect(screen.queryByTestId("chain-prefix")).toBeNull();
  });
});
