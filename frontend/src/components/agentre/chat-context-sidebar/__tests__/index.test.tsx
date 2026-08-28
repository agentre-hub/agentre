import "@testing-library/jest-dom/vitest";

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const gitChangesMock = vi.fn();
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

import { ChatContextSidebar } from "../index";

import type { chat_svc } from "../../../../../wailsjs/go/models";

type Msg = chat_svc.ChatMessage;

function gitChangesView(
  seeds: { path: string }[],
  extra: { notARepo?: boolean; baseRef?: string } = {},
) {
  return {
    notARepo: false,
    baseRef: "",
    truncated: false,
    ...extra,
    changes: seeds.map((seed) => ({
      path: seed.path,
      oldPath: "",
      status: "modified",
      added: 0,
      deleted: 0,
      binary: false,
    })),
  };
}

const userM = (id: number, text: string): Msg =>
  ({
    id,
    role: "user",
    sessionId: 1,
    blocks: [{ type: "text", text }],
    model: "",
    promptTokens: 0,
    completionTokens: 0,
    durationMs: 0,
    errorText: "",
    seq: 0,
    createtime: 0,
  }) as unknown as Msg;

const assistantM = (id: number, text: string): Msg =>
  ({
    id,
    role: "assistant",
    sessionId: 1,
    blocks: [{ type: "text", text }],
    model: "",
    promptTokens: 0,
    completionTokens: 0,
    durationMs: 0,
    errorText: "",
    seq: 0,
    createtime: 0,
  }) as unknown as Msg;

