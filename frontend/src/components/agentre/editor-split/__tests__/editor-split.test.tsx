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
import * as React from "react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
}));

const appMocks = vi.hoisted(() => ({
  readFile: vi.fn(),
  gitFileContent: vi.fn(),
}));
// 预览面板用别名写法、转录端口用相对写法 import 同一个绑定模块，两条都挂同一份桩。
const appModule = vi.hoisted(() => () => ({
  WorkspaceFsReadFile: (...args: unknown[]) => appMocks.readFile(...args),
  WorkspaceFsGitFileContent: (...args: unknown[]) =>
    appMocks.gitFileContent(...args),
  WorkspaceFsWorkRoots: () => Promise.resolve([]),
  OpenPath: () => Promise.resolve({}),
  RevealPath: () => Promise.resolve({}),
}));
vi.mock("@/../wailsjs/go/app/App", () => appModule());
vi.mock("../../../../../wailsjs/go/app/App", () => appModule());
vi.mock("@/lib/file-preview/monaco-loader", () => ({
  loadMonaco: vi.fn(async () => ({
    editor: {
      create: () => ({ setValue() {}, dispose() {} }),
      createDiffEditor: () => ({ setModel() {}, dispose() {} }),
      createModel: () => ({ dispose() {} }),
      setTheme() {},
    },
  })),
}));

import { TooltipProvider } from "@agentre-hub/agentre-ui";
import { useChatSidebarStore } from "@/stores/chat-sidebar-store";
import {
  DEFAULT_FILE_RATIO,
  EDITOR_SPLIT_KEY,
  useEditorSplitStore,
} from "@/stores/editor-split-store";
import { useFilePreviewTabsStore } from "@/stores/file-preview-tabs-store";
import {
  DEFAULT_FILE_SETTINGS,
  useFileSettingsStore,
} from "@/stores/file-settings-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { SessionChangesView } from "../../chat-context-sidebar/views/session-changes-view";
import type { ChangeRow } from "../../chat-context-sidebar/derive";
import { FilePreviewPanel } from "../../file-preview/file-preview-panel";
import { ShortcutsProvider } from "../../shortcuts/shortcuts-provider";
import { desktopTranscriptPorts } from "../../transcript-ports-desktop";
import { EditorSplit, EditorSplitFileActions } from "../editor-split";

const CWD = "/repo";

beforeEach(() => {
  localStorage.clear();
  useFilePreviewTabsStore.setState({ previewTabsBySession: {} });
  useEditorSplitStore.setState({
    fileRatio: DEFAULT_FILE_RATIO,
    fileFullBySession: {},
  });
  useChatSidebarStore.setState({ workRootBySession: {} });
  useFileSettingsStore.setState({ settings: { ...DEFAULT_FILE_SETTINGS } });
  useSessionStatusStore.getState().__reset();
  appMocks.readFile.mockReset();
  appMocks.readFile.mockImplementation(
    async (_sid: number, _root: string, path: string) => ({
      content: `content of ${path}`,
      contentType: "",
      binary: false,
      tooLarge: false,
    }),
  );
  appMocks.gitFileContent.mockReset();
});

function Editor({ sessionId }: { sessionId: number }) {
  return (
    <EditorSplit
      sessionId={sessionId}
      file={
        <FilePreviewPanel
          sessionId={sessionId}
          cwd={CWD}
          actions={<EditorSplitFileActions sessionId={sessionId} />}
        />
      }
    >
      <main aria-label="Chat body">
        <button type="button">chat action</button>
      </main>
    </EditorSplit>
  );
}

function Host({ children }: { children: React.ReactNode }) {
  return (
    <MemoryRouter>
      <ShortcutsProvider platform="darwin">
        <TooltipProvider>{children}</TooltipProvider>
      </ShortcutsProvider>
    </MemoryRouter>
  );
}

function renderEditor(sessionId = 1) {
  const view = render(<Editor sessionId={sessionId} />, { wrapper: Host });
  return {
    ...view,
    rerenderSession: (next: number) =>
      view.rerender(<Editor sessionId={next} />),
  };
}

const split = () => screen.getByTestId("editor-split");
const filePane = () =>
  screen.queryByRole("complementary", { name: "File preview" });
