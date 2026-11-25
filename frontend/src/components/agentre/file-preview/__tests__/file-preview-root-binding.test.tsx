import "@testing-library/jest-dom/vitest";

import { act, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  readFile: vi.fn(),
  gitFileContent: vi.fn(),
}));
vi.mock("@/../wailsjs/go/app/App", () => ({
  WorkspaceFsReadFile: (...args: unknown[]) => appMocks.readFile(...args),
  WorkspaceFsGitFileContent: (...args: unknown[]) =>
    appMocks.gitFileContent(...args),
  OpenPath: vi.fn(),
  RevealPath: vi.fn(),
}));

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

import type { chat_svc } from "@/../wailsjs/go/models";
import { useFilePreviewTabsStore } from "@/stores/file-preview-tabs-store";
import { useSessionRootStore } from "@/stores/session-root-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { FilePreviewPanel } from "../file-preview-panel";

const CWD = "/repo";
const WT = "/repo-wt";
const OTHER = "/repo-other";

function rootView(path: string, name: string, isPrimary = false) {
  return { path, name, isWorktree: !isPrimary, isPrimary, aiHere: false };
}

/** 把会话当前工作根（标题栏胶囊的那一个）切到 path。 */
function selectSessionRoot(path: string) {
  const store = useSessionRootStore.getState();
  store.applyRoots(7, CWD, [
    rootView(CWD, "repo", true),
    rootView(WT, "repo-wt"),
    rootView(OTHER, "repo-other"),
  ]);
  store.select(7, path);
}

beforeEach(() => {
  localStorage.clear();
  useSessionRootStore.setState({ bySession: {} });
  useFilePreviewTabsStore.setState({ previewTabsBySession: {} });
  useSessionStatusStore.getState().__reset();
  appMocks.readFile.mockReset();
  appMocks.readFile.mockImplementation(
    async (_sid: number, root: string, path: string) => ({
      content: `content of ${path} @ ${root || "primary"}`,
      contentType: "",
      binary: false,
      tooLarge: false,
    }),
  );
  appMocks.gitFileContent.mockReset();
});

const open = (
  root: string,
  path: string,
  mode: "directory" | "session" = "directory",
) =>
  useFilePreviewTabsStore
    .getState()
    .openPreviewInNewTab(7, { root, path }, mode);