describe("ChatContextSidebar", () => {
  beforeEach(() => {
    localStorage.clear();
    useChatSidebarStore.setState({ open: true, activeTab: "outline" });
    gitChangesMock.mockReset();
    gitChangesMock.mockResolvedValue(gitChangesView([]));
    listDirMock.mockReset();
    listDirMock.mockResolvedValue({
      path: "/repo",
      entries: [],
      truncated: false,
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("shows OutlineView by default", () => {
    render(
      <ChatContextSidebar
        sessionId={1}
        messages={[userM(1, "hello world")]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
      />,
    );
    expect(screen.getByText("hello world")).toBeInTheDocument();
  });

  it("does not render the legacy git Context block at the top", () => {
    render(
      <ChatContextSidebar
        sessionId={1}
        messages={[userM(1, "hello world")]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
      />,
    );
    expect(screen.queryByText("Context")).not.toBeInTheDocument();
    expect(screen.queryByTestId("branch-chip")).not.toBeInTheDocument();
  });

  it("exposes a resize separator on the left edge", () => {
    render(
      <ChatContextSidebar
        sessionId={1}
        messages={[userM(1, "hello world")]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
      />,
    );
    const sep = screen.getByRole("separator");
    expect(sep).toHaveAttribute("aria-orientation", "vertical");
  });

  it("switches to the session changes list when the Changes tab is clicked", async () => {
    render(
      <ChatContextSidebar
        sessionId={1}
        messages={[userM(1, "hello world")]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
      />,
    );
    await userEvent.click(screen.getByRole("tab", { name: /^Changes/ }));
    expect(
      screen.getByText(/No files have been changed by tools in this session/),
    ).toBeInTheDocument();
    expect(useChatSidebarStore.getState().activeTab).toBe("changes");
  });

  it("calls onJumpToMessage when outline row clicked", async () => {
    const onJump = vi.fn();
    render(
      <ChatContextSidebar
        sessionId={1}
        messages={[userM(99, "hello world")]}
        activeMessageId={null}
        onJumpToMessage={onJump}
      />,
    );
    await userEvent.click(screen.getByText("hello world"));
    expect(onJump).toHaveBeenCalledWith(99);
  });

  it("highlights the outline row matching activeMessageId", () => {
    render(
      <ChatContextSidebar
        sessionId={1}
        messages={[userM(11, "first"), userM(22, "second")]}
        activeMessageId={22}
        onJumpToMessage={() => {}}
      />,
    );
    const active = document.querySelector('[data-outline-message-id="22"]');
    const inactive = document.querySelector('[data-outline-message-id="11"]');
    expect(active).toHaveAttribute("data-active", "true");
    expect(inactive).toHaveAttribute("data-active", "false");
  });

  it("highlights the turn's user row when activeMessageId points to an assistant reply in that turn", () => {
    render(
      <ChatContextSidebar
        sessionId={1}
        messages={[
          userM(11, "first"),
          assistantM(12, "reply 1"),
          userM(22, "second"),
          assistantM(23, "reply 2"),
        ]}
        activeMessageId={12}
        onJumpToMessage={() => {}}
      />,
    );
    expect(
      document.querySelector('[data-outline-message-id="11"]'),
    ).toHaveAttribute("data-active", "true");
    expect(
      document.querySelector('[data-outline-message-id="22"]'),
    ).toHaveAttribute("data-active", "false");
  });

  it("switches the highlight to the next turn's user row once a later turn's assistant becomes active", () => {
    render(
      <ChatContextSidebar
        sessionId={1}
        messages={[
          userM(11, "first"),
          assistantM(12, "reply 1"),
          userM(22, "second"),
          assistantM(23, "reply 2"),
        ]}
        activeMessageId={23}
        onJumpToMessage={() => {}}
      />,
    );
    expect(
      document.querySelector('[data-outline-message-id="11"]'),
    ).toHaveAttribute("data-active", "false");
    expect(
      document.querySelector('[data-outline-message-id="22"]'),
    ).toHaveAttribute("data-active", "true");
  });

  it("Given the transcript focus reaches the last turn, When the active row changes, Then the outline scrolls that row to the bottom edge", () => {
    const scrollIntoView = vi
      .spyOn(HTMLElement.prototype, "scrollIntoView")
      .mockImplementation(() => {});

    render(
      <ChatContextSidebar
        sessionId={1}
        messages={[userM(11, "first"), userM(22, "second")]}
        activeMessageId={22}
        onJumpToMessage={() => {}}
      />,
    );

    expect(scrollIntoView).toHaveBeenCalledWith({
      block: "end",
      inline: "nearest",
    });
  });

  it("Given no transcript message is active, When the outline renders, Then it does not adjust scroll position", () => {
    const scrollIntoView = vi
      .spyOn(HTMLElement.prototype, "scrollIntoView")
      .mockImplementation(() => {});

    render(
      <ChatContextSidebar
        sessionId={1}
        messages={[userM(11, "first"), userM(22, "second")]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
      />,
    );

    expect(scrollIntoView).not.toHaveBeenCalled();
  });
});

describe("ChatContextSidebar top-level tabs", () => {
  beforeEach(() => {
    localStorage.clear();
    useChatSidebarStore.setState({ open: true, activeTab: "outline" });
    gitChangesMock.mockReset();
    gitChangesMock.mockResolvedValue(gitChangesView([]));
    listDirMock.mockReset();
    listDirMock.mockResolvedValue({
      path: "/repo",
      entries: [],
      truncated: false,
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders three equal-width, iconless tabs showing 大纲/变更/目录 counts, exactly one selected", () => {
    render(
      <ChatContextSidebar
        sessionId={1}
        messages={[userM(1, "first"), userM(2, "second")]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
      />,
    );

    const tabs = screen.getAllByRole("tab");
    expect(tabs).toHaveLength(3);
    expect(tabs.map((tab) => tab.textContent)).toEqual([
      "Outline2",
      "Changes0",
      "Directory",
    ]);
    // 不带图标。
    for (const tab of tabs) {
      expect(tab.querySelector("svg")).toBeNull();
    }
    // 恰有一段选中（大纲），其余未选中。
    expect(tabs.map((tab) => tab.getAttribute("aria-selected"))).toEqual([
      "true",
      "false",
      "false",
    ]);
    expect(screen.getByRole("tablist")).toContainElement(tabs[0]);
  });

  it("counts the message-derived rows on the Changes tab without calling the backend", () => {
    render(
      <ChatContextSidebar
        sessionId={1}
        messages={[]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
        cwd="/repo"
      />,
    );

    // 默认档是「本次会话」：角标来自消息派生，进大纲页时不打后端。
    expect(screen.getByRole("tab", { name: /^Changes/ }).textContent).toBe(
      "Changes0",
    );
    expect(gitChangesMock).not.toHaveBeenCalled();
  });

  it("switches the selected tab on click and persists it to the sidebar store", async () => {
    render(
      <ChatContextSidebar
        sessionId={1}
        messages={[]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
        cwd="/repo"
      />,
    );

    await userEvent.click(screen.getByRole("tab", { name: /^Directory/ }));
    expect(useChatSidebarStore.getState().activeTab).toBe("directory");
    expect(screen.getByRole("tab", { name: /^Directory/ })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByRole("tab", { name: /^Outline/ })).toHaveAttribute(
      "aria-selected",
      "false",
    );
  });

  it("keeps the outline page's chrome to a single row (only the top tab bar)", () => {
    render(
      <ChatContextSidebar
        sessionId={1}
        messages={[]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
      />,
    );

    expect(screen.getAllByRole("tablist")).toHaveLength(1);
  });

  it("keeps the changes page's chrome to two rows: top tab bar + scope pill group", async () => {
    render(
      <ChatContextSidebar
        sessionId={1}
        messages={[]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
        cwd="/repo"
      />,
    );

    await userEvent.click(screen.getByRole("tab", { name: /^Changes/ }));
    expect(screen.getAllByRole("tablist")).toHaveLength(2);
    const scopeBar = screen.getByRole("tablist", { name: /change scope/i });
    expect(
      within(scopeBar).getByRole("tab", { name: "This session" }),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(
        within(scopeBar).getByRole("tab", { name: "Uncommitted" }),
      ).toBeInTheDocument(),
    );
  });

  it("keeps the directory page's chrome to two rows as well", async () => {
    render(
      <ChatContextSidebar
        sessionId={1}
        messages={[]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
        cwd="/repo"
      />,
    );

    await userEvent.click(screen.getByRole("tab", { name: /^Directory/ }));
    // 目录页第二行是图标按钮行，不是 tablist：常驻 chrome 仍是两行。
    expect(screen.getAllByRole("tablist")).toHaveLength(1);
    expect(
      screen.getByRole("button", { name: /show ignored/i }),
    ).toBeInTheDocument();
  });
});
