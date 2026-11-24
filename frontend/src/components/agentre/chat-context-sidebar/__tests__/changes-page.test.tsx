import "@testing-library/jest-dom/vitest";

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

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
  OpenPath: vi.fn(),
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
import {
  selectActivePreviewTab,
  useFilePreviewTabsStore,
} from "@/stores/file-preview-tabs-store";
import { useChatStreamsStore } from "@/stores/chat-streams-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { ChatContextToolWindows } from "../index";

import {
  changesHeading,
  isToolWindowShown,
  showOnly,
  stripButton,
} from "./tool-window-helpers";

import type { chat_svc } from "../../../../../wailsjs/go/models";

const CWD = "/Users/me/proj";

type Msg = chat_svc.ChatMessage;

function userMsg(id: number): Msg {
  return {
    id,
    role: "user",
    blocks: [{ type: "text", text: "do it" }],
  } as unknown as Msg;
}

function editBlock(
  path: string,
  kind: string,
  plus: number,
  minus: number,
): unknown {
  return {
    type: "tool_use",
    toolName: "Edit",
    toolInput: { file_path: path },
    canonical: {
      kind: "file.edit",
      fileEdit: { files: [{ path, kind, hunks: [], plus, minus }] },
    },
  };
}

function writeBlock(path: string, lines: number): unknown {
  return {
    type: "tool_use",
    toolName: "Write",
    toolInput: { file_path: path },
    canonical: {
      kind: "file.write",
      fileWrite: { path, content: "", lines, bytes: 0 },
    },
  };
}

function assistantMsg(id: number, blocks: unknown[]): Msg {
  return { id, role: "assistant", blocks } as unknown as Msg;
}

/**
 * 一份典型的会话：内部改了两个文件（其中一个是全量写入）、还往 /tmp 写过一份
 * 补丁（工作根之外，不该进列表）。
 */
const messages: Msg[] = [
  userMsg(1),
  assistantMsg(2, [
    editBlock("internal/service/chat_svc/turn.go", "modified", 12, 3),
    editBlock(`${CWD}/docs/gone.md`, "deleted", 0, 4),
    editBlock("/tmp/patch.diff", "created", 99, 0),
  ]),
  userMsg(3),
  assistantMsg(4, [writeBlock("README.md", 7)]),
];

function changesView(
  seeds: Array<{ path: string; status?: string; added?: number }>,
  extra: { notARepo?: boolean } = {},
) {
  return {
    notARepo: false,
    baseRef: "",
    truncated: false,
    ...extra,
    changes: seeds.map((seed) => ({
      oldPath: "",
      status: "modified",
      added: 0,
      deleted: 0,
      binary: false,
      ...seed,
    })),
  };
}

function renderSidebar(
  props: Partial<React.ComponentProps<typeof ChatContextToolWindows>> = {},
) {
  return render(
    <ChatContextToolWindows
      sessionId={7}
      messages={messages}
      activeMessageId={null}
      onJumpToMessage={() => {}}
      cwd={CWD}
      remote={false}
      {...props}
    />,
  );
}

function changeRow(path: string): HTMLElement {
  const found = screen
    .getAllByTestId(/^(change|git)-row$/)
    .find((el) => el.getAttribute("data-path") === path);
  if (!found) throw new Error(`no change row for ${path}`);
  return found;
}

function scopeTabs(): string[] {
  const group = screen.getByRole("tablist", { name: "Change scope" });
  return within(group)
    .getAllByRole("tab")
    .map((el) => el.textContent ?? "");
}

beforeEach(() => {
  localStorage.clear();
  showOnly("changes");
  useChatSidebarStore.setState({
    changesScope: "session",
    showIgnored: false,
  });
  useFilePreviewTabsStore.setState({ previewTabsBySession: {} });
  useSessionStatusStore.getState().__reset();
  useChatStreamsStore.setState({ streams: new Map() });
  listDirMock.mockReset();
  listDirMock.mockResolvedValue({ path: CWD, entries: [], truncated: false });
  gitChangesMock.mockReset();
  gitChangesMock.mockResolvedValue(changesView([]));
  sonnerMocks.toast.error.mockReset();
});

