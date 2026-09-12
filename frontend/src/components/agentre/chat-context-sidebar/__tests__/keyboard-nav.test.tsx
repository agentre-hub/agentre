import "@testing-library/jest-dom/vitest";

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

const listDirMock = vi.fn();
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
  OpenPath: vi.fn(),
  RevealPath: vi.fn(),
  WorkspaceFsSearchFiles: vi.fn(),
  WorkspaceFsListDir: (
    sessionId: number,
    _root: string,
    relPath: string,
    ignored: boolean,
  ) => listDirMock(sessionId, relPath, ignored),
}));

import {
  selectActivePreviewTab,
  useFilePreviewTabsStore,
} from "@/stores/file-preview-tabs-store";

import { DirectorySearchPanel } from "../views/directory-search-panel";
import { DirectoryView } from "../views/directory-view";
import { GitView } from "../views/git-view";

import type { DirectorySearch } from "../views/use-directory-search";
import type { GitChangesState } from "../views/use-git-changes";

const CWD = "/Users/me/proj";

function setupUser() {
  return userEvent.setup({ pointerEventsCheck: 0 });
}

function entry(name: string, isDir = false) {
  return { name, isDir, size: 12, mtime: 0, symlink: false, gitIgnored: false };
}

function listing(entries: ReturnType<typeof entry>[]) {
  return { path: CWD, entries, truncated: false };
}

function renderDirectory() {
  return render(
    <DirectoryView
      sessionId={1}
      cwd={CWD}
      root=""
      remote={false}
      showIgnored={false}
    />,
  );
}

/**
 * 树形键盘模型的取样对象是「目录」页（本轮之后它是侧栏里唯一的树）：一个目录
 * 行 service（含两个文件）+ 一个根级文件 README.md，展开后可见行序
 * service → chat.go → turn.go → README.md，层级 1 / 2 / 2 / 1。
 */
async function renderExpandedTree(
  user: ReturnType<typeof setupUser>,
): Promise<void> {
  listDirMock.mockImplementation((_id: number, relPath: string) =>
    Promise.resolve(
      relPath === ""
        ? listing([entry("service", true), entry("README.md")])
        : listing([entry("chat.go"), entry("turn.go")]),
    ),
  );
  renderDirectory();
  await screen.findByText("service");
  await user.click(
    within(treeRows()[0]).getByRole("button", { name: /^Expand/ }),
  );
  await screen.findByText("chat.go");
  // 展开是用鼠标点出来的，焦点因此停在那颗行内按钮上；键盘模型的用例都从
  // 「焦点在列表之外」起步，这里把它交还给 body。
  (document.activeElement as HTMLElement | null)?.blur();
}

function treeRows(): HTMLElement[] {
  return screen.getAllByRole("treeitem");
}

function namesOf(items: HTMLElement[]): (string | null)[] {
  return items.map((el) => el.getAttribute("data-name"));
}

beforeEach(() => {
  localStorage.clear();
  listDirMock.mockReset();
  listDirMock.mockResolvedValue(listing([]));
  sonnerMocks.toast.success.mockReset();
  useFilePreviewTabsStore.setState({ previewTabsBySession: {} });
});

