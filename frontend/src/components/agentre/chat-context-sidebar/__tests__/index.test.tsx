import "@testing-library/jest-dom/vitest";

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { edgeClasses } from "@/__tests__/helpers/edge-classes";

const gitChangesMock = vi.fn();
const listDirMock = vi.fn();
const getAppSettingMock = vi.fn();
const openPathMock = vi.fn();
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
  GetAppSetting: (req: { key: string }) => getAppSettingMock(req),
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
// 同一份绑定的两个模块标识：组件走别名 `@/../wailsjs/...`，stores 走相对路径
// `../../wailsjs/...`（见 stores/ 下 6 个 store），vitest 按标识各记一份。本文件
// 的被测面同时跨这两侧，所以两个都要替身。
vi.mock("../../../../../wailsjs/go/app/App", () => ({
  GetAppSetting: (req: { key: string }) => getAppSettingMock(req),
  UpdateAppSettings: vi.fn(),
}));

import {
  selectActivePreviewTab,
  useFilePreviewTabsStore,
} from "@/stores/file-preview-tabs-store";
import {
  DEFAULT_FILE_SETTINGS,
  useFileSettingsStore,
} from "@/stores/file-settings-store";

import { ChatContextToolWindows } from "../index";

import {
  changesHeading,
  isToolWindowShown,
  showOnly,
  stripButton,
} from "./tool-window-helpers";

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