const header = () => within(filePane()!).getByTestId("file-preview-header");
const divider = () =>
  screen.queryByRole("separator", { name: "Resize chat and file" });
const openFile = (sessionId: number, path: string) =>
  act(() => {
    useFilePreviewTabsStore
      .getState()
      .openPreviewInNewTab(sessionId, path, "directory");
  });
const pressFill = (target: Element) =>
  fireEvent.keyDown(target, {
    key: '"',
    code: "Quote",
    metaKey: true,
    shiftKey: true,
  });

describe("EditorSplit · 分屏", () => {
  it("Given no file is open, Then the editor shows only the chat", () => {
    renderEditor();

    expect(screen.getByRole("main", { name: "Chat body" })).toBeInTheDocument();
    expect(split()).toHaveAttribute("data-layout", "chat");
    expect(filePane()).not.toBeInTheDocument();
    expect(divider()).not.toBeInTheDocument();
  });

  it("When a row in the changes window is clicked, Then the editor splits chat | file", async () => {
    const row: ChangeRow = {
      path: "src/a.ts",
      name: "a.ts",
      dir: "src",
      status: "modified",
      plus: 1,
      minus: 0,
      lastTurn: 1,
    };
    render(
      <SessionChangesView
        sessionId={1}
        rows={[row]}
        cwd={CWD}
        remote={false}
        onJumpToTurn={() => {}}
      />,
    );
    renderEditor();

    const el = screen
      .getAllByTestId(/-row$/)
      .find((e) => e.getAttribute("data-name") === "a.ts")!;
    await userEvent
      .setup({ pointerEventsCheck: 0 })
      .click(within(el).getByText("a.ts"));

    expect(split()).toHaveAttribute("data-layout", "split");
    // 左对话、右文件，中间一条分隔条。
    const children = Array.from(split().children);
    const chatIdx = children.findIndex((c) =>
      c.contains(screen.getByRole("main", { name: "Chat body" })),
    );
    const fileIdx = children.findIndex((c) => c.contains(filePane()));
    expect(chatIdx).toBeGreaterThanOrEqual(0);
    expect(fileIdx).toBeGreaterThan(chatIdx);
    expect(divider()).toBeInTheDocument();
  });

  it("When a file link in the transcript is clicked, Then the editor splits and shows that file", async () => {
    renderEditor();

    act(() => {
      expect(desktopTranscriptPorts.previewFile(1, "src/b.ts")).toBe(true);
    });

    expect(split()).toHaveAttribute("data-layout", "split");
    expect(header()).toHaveTextContent("b.ts");
    await waitFor(() =>
      expect(appMocks.readFile).toHaveBeenCalledWith(1, "", "src/b.ts"),
    );
  });

  it("When the last file tab is closed, Then the split collapses", async () => {
    renderEditor();
    openFile(1, "a.ts");
    openFile(1, "b.ts");

    const tabs = within(filePane()!).getByRole("tablist");
    await userEvent.click(
      within(tabs).getAllByRole("button", { name: "Close Tab" })[0],
    );
    expect(split()).toHaveAttribute("data-layout", "split");
    // 只剩一个标签时标签条收起，关的是 header 上那颗关闭键。
    await userEvent.click(
      within(header()).getByRole("button", { name: "Close preview" }),
    );

    await waitFor(() => expect(filePane()).not.toBeInTheDocument());
    expect(split()).toHaveAttribute("data-layout", "chat");
    expect(divider()).not.toBeInTheDocument();
  });

  it("When 关闭全部 is clicked, Then every tab closes and the split collapses", async () => {
    renderEditor();
    openFile(1, "a.ts");
    openFile(1, "b.ts");

    await userEvent.click(
      screen.getByRole("button", { name: "Close all files" }),
    );

    expect(filePane()).not.toBeInTheDocument();
    expect(split()).toHaveAttribute("data-layout", "chat");
    expect(useFilePreviewTabsStore.getState().previewTabsBySession[1]).toBe(
      undefined,
    );
  });

  it("When the divider is dragged, Then the ratio follows, is remembered and restored", async () => {
    const { unmount } = renderEditor();
    openFile(1, "a.ts");
    vi.spyOn(split(), "getBoundingClientRect").mockReturnValue({
      left: 0,
      right: 1000,
      width: 1000,
      top: 0,
      bottom: 600,
      height: 600,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    });

    fireEvent.pointerDown(divider()!, { button: 0, clientX: 500 });
    fireEvent.pointerMove(document, { clientX: 700 });
    fireEvent.pointerUp(document, { clientX: 700 });

    // 文件一侧占 (1000 - 700) / 1000。
    expect(useEditorSplitStore.getState().fileRatio).toBeCloseTo(0.3);
    expect(divider()).toHaveAttribute("aria-valuenow", "30");
    expect(
      JSON.parse(localStorage.getItem(EDITOR_SPLIT_KEY)!).state.fileRatio,
    ).toBeCloseTo(0.3);

    unmount();
    // 模拟重启：内存回到默认值，存储里仍是拖过的那份。
    const saved = localStorage.getItem(EDITOR_SPLIT_KEY)!;
    useEditorSplitStore.setState({ fileRatio: DEFAULT_FILE_RATIO });
    localStorage.setItem(EDITOR_SPLIT_KEY, saved);
    await act(() => useEditorSplitStore.persist.rehydrate());
    renderEditor();
    expect(divider()).toHaveAttribute("aria-valuenow", "30");
  });

  it("Given the divider has focus, When arrow keys are pressed, Then the file side grows and shrinks", () => {
    renderEditor();
    openFile(1, "a.ts");

    fireEvent.keyDown(divider()!, { key: "ArrowLeft" });
    expect(useEditorSplitStore.getState().fileRatio).toBeCloseTo(0.52);
    fireEvent.keyDown(divider()!, { key: "ArrowRight" });
    fireEvent.keyDown(divider()!, { key: "ArrowRight" });
    expect(useEditorSplitStore.getState().fileRatio).toBeCloseTo(0.48);
  });
});