describe("文件树的键盘模型", () => {
  it("整个列表是一个 Tab 停靠点，行是带 aria-level / aria-expanded 的 treeitem", async () => {
    const user = setupUser();
    await renderExpandedTree(user);

    const tree = screen.getByRole("tree", {
      name: "Working directory files",
    });
    const items = within(tree).getAllByRole("treeitem");
    expect(namesOf(items)).toEqual([
      "service",
      "chat.go",
      "turn.go",
      "README.md",
    ]);
    expect(items.map((el) => el.getAttribute("aria-level"))).toEqual([
      "1",
      "2",
      "2",
      "1",
    ]);
    // 展开态只在目录行上表达。
    expect(items[0]).toHaveAttribute("aria-expanded", "true");
    expect(items[1]).not.toHaveAttribute("aria-expanded");

    // roving tabindex：树内同时只有一个 tabindex=0。
    await user.tab();
    expect(items[0]).toHaveFocus();
    expect(items.map((el) => el.getAttribute("tabindex"))).toEqual([
      "0",
      "-1",
      "-1",
      "-1",
    ]);

    // 再按一次 Tab 就离开整棵树：行内的主按钮与 ⋯ 都不是 Tab 停靠点。
    await user.tab();
    expect(tree).not.toContainElement(document.activeElement as HTMLElement);
  });

  it("↑ / ↓ 沿可见行序移动，Home / End 跳到首 / 末行", async () => {
    const user = setupUser();
    await renderExpandedTree(user);

    await user.tab();
    await user.keyboard("{ArrowDown}");
    expect(treeRows()[1]).toHaveFocus();
    await user.keyboard("{ArrowDown}{ArrowDown}");
    expect(treeRows()[3]).toHaveFocus();
    // 末行再往下不动（不回绕）。
    await user.keyboard("{ArrowDown}");
    expect(treeRows()[3]).toHaveFocus();

    await user.keyboard("{ArrowUp}");
    expect(treeRows()[2]).toHaveFocus();
    await user.keyboard("{Home}");
    expect(treeRows()[0]).toHaveFocus();
    // 首行再往上不动。
    await user.keyboard("{ArrowUp}");
    expect(treeRows()[0]).toHaveFocus();
    await user.keyboard("{End}");
    expect(treeRows()[3]).toHaveFocus();
  });

  it("收起的子树不在可见行序里：↓ 从目录行直接到下一个同级行", async () => {
    const user = setupUser();
    await renderExpandedTree(user);

    await user.click(
      within(treeRows()[0]).getByRole("button", { name: /^Collapse/ }),
    );
    expect(treeRows()).toHaveLength(2);

    await user.keyboard("{ArrowDown}");
    expect(treeRows()[1]).toHaveFocus();
    expect(treeRows()[1]).toHaveAttribute("data-name", "README.md");
  });

  it("→ 展开目录行、已展开则移入首个子项；← 收起、已收起则移到父行", async () => {
    const user = setupUser();
    await renderExpandedTree(user);

    await user.tab();
    await user.keyboard("{ArrowLeft}");
    expect(treeRows()[0]).toHaveAttribute("aria-expanded", "false");
    expect(treeRows()[0]).toHaveFocus();

    await user.keyboard("{ArrowRight}");
    expect(treeRows()[0]).toHaveAttribute("aria-expanded", "true");
    // 展开这一步不移动焦点。
    expect(treeRows()[0]).toHaveFocus();

    await user.keyboard("{ArrowRight}");
    expect(treeRows()[1]).toHaveFocus();
    // 文件行上的 → 什么也不做。
    await user.keyboard("{ArrowRight}");
    expect(treeRows()[1]).toHaveFocus();

    await user.keyboard("{ArrowLeft}");
    expect(treeRows()[0]).toHaveFocus();
    expect(treeRows()[0]).toHaveAttribute("aria-expanded", "true");
  });

  it("→ 触发这一层的懒加载，子项异步到达后焦点仍在目录行", async () => {
    let resolveChild: (value: unknown) => void = () => {};
    listDirMock.mockImplementation((_id: number, relPath: string) =>
      relPath === ""
        ? Promise.resolve(listing([entry("app", true)]))
        : new Promise((resolve) => {
            resolveChild = resolve;
          }),
    );
    const user = setupUser();
    render(
      <DirectoryView
        sessionId={1}
        cwd={CWD}
        root=""
        remote={false}
        showIgnored={false}
      />,
    );

    await screen.findByRole("treeitem");
    await user.tab();
    const dirRow = treeRows()[0];
    expect(dirRow).toHaveFocus();

    await user.keyboard("{ArrowRight}");
    await waitFor(() =>
      expect(listDirMock).toHaveBeenCalledWith(1, "app", false),
    );
    // 取数在途时焦点不动。
    expect(dirRow).toHaveFocus();

    resolveChild(listing([entry("app.go")]));
    await screen.findByText("app.go");
    // 子项异步到达后，焦点与 roving 位置仍在目录行上。
    expect(dirRow).toHaveFocus();
    expect(dirRow).toHaveAttribute("tabindex", "0");

    await user.keyboard("{ArrowRight}");
    expect(treeRows()[1]).toHaveFocus();
    expect(treeRows()[1]).toHaveAttribute("data-name", "app.go");
  });

  it("链压缩让链尾变深时，roving 落点仍留在这一行", async () => {
    listDirMock.mockImplementation((_id: number, relPath: string) => {
      if (relPath === "") {
        return Promise.resolve(
          listing([entry("app", true), entry("zzz", true)]),
        );
      }
      // zzz 只有一个子目录、没有文件 → 展开后链压缩成 zzz/sub，这一行的链尾
      // （path）随之变深。
      if (relPath === "zzz")
        return Promise.resolve(listing([entry("sub", true)]));
      return Promise.resolve(listing([entry("deep.go")]));
    });
    const user = setupUser();
    render(
      <DirectoryView
        sessionId={1}
        cwd={CWD}
        root=""
        remote={false}
        showIgnored={false}
      />,
    );

    await screen.findByText("zzz");
    await user.tab();
    await user.keyboard("{ArrowDown}");
    const zzzRow = treeRows()[1];
    await user.keyboard("{ArrowRight}");

    await waitFor(() => expect(zzzRow).toHaveAttribute("data-name", "sub"));
    expect(zzzRow).toHaveFocus();
    expect(zzzRow).toHaveAttribute("tabindex", "0");
    expect(treeRows()[0]).toHaveAttribute("tabindex", "-1");
  });

  it("Enter 对文件行执行预览、对目录行执行展开收起", async () => {
    const user = setupUser();
    await renderExpandedTree(user);

    await user.tab();
    await user.keyboard("{Enter}");
    expect(treeRows()[0]).toHaveAttribute("aria-expanded", "false");
    await user.keyboard("{Enter}");
    expect(treeRows()[0]).toHaveAttribute("aria-expanded", "true");

    await user.keyboard("{ArrowDown}{Enter}");
    expect(
      selectActivePreviewTab(useFilePreviewTabsStore.getState(), 1),
    ).toMatchObject({
      path: "service/chat.go",
      isPreview: true,
    });
  });

  it("焦点在行内按钮上时（鼠标点过），Enter 只触发一次展开收起", async () => {
    const user = setupUser();
    await renderExpandedTree(user);

    await user.click(
      within(treeRows()[0]).getByRole("button", { name: /^Collapse/ }),
    );
    expect(treeRows()[0]).toHaveAttribute("aria-expanded", "false");

    await user.keyboard("{Enter}");
    expect(treeRows()[0]).toHaveAttribute("aria-expanded", "true");
  });

  it("Shift+F10 打开该行的菜单", async () => {
    const user = setupUser();
    await renderExpandedTree(user);

    await user.tab();
    await user.keyboard("{ArrowDown}");
    await user.keyboard("{Shift>}{F10}{/Shift}");

    const menu = await screen.findByRole("menu");
    await user.click(
      within(menu).getByRole("menuitem", { name: "Copy file name" }),
    );
    await vi.waitFor(async () =>
      expect(await navigator.clipboard.readText()).toBe("chat.go"),
    );
  });

  it("菜单键同样打开该行的菜单", async () => {
    const user = setupUser();
    await renderExpandedTree(user);

    await user.tab();
    await user.keyboard("{ArrowDown}{ContextMenu}");
    expect(
      within(await screen.findByRole("menu")).getByRole("menuitem", {
        name: "Preview",
      }),
    ).toBeInTheDocument();
  });
});

