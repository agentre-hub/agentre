import "@testing-library/jest-dom/vitest";

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

const openPathMock = vi.fn();
const revealPathMock = vi.fn();
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
  OpenPath: (p: string) => openPathMock(p),
  RevealPath: (p: string) => revealPathMock(p),
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

import { DirectoryView } from "../views/directory-view";
import { GitView } from "../views/git-view";
import { SessionChangesView } from "../views/session-changes-view";

import type { ChangeRow } from "../derive";
import type { GitChangesState } from "../views/use-git-changes";

const CWD = "/Users/me/proj";

function sessionRow(path: string, extra: Partial<ChangeRow> = {}): ChangeRow {
  const cut = path.lastIndexOf("/");
  return {
    path,
    name: cut < 0 ? path : path.slice(cut + 1),
    dir: cut < 0 ? "" : path.slice(0, cut),
    status: "modified",
    plus: 0,
    minus: 0,
    lastTurn: 1,
    ...extra,
  };
}

const rows: ChangeRow[] = [
  sessionRow("internal/service/chat.go", { plus: 5, minus: 2, lastTurn: 3 }),
  sessionRow("README.md"),
  sessionRow("assets/logo.png"),
  sessionRow("archive.zip", { lastTurn: 2 }),
];

// Radix 的菜单在 happy-dom 里要关掉 pointerEvents 检查。
function setupUser() {
  return userEvent.setup({ pointerEventsCheck: 0 });
}

function renderChanges(
  props: Partial<React.ComponentProps<typeof SessionChangesView>>,
) {
  return render(
    <SessionChangesView
      sessionId={1}
      rows={rows}
      cwd={CWD}
      remote={false}
      onJumpToTurn={() => {}}
      {...props}
    />,
  );
}

function renderGit(props: Partial<React.ComponentProps<typeof GitView>>) {
  return render(
    <GitView
      sessionId={1}
      cwd={CWD}
      remote={false}
      state={
        {
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
            ],
          },
        } as unknown as GitChangesState
      }
      onRetry={() => {}}
      {...props}
    />,
  );
}

/** 一行的容器：所有模式都给行加了 data-testid，右键菜单挂在这个容器上。 */
function rowOf(name: string): HTMLElement {
  const found = screen
    .getAllByTestId(/-row$/)
    .find((el) => el.getAttribute("data-name") === name);
  if (!found) throw new Error(`no row named ${name}`);
  return found;
}

/** 打开某一行的 ⋯ 菜单并返回菜单元素。 */
async function openRowMenu(name: string): Promise<HTMLElement> {
  const user = setupUser();
  await user.click(
    within(rowOf(name)).getByRole("button", { name: /more actions/i }),
  );
  return await screen.findByRole("menu");
}

function menuItemNames(menu: HTMLElement): string[] {
  return within(menu)
    .getAllByRole("menuitem")
    .map((el) => el.textContent ?? "");
}

beforeEach(() => {
  localStorage.clear();
  openPathMock.mockReset();
  openPathMock.mockResolvedValue(undefined);
  revealPathMock.mockReset();
  revealPathMock.mockResolvedValue(undefined);
  listDirMock.mockReset();
  listDirMock.mockResolvedValue({ path: CWD, entries: [], truncated: false });
  sonnerMocks.toast.error.mockReset();
  sonnerMocks.toast.success.mockReset();
  useFilePreviewTabsStore.setState({ previewTabsBySession: {} });
});