describe("FilePreviewPanel · 标签绑定工作根", () => {
  // Given 在工作根 WT 里打开了 a.ts 的预览标签,
  // When 侧栏把会话当前工作根切到 OTHER,
  // Then 这个标签仍然从 WT 取数——内容不会悄悄换成 OTHER 下的同名文件。
  it("keeps reading an open tab from its own root when the sidebar root switches", async () => {
    selectSessionRoot(WT);
    open(WT, "a.ts");
    render(<FilePreviewPanel sessionId={7} cwd={CWD} />);
    await waitFor(() =>
      expect(appMocks.readFile).toHaveBeenCalledWith(7, WT, "a.ts"),
    );

    appMocks.readFile.mockClear();
    act(() => selectSessionRoot(OTHER));
    await act(async () => {
      await Promise.resolve();
    });

    expect(appMocks.readFile).not.toHaveBeenCalledWith(7, OTHER, "a.ts");
    expect(screen.queryByText(/@ \/repo-other/)).toBeNull();
  });

  // Given 同一相对路径在主根与 WT 下各开一个标签,
  // When 在两者之间切换,
  // Then 各自读各自根下的文件。
  it("reads the same relative path from each tab's own root", async () => {
    open("", "a.ts");
    open(WT, "a.ts");
    render(<FilePreviewPanel sessionId={7} cwd={CWD} />);
    await waitFor(() =>
      expect(appMocks.readFile).toHaveBeenCalledWith(7, WT, "a.ts"),
    );
    expect(
      useFilePreviewTabsStore.getState().previewTabsBySession[7].tabs,
    ).toHaveLength(2);

    appMocks.readFile.mockClear();
    const tabs = await screen.findAllByRole("tab");
    await act(async () => tabs[0].click());
    await waitFor(() =>
      expect(appMocks.readFile).toHaveBeenCalledWith(7, "", "a.ts"),
    );
  });

  // Given 标签的根不是会话主根, Then 标签上显示根名；主根的标签不显示。
  it("shows the root name on tabs of a non-primary root only", async () => {
    open("", "a.ts");
    open(WT, "b.ts");
    render(<FilePreviewPanel sessionId={7} cwd={CWD} />);

    const tabs = await screen.findAllByRole("tab");
    await waitFor(() =>
      expect(within(tabs[1]).getByTestId("preview-tab-root")).toHaveTextContent(
        "repo-wt",
      ),
    );
    expect(within(tabs[0]).queryByTestId("preview-tab-root")).toBeNull();
    expect(within(tabs[1]).queryByTestId("preview-tab-removed")).toBeNull();
  });

  // Given 标签所属的根已不在会话的工作根里,
  // Then 标签划掉并标「已移除」，「文件」档显示文件不存在。
  it("strikes a tab whose root was removed and shows file-not-found for the File view", async () => {
    open("", "a.ts");
    open("/repo-gone", "b.ts");
    render(<FilePreviewPanel sessionId={7} cwd={CWD} />);
    act(() =>
      useSessionRootStore
        .getState()
        .applyRoots(7, CWD, [
          rootView(CWD, "repo", true),
          rootView(WT, "repo-wt"),
        ]),
    );

    const tabs = await screen.findAllByRole("tab");
    await waitFor(() =>
      expect(
        within(tabs[1]).getByTestId("preview-tab-removed"),
      ).toHaveTextContent("Removed"),
    );
    expect(within(tabs[1]).getByText("b.ts")).toHaveClass("line-through");
    // 根集合回来之前那一次读取的结果不能留着：判成已移除后要重新取，落到不存在态。
    expect(await screen.findByText("File not found")).toBeInTheDocument();
    expect(screen.queryByText(/content of b.ts/)).toBeNull();
  });

  // Given 面板挂着时会话只认领了主根,
  // When 轮次进行中一个 worktree 被认领（chat:workroots 推送 / 点链接认领落到会话根 store）
  //      并在它里面打开一个文件,
  // Then 这个标签不是「已移除」，照它自己的根取数——不必等到轮末重取。
  it("treats a root claimed mid-turn as present without waiting for the turn end", async () => {
    useSessionRootStore
      .getState()
      .applyRoots(7, CWD, [rootView(CWD, "repo", true)]);
    open("", "a.ts");
    render(<FilePreviewPanel sessionId={7} cwd={CWD} />);
    await waitFor(() =>
      expect(appMocks.readFile).toHaveBeenCalledWith(7, "", "a.ts"),
    );
    await act(async () => {
      await Promise.resolve();
    });

    act(() =>
      useSessionRootStore
        .getState()
        .applyRoots(7, CWD, [
          rootView(CWD, "repo", true),
          rootView(WT, "repo-wt"),
        ]),
    );
    act(() => open(WT, "b.ts"));

    await waitFor(() =>
      expect(appMocks.readFile).toHaveBeenCalledWith(7, WT, "b.ts"),
    );
    const tabs = await screen.findAllByRole("tab");
    expect(within(tabs[1]).queryByTestId("preview-tab-removed")).toBeNull();
    expect(screen.queryByText("File not found")).toBeNull();
  });

  // 根集合没取到（空）≠ 根被移除：不能把标签误判成已移除。
  it("does not mark tabs removed while the claimed roots are unknown", async () => {
    open("", "a.ts");
    open("/repo-gone", "b.ts");
    render(<FilePreviewPanel sessionId={7} cwd={CWD} />);

    const tabs = await screen.findAllByRole("tab");
    await act(async () => {
      await Promise.resolve();
    });
    expect(within(tabs[1]).queryByTestId("preview-tab-removed")).toBeNull();
    expect(appMocks.readFile).toHaveBeenCalledWith(7, "/repo-gone", "b.ts");
  });

  // Given 已移除根下的「本次会话」标签,
  // Then Diff 仍按对话记录显示（归属判定用标签自己的根）。
  it("still shows the transcript Diff for a removed root's session tab", async () => {
    const messages = [
      {
        id: 1,
        role: "assistant",
        blocks: [
          {
            type: "tool_use",
            canonical: {
              kind: "file.edit",
              fileEdit: {
                files: [
                  {
                    path: "/repo-gone/b.ts",
                    kind: "modified",
                    hunks: [
                      {
                        oldStart: 1,
                        oldLines: 1,
                        newStart: 1,
                        newLines: 2,
                        lines: [
                          { op: " ", text: "keep" },
                          { op: "+", new: 2, text: "from-transcript" },
                        ],
                      },
                    ],
                    plus: 1,
                    minus: 0,
                  },
                ],
              },
            },
          },
        ],
      },
    ] as unknown as chat_svc.ChatMessage[];
    open("/repo-gone", "b.ts", "session");
    render(<FilePreviewPanel sessionId={7} cwd={CWD} messages={messages} />);

    const diff = await screen.findByTestId("replayed-file-diff");
    expect(within(diff).getByText("from-transcript")).toBeInTheDocument();
    expect(appMocks.readFile).not.toHaveBeenCalled();
  });
});