describe("ChatContextToolWindows", () => {
  beforeEach(() => {
    localStorage.clear();
    showOnly("outline");
    gitChangesMock.mockReset();
    gitChangesMock.mockResolvedValue(gitChangesView([]));
    listDirMock.mockReset();
    listDirMock.mockResolvedValue({
      path: "/repo",
      entries: [],
      truncated: false,
    });
    openPathMock.mockReset();
    openPathMock.mockResolvedValue(undefined);
    getAppSettingMock.mockReset();
    getAppSettingMock.mockRejectedValue(new Error("nf"));
    useFilePreviewTabsStore.setState({ previewTabsBySession: {} });
    useFileSettingsStore.setState({ settings: { ...DEFAULT_FILE_SETTINGS } });
  });

  it("按已存的 files.open_action 分流:侧栏自己把这个设置读进来", async () => {
    // 设置页不是唯一的读入口——用户可能从没进过设置页,侧栏必须自己 load 一次,
    // 否则文件面板永远按默认的内置预览分流。
    getAppSettingMock.mockResolvedValue({ value: "external" });
    listDirMock.mockResolvedValue({
      path: "/repo",
      entries: [
        { name: "main.go", isDir: false, size: 1, mtime: 0, symlink: false },
      ],
      truncated: false,
    });
    showOnly("directory");
    render(
      <ChatContextToolWindows
        sessionId={1}
        messages={[]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
        cwd="/repo"
      />,
    );

    await waitFor(() =>
      expect(getAppSettingMock).toHaveBeenCalledWith({
        key: "files.open_action",
      }),
    );
    await userEvent.click(
      await screen.findByRole("button", { name: /main\.go/ }),
    );

    expect(openPathMock).toHaveBeenCalledWith("/repo/main.go");
    expect(selectActivePreviewTab(useFilePreviewTabsStore.getState(), 1)).toBe(
      null,
    );
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("shows OutlineView by default", () => {
    render(
      <ChatContextToolWindows
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
      <ChatContextToolWindows
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
      <ChatContextToolWindows
        sessionId={1}
        messages={[userM(1, "hello world")]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
      />,
    );
    const sep = screen.getByRole("separator");
    expect(sep).toHaveAttribute("aria-orientation", "vertical");
  });

  it("Given each of the three tool windows, When shown, Then the right area sits on the chrome surface and no row draws a dividing line", async () => {
    render(
      <ChatContextToolWindows
        sessionId={1}
        messages={[userM(1, "hello world")]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
      />,
    );
    const aside = screen.getByRole("complementary", {
      name: "Right tool windows",
    });
    expect(aside).toHaveClass("bg-chrome");
    // 已打开的图标是按钮填充，不是品牌色。
    expect(stripButton("Outline")).toHaveClass("bg-button-fill");
    expect(stripButton("Outline").className).not.toMatch(/bg-primary/);
    expect(edgeClasses(aside)).toEqual([]);
    expect(
      edgeClasses(screen.getByRole("toolbar", { name: "Tool windows" })),
    ).toEqual([]);

    await userEvent.click(stripButton("Changes"));
    expect(edgeClasses(aside)).toEqual([]);

    await userEvent.click(stripButton("Directory"));
    expect(edgeClasses(aside)).toEqual([]);
  });

  it("switches to the session changes list when the Changes icon is clicked", async () => {
    render(
      <ChatContextToolWindows
        sessionId={1}
        messages={[userM(1, "hello world")]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
      />,
    );
    await userEvent.click(stripButton("Changes"));
    expect(
      screen.getByText(/No files have been changed by tools in this session/),
    ).toBeInTheDocument();
    expect(isToolWindowShown("changes")).toBe(true);
  });

  it("calls onJumpToMessage when outline row clicked", async () => {
    const onJump = vi.fn();
    render(
      <ChatContextToolWindows
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
      <ChatContextToolWindows
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
      <ChatContextToolWindows
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
      <ChatContextToolWindows
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
      <ChatContextToolWindows
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
      <ChatContextToolWindows
        sessionId={1}
        messages={[userM(11, "first"), userM(22, "second")]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
      />,
    );

    expect(scrollIntoView).not.toHaveBeenCalled();
  });
});

describe("ChatContextToolWindows · three tool windows", () => {
  beforeEach(() => {
    localStorage.clear();
    showOnly("outline");
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

  it("registers 大纲 / 变更 / 目录 / 终端 as tool windows on the strip; only the outline window is open", () => {
    render(
      <ChatContextToolWindows
        sessionId={1}
        messages={[userM(1, "first"), userM(2, "second")]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
      />,
    );

    const toolbar = screen.getByRole("toolbar", { name: "Tool windows" });
    const buttons = within(toolbar).getAllByRole("button");
    expect(buttons.map((b) => b.getAttribute("aria-label"))).toEqual([
      "Outline",
      "Changes",
      "Directory",
      "Terminal",
    ]);
    expect(buttons.map((b) => b.getAttribute("aria-pressed"))).toEqual([
      "true",
      "false",
      "false",
      "false",
    ]);
    expect(
      screen.getAllByRole("region").map((r) => r.getAttribute("aria-label")),
    ).toEqual(["Outline"]);
    // 大纲的标题只有名称，不带数量（spec：变更、关联才带数量）。
    expect(
      within(screen.getByRole("region", { name: "Outline" })).getByRole(
        "heading",
      ),
    ).toHaveTextContent(/^Outline$/);
  });

  it("counts the message-derived rows for the Changes icon without calling the backend; no badge at 0", async () => {
    render(
      <ChatContextToolWindows
        sessionId={1}
        messages={[]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
        cwd="/repo"
      />,
    );

    // 默认档是「本次会话」：数量来自消息派生，只开着大纲时不打后端。
    expect(
      within(stripButton("Changes")).queryByTestId("tool-window-badge"),
    ).toBeNull();
    expect(gitChangesMock).not.toHaveBeenCalled();

    await userEvent.click(stripButton("Changes"));
    expect(changesHeading()).toHaveTextContent(/^Changes0$/);
  });

  it("opens the directory window below the outline on click and records it in the layout store", async () => {
    render(
      <ChatContextToolWindows
        sessionId={1}
        messages={[]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
        cwd="/repo"
      />,
    );

    await userEvent.click(stripButton("Directory"));
    expect(isToolWindowShown("directory")).toBe(true);
    expect(stripButton("Directory")).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("region", { name: "Directory" })).toHaveFocus();
    // 目录在右下，不替换右上的大纲。
    expect(stripButton("Outline")).toHaveAttribute("aria-pressed", "true");
  });

  it("keeps the outline window's content free of extra chrome rows", () => {
    render(
      <ChatContextToolWindows
        sessionId={1}
        messages={[]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
      />,
    );

    expect(screen.queryAllByRole("tablist")).toHaveLength(0);
  });

  it("gives the changes window one chrome row under its title: the scope pill group", async () => {
    showOnly("changes");
    render(
      <ChatContextToolWindows
        sessionId={1}
        messages={[]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
        cwd="/repo"
      />,
    );

    expect(screen.getAllByRole("tablist")).toHaveLength(1);
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

  it("gives the directory window one chrome row under its title as well", async () => {
    showOnly("directory");
    render(
      <ChatContextToolWindows
        sessionId={1}
        messages={[]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
        cwd="/repo"
      />,
    );

    // 目录的第二行是图标按钮行，不是 tablist。
    expect(screen.queryAllByRole("tablist")).toHaveLength(0);
    expect(
      screen.getByRole("button", { name: /show ignored/i }),
    ).toBeInTheDocument();
  });
});