describe("EditorSplit · 文件占满", () => {
  it("When 占满 is clicked, Then the file fills the editor; 还原 returns to the split", async () => {
    renderEditor();
    openFile(1, "a.ts");

    await userEvent.click(screen.getByRole("button", { name: "Fill editor" }));
    expect(split()).toHaveAttribute("data-layout", "file-full");
    expect(divider()).not.toBeInTheDocument();
    // 对话仍在（缩成窄栏），没有被卸载。
    expect(screen.getByRole("main", { name: "Chat body" })).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Restore" }));
    expect(split()).toHaveAttribute("data-layout", "split");
    expect(divider()).toBeInTheDocument();
  });

  it("Given focus is in the file side, When ⇧⌘' is pressed, Then file-full toggles", () => {
    renderEditor();
    openFile(1, "a.ts");
    pressFill(header());
    expect(split()).toHaveAttribute("data-layout", "file-full");
    pressFill(header());
    expect(split()).toHaveAttribute("data-layout", "split");
  });

  it("Given focus is in the chat of a split, When ⇧⌘' is pressed, Then nothing changes", () => {
    renderEditor();
    openFile(1, "a.ts");

    pressFill(screen.getByRole("button", { name: "chat action" }));
    expect(split()).toHaveAttribute("data-layout", "split");
  });

  it("Given file-full, When ⇧⌘' is pressed in the chat rail, Then it returns to the split", () => {
    renderEditor();
    openFile(1, "a.ts");
    act(() => useEditorSplitStore.getState().toggleFileFull(1));

    pressFill(screen.getByRole("button", { name: "chat action" }));
    expect(split()).toHaveAttribute("data-layout", "split");
  });
});

describe("EditorSplit · 按会话", () => {
  it("When switching sessions, Then each session shows its own tabs and layout", () => {
    openFile(1, "one.ts");
    act(() => useEditorSplitStore.getState().toggleFileFull(1));
    const { rerenderSession } = renderEditor(2);

    expect(split()).toHaveAttribute("data-layout", "chat");
    expect(filePane()).not.toBeInTheDocument();

    openFile(2, "two.ts");
    expect(split()).toHaveAttribute("data-layout", "split");
    expect(header()).toHaveTextContent("two.ts");

    rerenderSession(1);
    expect(split()).toHaveAttribute("data-layout", "file-full");
    expect(header()).toHaveTextContent("one.ts");
    expect(header()).not.toHaveTextContent("two.ts");
  });
});