describe("行的统一点击语义", () => {
  it("本次会话档：单击可预览文件行开出临时标签，不再跳到对应轮次", async () => {
    const onJump = vi.fn();
    renderChanges({ onJumpToTurn: onJump });

    await setupUser().click(screen.getByRole("button", { name: /chat\.go/ }));

    expect(
      selectActivePreviewTab(useFilePreviewTabsStore.getState(), 1),
    ).toMatchObject({
      path: "internal/service/chat.go",
      sourceMode: "session",
      isPreview: true,
    });
    expect(onJump).not.toHaveBeenCalled();
  });

  it("未提交档：单击行开预览，不再用系统默认应用打开", async () => {
    renderGit({});

    await setupUser().click(screen.getByRole("button", { name: /chat\.go/ }));

    expect(
      selectActivePreviewTab(useFilePreviewTabsStore.getState(), 1),
    ).toMatchObject({ path: "internal/service/chat.go", sourceMode: "git" });
    expect(openPathMock).not.toHaveBeenCalled();
  });

  it("目录页：文件行可单击预览，目录行单击展开收起", async () => {
    const user = setupUser();
    listDirMock.mockImplementation((_id: number, relPath: string) =>
      Promise.resolve({
        path: CWD,
        truncated: false,
        entries:
          relPath === ""
            ? [
                {
                  name: "internal",
                  isDir: true,
                  size: 0,
                  mtime: 0,
                  symlink: false,
                  gitIgnored: false,
                },
              ]
            : [
                {
                  name: "chat.go",
                  isDir: false,
                  size: 1,
                  mtime: 0,
                  symlink: false,
                  gitIgnored: false,
                },
              ],
      }),
    );
    render(
      <DirectoryView
        sessionId={1}
        cwd={CWD}
        root=""
        remote={false}
        showIgnored
      />,
    );

    await user.click(await screen.findByRole("button", { name: /internal/ }));
    await user.click(await screen.findByRole("button", { name: /chat\.go/ }));

    expect(
      selectActivePreviewTab(useFilePreviewTabsStore.getState(), 1),
    ).toMatchObject({ path: "internal/chat.go", sourceMode: "directory" });
  });

  it("正在预览的行以强调背景高亮，其他行不高亮", async () => {
    renderChanges({});
    await setupUser().click(screen.getByRole("button", { name: /README\.md/ }));

    expect(rowOf("README.md").className).toContain("bg-accent");
    expect(rowOf("chat.go").className).not.toContain("bg-accent");
  });

  it("双击可预览行把它转成常驻标签", async () => {
    renderChanges({});
    await setupUser().dblClick(
      screen.getByRole("button", { name: /README\.md/ }),
    );

    expect(
      selectActivePreviewTab(useFilePreviewTabsStore.getState(), 1),
    ).toMatchObject({ path: "README.md", isPreview: false });
  });

  it("已有临时标签时双击另一行：原临时标签保留在原地，双击的行转常驻新增一个标签（不是原地互相覆盖）", async () => {
    renderChanges({});
    const user = setupUser();

    // 单击 chat.go 开出临时标签，再单击 README.md 原地替换它——此时仍只有
    // 一个临时标签（README.md）。真实鼠标双击会先各打一次 click 再打
    // dblclick：如果 onClick / onDoubleClick 挂在同一个元素上，双击 logo.png
    // 的第一次 click 会把 README.md 那个临时标签原地替换成 logo.png，
    // dblclick 随即只是把同一个位置转常驻——最终只剩 1 个标签，而不是
    // README.md（仍临时）+ logo.png（转常驻）两个标签。
    await user.click(screen.getByRole("button", { name: /chat\.go/ }));
    await user.click(screen.getByRole("button", { name: /README\.md/ }));
    await user.dblClick(screen.getByRole("button", { name: /logo\.png/ }));

    const entry = useFilePreviewTabsStore.getState().previewTabsBySession[1];
    expect(entry?.tabs).toHaveLength(2);
    expect(entry?.tabs.find((tab) => tab.path === "README.md")).toMatchObject({
      isPreview: true,
    });
    expect(
      entry?.tabs.find((tab) => tab.path === "assets/logo.png"),
    ).toMatchObject({ isPreview: false });
  });

  it("先单击开出临时标签、之后再双击同一行转常驻：被那次单击替换掉的标签不复活", async () => {
    renderChanges({});
    const user = setupUser();

    // 单击 chat.go 开出临时标签，再单击 README.md 把它原地替换掉——chat.go 是
    // 用户这一次单独的手势主动换走的，理应就此消失。
    await user.click(screen.getByRole("button", { name: /chat\.go/ }));
    await user.click(screen.getByRole("button", { name: /README\.md/ }));

    // 之后（可能过了很久）再双击 README.md 把它转常驻。这次双击的两次 click 都
    // 落在"已经开着的行"上、没有替换掉任何东西，所以没有需要补回的标签。
    await user.dblClick(screen.getByRole("button", { name: /README\.md/ }));

    const entry = useFilePreviewTabsStore.getState().previewTabsBySession[1];
    expect(entry?.tabs.map((tab) => tab.path)).toEqual(["README.md"]);
    expect(entry?.tabs[0]).toMatchObject({ isPreview: false });
  });
});

