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

import { useFilePreviewTabsStore } from "@/stores/file-preview-tabs-store";
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
 * 一份典型的会话：工具经 Edit / Write 改过三个文件。「变更」不再从这些消息派生
 * 任何东西——AI 常用 Bash / python 写文件，按工具名认出来的清单是残缺的；列表与
 * 角标只认 git 工作区。
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

beforeEach(() => {
  localStorage.clear();
  showOnly("changes");
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
  it("图标条上是大纲 / 变更 / 网页预览 / 目录 / 关联 / 会话信息 / 终端七个工具窗，Git 不是其中之一；变更带未提交的文件数", async () => {
    gitChangesMock.mockResolvedValue(
      changesView([{ path: "a.go" }, { path: "b.go" }]),
    );
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
    // 角标与标题的数字是 git 未提交的文件数，与消息里 Edit / Write 过几个文件无关。
    await waitFor(() =>
      expect(
        within(stripButton("Changes")).getByTestId("tool-window-badge"),
      ).toHaveTextContent("2"),
    );
    expect(changesHeading()).toHaveTextContent(/^Changes2$/);
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

describe("「变更」页只有工作区未提交这一份清单", () => {
  it("Given 工具在消息里 Edit / Write 过文件, When 打开「变更」, Then 没有档位切换，列的是 git 未提交的文件而不是消息派生的行", async () => {
    gitChangesMock.mockResolvedValue(
      changesView([
        { path: "internal/a.go", status: "modified", added: 2 },
        { path: "scripts/gen.py", status: "untracked" },
      ]),
    );
    renderSidebar();

    await screen.findByText("a.go");
    expect(gitChangesMock).toHaveBeenCalledWith(7, "uncommitted", "");
    expect(screen.queryByRole("tablist", { name: "Change scope" })).toBeNull();
    expect(screen.queryByRole("tab")).toBeNull();
    expect(screen.queryAllByTestId("change-row")).toHaveLength(0);
    // Bash / python 写出来的文件与 Edit 改的文件一样在列。
    expect(changeRow("scripts/gen.py")).toBeInTheDocument();
    expect(screen.queryByText("turn.go")).toBeNull();
  });

  it("Given 正在跑的那一轮 Edit 了一个文件, When git 还没报告它, Then 它不会凭消息进列表", async () => {
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

    expect(
      await screen.findByText(/the working tree is clean/i),
    ).toBeInTheDocument();
    expect(screen.queryByText("only.go")).toBeNull();
  });
});

describe("「变更」页 · 非 git 仓库与无工作目录", () => {
  it("Given 会话目录不是 git 仓库, When 打开「变更」, Then 给一句说明、没有角标，也不列消息派生的行", async () => {
    gitChangesMock.mockResolvedValue(changesView([], { notARepo: true }));
    renderSidebar();

    expect(
      await screen.findByText(
        "This folder isn't a git repository, so changes can't be listed",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByRole("tab")).toBeNull();
    expect(screen.queryAllByTestId("change-row")).toHaveLength(0);
    expect(
      within(stripButton("Changes")).queryByTestId("tool-window-badge"),
    ).toBeNull();
  });

  it("Given 会话没有工作目录, When 打开「变更」, Then 同一句说明，且完全不打后端", () => {
    renderSidebar({ cwd: "" });

    expect(
      screen.getByText(
        "This folder isn't a git repository, so changes can't be listed",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByRole("tab")).toBeNull();
    expect(gitChangesMock).not.toHaveBeenCalled();
  });
});