describe("「变更」工具窗 · 图标条", () => {
  it("图标条上是大纲 / 变更 / 网页预览 / 目录 / 关联 / 会话信息 / 终端七个工具窗，Git 不是其中之一；变更带当前档的文件数", () => {
    renderSidebar();

    const toolbar = screen.getByRole("toolbar", { name: "Tool windows" });
    expect(
      within(toolbar)
        .getAllByRole("button")
        .map((b) => b.getAttribute("aria-label")),
    ).toEqual([
      "Outline",
      "Changes",
      "Web preview",
      "Directory",
      "Relations",
      "Session info",
      "Terminal",
    ]);
    expect(within(toolbar).queryByRole("button", { name: /^git/i })).toBeNull();

    expect(stripButton("Changes")).toHaveAttribute("aria-pressed", "true");
    // 角标与标题的数字是当前档（本次会话）的文件数：/tmp 的那次写入不算数。
    expect(
      within(stripButton("Changes")).getByTestId("tool-window-badge"),
    ).toHaveTextContent("3");
    expect(changesHeading()).toHaveTextContent(/^Changes3$/);
  });

  it("Given 图标条, When 在图标上按 ↓ / ↑ / Home / End, Then 焦点在图标之间移动并回绕，不改变显示状态", async () => {
    const user = userEvent.setup();
    renderSidebar();

    stripButton("Outline").focus();
    await user.keyboard("{ArrowDown}");
    expect(stripButton("Changes")).toHaveFocus();
    await user.keyboard("{ArrowDown}");
    expect(stripButton("Web preview")).toHaveFocus();
    await user.keyboard("{ArrowDown}");
    expect(stripButton("Directory")).toHaveFocus();
    await user.keyboard("{ArrowDown}");
    expect(stripButton("Relations")).toHaveFocus();
    await user.keyboard("{ArrowDown}");
    expect(stripButton("Session info")).toHaveFocus();
    await user.keyboard("{ArrowDown}");
    expect(stripButton("Terminal")).toHaveFocus();
    await user.keyboard("{ArrowDown}");
    expect(stripButton("Outline")).toHaveFocus();
    await user.keyboard("{ArrowUp}");
    expect(stripButton("Terminal")).toHaveFocus();
    await user.keyboard("{Home}");
    expect(stripButton("Outline")).toHaveFocus();
    await user.keyboard("{End}");
    expect(stripButton("Terminal")).toHaveFocus();

    expect(isToolWindowShown("changes")).toBe(true);
    expect(isToolWindowShown("directory")).toBe(false);
  });
});

describe("「变更」页 · 本次会话档", () => {
  it("默认停在「本次会话」，按变动规模列出工具改过的文件，不为这一档打后端", () => {
    renderSidebar();

    expect(scopeTabs()).toEqual(["This session", "Uncommitted"]);
    expect(screen.getByRole("tab", { name: "This session" })).toHaveAttribute(
      "aria-selected",
      "true",
    );

    const rows = screen.getAllByTestId("change-row");
    expect(rows.map((el) => el.getAttribute("data-path"))).toEqual([
      "internal/service/chat_svc/turn.go",
      "README.md",
      "docs/gone.md",
    ]);
  });

  it("Given 「本次会话 / 未提交」两档, When 在当前档上按方向键, Then 切到另一档且焦点跟过去，只有当前档在 Tab 序列里", async () => {
    const user = userEvent.setup();
    renderSidebar();

    const session = screen.getByRole("tab", { name: "This session" });
    expect(session).toHaveAttribute("tabindex", "0");
    expect(screen.getByRole("tab", { name: "Uncommitted" })).toHaveAttribute(
      "tabindex",
      "-1",
    );

    session.focus();
    await user.keyboard("{ArrowRight}");
    expect(useChatSidebarStore.getState().changesScope).toBe("uncommitted");
    // 焦点留在档位组里，不冒泡出「变更」工具窗。
    expect(isToolWindowShown("changes")).toBe(true);
    expect(screen.getByRole("tab", { name: "Uncommitted" })).toHaveFocus();

    await user.keyboard("{ArrowLeft}");
    expect(useChatSidebarStore.getState().changesScope).toBe("session");
    expect(screen.getByRole("tab", { name: "This session" })).toHaveFocus();
  });

  it("正在跑的那一轮改到的文件当场进列表，不必等轮次落定", () => {
    // 在流的内容不在 messages 里：发送那一刻插进去的 assistant 是空占位，整轮的
    // 块都住在 chat-streams-store，落定时才被真正的消息替换。只读 messages 的话
    // AI 改文件的**整个过程**中这一页都写着「本会话还没有工具改动过文件」。
    useChatStreamsStore.getState().openStream({
      name: "chat:7:live",
      sessionId: 7,
      assistantMessageId: 99,
      streamStartedAt: 0,
    });
    useChatStreamsStore
      .getState()
      .appendLiveToolUse(
        7,
        99,
        editBlock("live/only.go", "modified", 8, 2) as never,
      );

    renderSidebar();

    const row = changeRow("live/only.go");
    expect(within(row).getByText("only.go")).toBeInTheDocument();
    expect(changesHeading()).toHaveTextContent(/^Changes4$/);
  });

  it("行是扁平的：basename 主显 + 目录后缀，右端是本会话累计的 ±N", () => {
    renderSidebar();

    const turn = changeRow("internal/service/chat_svc/turn.go");
    expect(within(turn).getByText("turn.go")).toBeInTheDocument();
    expect(
      within(turn).getByText("internal/service/chat_svc"),
    ).toBeInTheDocument();
    expect(within(turn).getByText("+12")).toBeInTheDocument();
    expect(within(turn).getByText("−3")).toBeInTheDocument();
  });

  it("四种状态各有字母与文字标签，全量写入自成一类而不是新建或修改", () => {
    renderSidebar();

    const expected: Array<[string, string, string]> = [
      ["internal/service/chat_svc/turn.go", "M", "Modified"],
      ["docs/gone.md", "D", "Deleted"],
      ["README.md", "W", "Written"],
    ];
    for (const [path, letter, label] of expected) {
      const row = changeRow(path);
      const mark = row.querySelector("[data-status-letter]");
      expect(mark).toHaveTextContent(letter);
      expect(mark).toHaveAttribute("aria-hidden", "true");
      expect(within(row).getByText(label)).toBeInTheDocument();
    }
    // 「写入」不冒充新建：Created 这个标签在整页都不出现。
    expect(screen.queryByText("Created")).toBeNull();
  });

  it("工作根之外的路径不进列表，即使工具真的写过它", () => {
    renderSidebar();

    expect(screen.queryByText("patch.diff")).toBeNull();
  });

  it("单击一行开出预览标签，来源模式是「本次会话」", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderSidebar();

    await user.click(
      within(changeRow("README.md")).getByRole("button", {
        name: /README\.md/,
      }),
    );

    expect(
      selectActivePreviewTab(useFilePreviewTabsStore.getState(), 7),
    ).toMatchObject({ path: "README.md", sourceMode: "session" });
  });

  it("本会话没有任何工具改动时给一句空态，而不是空白", () => {
    renderSidebar({ messages: [userMsg(1)] });

    expect(
      screen.getByText("No files have been changed by tools in this session"),
    ).toBeInTheDocument();
    expect(screen.queryAllByTestId("change-row")).toHaveLength(0);
  });
});