describe("不可预览的行", () => {
  it("不响应单击、不出 hover 高亮，但仍有 ⋯ 菜单按钮", async () => {
    renderChanges({});
    const row = rowOf("archive.zip");

    // 名称不是按钮：整行不可点。
    expect(within(row).queryByRole("button", { name: /archive\.zip/ })).toBe(
      null,
    );
    expect(row.className).not.toContain("hover:bg-muted");

    await setupUser().click(within(row).getByText("archive.zip"));
    expect(selectActivePreviewTab(useFilePreviewTabsStore.getState(), 1)).toBe(
      null,
    );

    expect(
      within(row).getByRole("button", { name: /more actions/i }),
    ).toBeInTheDocument();
  });

  it("菜单里「预览」「在新标签页预览」两项整项不渲染，其余项照常", async () => {
    renderChanges({});
    const menu = await openRowMenu("archive.zip");

    expect(menuItemNames(menu)).toEqual([
      "Jump to turn",
      "Open with default app",
      "Show in file manager",
      "Copy relative path",
      "Copy absolute path",
      "Copy file name",
    ]);
  });
});

describe("⋯ 槽位", () => {
  it("恒占 24px 且仅 hover / 键盘聚焦时显形", () => {
    renderChanges({});
    for (const name of ["chat.go", "README.md", "archive.zip"]) {
      const btn = within(rowOf(name)).getByRole("button", {
        name: /more actions/i,
      });
      expect(btn.className).toContain("size-6");
      expect(btn.className).toContain("opacity-0");
      expect(btn.className).toContain("group-hover/row:opacity-100");
      expect(btn.className).toContain("focus-visible:opacity-100");
    }
  });

  it("行内不再有其他常驻图标按钮", () => {
    renderChanges({});
    expect(screen.queryByRole("button", { name: /open file/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /^preview$/i })).toBeNull();
  });
});

describe("行的右键菜单", () => {
  it("本次会话档的文件行：预览两项 / 跳到对应轮次 / 打开两项 / 复制三项，按序分组", async () => {
    const menu = await (async () => {
      renderChanges({});
      return openRowMenu("chat.go");
    })();

    expect(menuItemNames(menu)).toEqual([
      "Preview",
      "Preview in a new tab",
      "Jump to turn",
      "Open with default app",
      "Show in file manager",
      "Copy relative path",
      "Copy absolute path",
      "Copy file name",
    ]);
    expect(within(menu).getAllByRole("separator")).toHaveLength(3);
  });

  it("右键整行打开的菜单与 ⋯ 打开的完全相同", async () => {
    renderChanges({});
    await setupUser().pointer({
      target: rowOf("chat.go"),
      keys: "[MouseRight]",
    });

    const menu = await screen.findByRole("menu");
    expect(menuItemNames(menu)).toEqual([
      "Preview",
      "Preview in a new tab",
      "Jump to turn",
      "Open with default app",
      "Show in file manager",
      "Copy relative path",
      "Copy absolute path",
      "Copy file name",
    ]);
  });

  it("「跳到对应轮次」只在「本次会话」档出现，且触发跳转回调", async () => {
    const onJump = vi.fn();
    renderChanges({ onJumpToTurn: onJump });
    const menu = await openRowMenu("chat.go");
    await setupUser().click(
      within(menu).getByRole("menuitem", { name: "Jump to turn" }),
    );
    expect(onJump).toHaveBeenCalledWith(3);
  });

  it("未提交档的文件行没有「跳到对应轮次」", async () => {
    renderGit({});
    const menu = await openRowMenu("chat.go");
    expect(menuItemNames(menu)).toEqual([
      "Preview",
      "Preview in a new tab",
      "Open with default app",
      "Show in file manager",
      "Copy relative path",
      "Copy absolute path",
      "Copy file name",
    ]);
  });

  it("「在新标签页预览」直接开常驻标签", async () => {
    renderChanges({});
    const menu = await openRowMenu("README.md");
    await setupUser().click(
      within(menu).getByRole("menuitem", { name: "Preview in a new tab" }),
    );
    expect(
      selectActivePreviewTab(useFilePreviewTabsStore.getState(), 1),
    ).toMatchObject({ path: "README.md", isPreview: false });
  });

  it("目录行的菜单是：展开或收起 / 在文件管理器中显示 / 复制两项", async () => {
    listDirMock.mockImplementation((_id: number, relPath: string) =>
      Promise.resolve({
        path: CWD,
        truncated: false,
        entries:
          relPath === ""
            ? [
                {
                  name: "internal",
                  isDir: true,
                  size: 0,
                  mtime: 0,
                  symlink: false,
                  gitIgnored: false,
                },
              ]
            : [],
      }),
    );
    render(
      <DirectoryView
        sessionId={1}
        cwd={CWD}
        root=""
        remote={false}
        showIgnored
      />,
    );
    await screen.findByRole("button", { name: /internal/ });

    const menu = await openRowMenu("internal");
    expect(menuItemNames(menu)).toEqual([
      "Expand",
      "Show in file manager",
      "Copy relative path",
      "Copy absolute path",
    ]);

    await setupUser().click(
      within(menu).getByRole("menuitem", { name: "Expand" }),
    );
    // 展开态挂在行（treeitem）上，不在行内按钮上。
    expect(rowOf("internal")).toHaveAttribute("aria-expanded", "true");
  });

  it("远端会话下本机三项整项不渲染", async () => {
    renderChanges({ remote: true });
    const menu = await openRowMenu("chat.go");
    expect(menuItemNames(menu)).toEqual([
      "Preview",
      "Preview in a new tab",
      "Jump to turn",
      "Copy relative path",
      "Copy file name",
    ]);
  });
});