describe("扁平列表的键盘模型", () => {
  const gitState = {
    status: "loaded",
    view: {
      notARepo: false,
      baseRef: "",
      truncated: false,
      changes: [
        {
          path: "internal/service/chat.go",
          oldPath: "",
          status: "modified",
          added: 3,
          deleted: 1,
          binary: false,
        },
        {
          path: "README.md",
          oldPath: "",
          status: "added",
          added: 9,
          deleted: 0,
          binary: false,
        },
      ],
    },
  } as unknown as GitChangesState;

  function renderGit() {
    return render(
      <GitView
        sessionId={1}
        cwd={CWD}
        remote={false}
        state={gitState}
        onRetry={() => {}}
      />,
    );
  }

  it("未提交档是 listbox / option（不是树）：无层级、无展开态", async () => {
    const user = setupUser();
    renderGit();

    const list = screen.getByRole("listbox", { name: "Git changed files" });
    const options = within(list).getAllByRole("option");
    expect(namesOf(options)).toEqual(["chat.go", "README.md"]);
    expect(options[0]).not.toHaveAttribute("aria-level");
    expect(options[0]).not.toHaveAttribute("aria-expanded");
    expect(screen.queryByRole("tree")).toBeNull();

    await user.tab();
    expect(options[0]).toHaveFocus();
    await user.keyboard("{ArrowDown}");
    expect(options[1]).toHaveFocus();
    // 扁平列表没有展开收起，← / → 不移动焦点。
    await user.keyboard("{ArrowLeft}");
    expect(options[1]).toHaveFocus();
    await user.keyboard("{ArrowRight}");
    expect(options[1]).toHaveFocus();
    await user.keyboard("{Home}");
    expect(options[0]).toHaveFocus();
    await user.keyboard("{End}");
    expect(options[1]).toHaveFocus();
  });

  it("未提交档：Enter 预览当前行，选中态以 aria-selected 表达", async () => {
    const user = setupUser();
    renderGit();

    await user.tab();
    await user.keyboard("{Enter}");

    expect(
      selectActivePreviewTab(useFilePreviewTabsStore.getState(), 1),
    ).toMatchObject({ path: "internal/service/chat.go" });
    const options = screen.getAllByRole("option");
    expect(options[0]).toHaveAttribute("aria-selected", "true");
    expect(options[1]).toHaveAttribute("aria-selected", "false");
  });

  it("搜索结果同样是 listbox / option", async () => {
    const search: DirectorySearch = {
      active: true,
      toggle: () => {},
      close: () => {},
      query: "chat",
      setQuery: () => {},
      state: {
        status: "loaded",
        hits: [
          { path: "internal/chat.go", isDir: false },
          { path: "chat_svc", isDir: true },
        ],
        truncated: false,
      },
      retry: () => {},
    };
    const user = setupUser();
    render(
      <DirectorySearchPanel
        sessionId={1}
        cwd={CWD}
        remote={false}
        search={search}
      />,
    );

    const list = screen.getByRole("listbox", { name: "Search results" });
    const options = within(list).getAllByRole("option");
    expect(namesOf(options)).toEqual(["chat.go", "chat_svc"]);

    // 输入行（自动聚焦的输入框 + 清除按钮）在结果列表之外，整块结果列表自己
    // 只占一个 Tab 停靠点。
    expect(screen.getByRole("textbox")).toHaveFocus();
    await user.tab();
    await user.tab();
    expect(options[0]).toHaveFocus();
    await user.keyboard("{ArrowDown}");
    expect(options[1]).toHaveFocus();
    await user.tab();
    expect(list).not.toContainElement(document.activeElement as HTMLElement);
  });
});