describe("「变更」页 · 未提交档", () => {
  it("一次点击切到「未提交」：走 git、行同形，tab 角标换成该档的文件数", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    gitChangesMock.mockResolvedValue(
      changesView([
        { path: "internal/a.go", status: "modified", added: 2 },
        { path: "b.md", status: "untracked" },
      ]),
    );
    renderSidebar();

    await user.click(screen.getByRole("tab", { name: "Uncommitted" }));

    await screen.findByText("a.go");
    expect(gitChangesMock).toHaveBeenCalledWith(7, "uncommitted", "");
    expect(screen.queryAllByTestId("change-row")).toHaveLength(0);

    const row = changeRow("internal/a.go");
    expect(row.querySelector("[data-status-letter]")).toHaveTextContent("M");
    expect(within(row).getByText("internal")).toBeInTheDocument();
    expect(within(row).getByText("+2")).toBeInTheDocument();

    await waitFor(() =>
      expect(changesHeading()).toHaveTextContent(/^Changes2$/),
    );
  });

  it("选中的档随会话侧栏状态记住", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderSidebar();

    await user.click(screen.getByRole("tab", { name: "Uncommitted" }));

    expect(useChatSidebarStore.getState().changesScope).toBe("uncommitted");
    expect(localStorage.getItem("chat-sidebar-state")).toContain(
      '"changesScope":"uncommitted"',
    );
  });
});

describe("「变更」页 · 非 git 仓库与无工作目录", () => {
  it("非 git 仓库只剩「本次会话」一档，消息派生的行照常在", async () => {
    gitChangesMock.mockResolvedValue(changesView([], { notARepo: true }));
    renderSidebar();

    await waitFor(() => expect(scopeTabs()).toEqual(["This session"]));
    expect(screen.getAllByTestId("change-row").length).toBeGreaterThan(0);
  });

  it("持久化在「未提交」档的会话进了非 git 仓库时钳回「本次会话」，不留空页", async () => {
    useChatSidebarStore.setState({ changesScope: "uncommitted" });
    gitChangesMock.mockResolvedValue(changesView([], { notARepo: true }));
    renderSidebar();

    await waitFor(() => expect(scopeTabs()).toEqual(["This session"]));
    expect(screen.getByRole("tab", { name: "This session" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });

  it("会话没有工作目录时同样只剩一档，且完全不打后端", () => {
    renderSidebar({ cwd: "" });

    expect(scopeTabs()).toEqual(["This session"]);
    expect(gitChangesMock).not.toHaveBeenCalled();
  });
});