describe("复制与在文件管理器中显示", () => {
  it("复制相对 / 绝对路径 / 文件名各自写入剪贴板并提示成功", async () => {
    renderChanges({});
    const user = setupUser();

    for (const [item, expected] of [
      ["Copy relative path", "internal/service/chat.go"],
      ["Copy absolute path", `${CWD}/internal/service/chat.go`],
      ["Copy file name", "chat.go"],
    ] as const) {
      const menu = await openRowMenu("chat.go");
      await user.click(within(menu).getByRole("menuitem", { name: item }));
      await vi.waitFor(async () =>
        expect(await navigator.clipboard.readText()).toBe(expected),
      );
      await vi.waitFor(() =>
        expect(sonnerMocks.toast.success).toHaveBeenCalledWith(
          "Copied",
          expect.anything(),
        ),
      );
      sonnerMocks.toast.success.mockClear();
    }
  });

  it("剪贴板不可用时提示复制失败", async () => {
    renderChanges({});
    const menu = await openRowMenu("chat.go");
    // 菜单已经开好之后再抽掉剪贴板：userEvent.setup() 自己会装一个剪贴板桩，
    // 提前抽掉会被它重新装回来。
    Object.defineProperty(navigator, "clipboard", {
      value: undefined,
      configurable: true,
    });
    await userEvent.click(
      within(menu).getByRole("menuitem", { name: "Copy relative path" }),
    );

    await vi.waitFor(() =>
      expect(sonnerMocks.toast.error).toHaveBeenCalledWith(
        "Copy failed",
        expect.anything(),
      ),
    );
  });

  it("「在文件管理器中显示」用绝对路径调 RevealPath", async () => {
    renderChanges({});
    const menu = await openRowMenu("chat.go");
    await setupUser().click(
      within(menu).getByRole("menuitem", { name: "Show in file manager" }),
    );
    expect(revealPathMock).toHaveBeenCalledWith(
      `${CWD}/internal/service/chat.go`,
    );
  });

  it("RevealPath 失败时提示里带上系统返回的原因", async () => {
    revealPathMock.mockRejectedValue(new Error("no such file"));
    renderChanges({});
    const menu = await openRowMenu("chat.go");
    await setupUser().click(
      within(menu).getByRole("menuitem", { name: "Show in file manager" }),
    );

    await vi.waitFor(() =>
      expect(sonnerMocks.toast.error).toHaveBeenCalledWith(
        "Failed to show in file manager: no such file",
      ),
    );
  });

  it("「用默认应用打开」走 OpenPath", async () => {
    renderChanges({});
    const menu = await openRowMenu("chat.go");
    await setupUser().click(
      within(menu).getByRole("menuitem", { name: "Open with default app" }),
    );
    expect(openPathMock).toHaveBeenCalledWith(
      `${CWD}/internal/service/chat.go`,
    );
  });
});

describe("文件类型图标经共享组件渲染", () => {
  it("语言与常见格式的身份通过共享 FileTypeIcon 渲染，而非四档可预览性", () => {
    renderChanges({});
    const typeOf = (name: string) =>
      rowOf(name)
        .querySelector("[data-file-type]")
        ?.getAttribute("data-file-type");

    expect(typeOf("chat.go")).toBe("go");
    expect(typeOf("README.md")).toBe("markdown");
    expect(typeOf("logo.png")).toBe("image");
    expect(typeOf("archive.zip")).toBe("archive");
  });

  it("图标是装饰元素，对辅助技术隐藏", () => {
    renderChanges({});
    expect(rowOf("chat.go").querySelector("[data-file-type]")).toHaveAttribute(
      "aria-hidden",
      "true",
    );
  });
});
