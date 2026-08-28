import "@testing-library/jest-dom/vitest";

import { render, screen, within } from "@testing-library/react";

import { beforeEach, describe, expect, it, vi } from "vitest";

const readFileMock = vi.fn();
const gitFileContentMock = vi.fn();
vi.mock("@/../wailsjs/go/app/App", () => ({
  WorkspaceFsReadFile: (...args: unknown[]) => readFileMock(...args),
  WorkspaceFsGitFileContent: (...args: unknown[]) =>
    gitFileContentMock(...args),
}));

// Monaco 在 happy-dom 里跑不起来。「本次会话」档不该走到它,这个 mock 只是保证
// 万一走到了,失败是断言不成立而不是加载器炸掉。
const loaderMocks = vi.hoisted(() => ({ loadMonaco: vi.fn() }));
vi.mock("@/lib/file-preview/monaco-loader", () => loaderMocks);

import type { chat_svc } from "@/../wailsjs/go/models";
import { useChatSidebarStore } from "@/stores/chat-sidebar-store";
import { useFilePreviewTabsStore } from "@/stores/file-preview-tabs-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { FilePreviewPanel } from "../file-preview-panel";

type Msg = chat_svc.ChatMessage;
type Line = { op: string; old?: number; new?: number; text: string };

const CWD = "/w";

let nextId = 1;

function message(block: unknown): Msg {
  return {
    id: nextId++,
    role: "assistant",
    blocks: [block],
  } as unknown as Msg;
}

/** 一次 file.edit 调用:hunk 自带改动前后的内容,是**增量**。 */
function editCall(
  path: string,
  lines: Line[],
  extra: Record<string, unknown> = {},
): Msg {
  return message({
    type: "tool_use",
    canonical: {
      kind: "file.edit",
      fileEdit: {
        files: [
          {
            path,
            kind: "modified",
            hunks: [
              {
                oldStart: 1,
                oldLines: lines.filter((l) => l.op !== "+").length,
                newStart: 1,
                newLines: lines.filter((l) => l.op !== "-").length,
                lines,
              },
            ],
            plus: lines.filter((l) => l.op === "+").length,
            minus: lines.filter((l) => l.op === "-").length,
            ...extra,
          },
        ],
      },
    },
  });
}

/** 一次 file.write 调用:该步之后文件内容正好等于 content,是**绝对状态**。 */
function writeCall(
  path: string,
  content: string,
  extra: Record<string, unknown> = {},
): Msg {
  return message({
    type: "tool_use",
    canonical: {
      kind: "file.write",
      fileWrite: {
        path,
        content,
        lines: content.split("\n").filter((l) => l !== "").length,
        bytes: content.length,
        ...extra,
      },
    },
  });
}

function ctx(op: string, text: string): Line {
  return { op, text };
}

beforeEach(() => {
  nextId = 1;
  localStorage.clear();
  useChatSidebarStore.setState({
    open: true,
    activeTab: "changes",
    changesScope: "session",
    showIgnored: false,
    // 工作根是每会话一条的运行期状态:不在这里清掉,上一个用例设过的根会跟着
    // 进下一个用例,把它的重放归属判到别的根上。
    workRootBySession: {},
  });
  useFilePreviewTabsStore.setState({ previewTabsBySession: {} });
  useSessionStatusStore.getState().__reset();
  // 给两个后端读取一个可用的默认返回:「本次会话」档本就不该调它们,失败要落在
  // 「没有重放出的 diff」这条断言上,而不是 mock 返回 undefined 的崩溃。
  readFileMock.mockReset();
  readFileMock.mockResolvedValue({
    content: "",
    contentType: "",
    binary: false,
    tooLarge: false,
  });
  gitFileContentMock.mockReset();
  gitFileContentMock.mockResolvedValue({ content: "", notARepo: false });
  loaderMocks.loadMonaco.mockReset();
});

function renderToolDiff(messages: Msg[], path = "app/x.ts") {
  useFilePreviewTabsStore.getState().openPreview(7, path, "session");
  return render(
    <FilePreviewPanel sessionId={7} messages={messages} cwd={CWD} />,
  );
}

describe("FilePreviewPanel · 「本次会话」的工具 diff", () => {
  // Given 同一个文件被两次工具调用改过(第二次改的是第一次改完的内容),
  // When 从「本次会话」点开这一行,
  // Then 看到的是重放出的**一个连续 diff**:动手前 → 最后一次动完,
  //      中间态不出现;并且这一档不读文件、不读 git。
  it("replays every tool call on the file into one continuous diff", async () => {
    const messages = [
      // 绝对路径与相对路径指向同一个文件,两次都要算进去。
      editCall(`${CWD}/app/x.ts`, [
        ctx(" ", "keep"),
        { op: "-", old: 2, text: "one" },
        { op: "+", new: 2, text: "two" },
      ]),
      editCall("app/x.ts", [
        ctx(" ", "keep"),
        { op: "-", old: 2, text: "two" },
        { op: "+", new: 2, text: "three" },
      ]),
    ];

    renderToolDiff(messages);

    const panel = await screen.findByRole("complementary", {
      name: "File preview",
    });
    const diff = await within(panel).findByTestId("replayed-file-diff");
    expect(within(diff).getByText("one")).toBeInTheDocument();
    expect(within(diff).getByText("three")).toBeInTheDocument();
    // 中间态 "two" 出现 = 两次调用被首尾拼接而不是重放。
    expect(within(diff).queryByText("two")).toBeNull();

    expect(readFileMock).not.toHaveBeenCalled();
    expect(gitFileContentMock).not.toHaveBeenCalled();
  });

  // Given 多工作根会话:同一条相对路径在会话 cwd 与 worktree 里各有一个文件,
  // When 侧栏当前的工作根是那个 worktree,
  // Then 重放收的是 worktree 那个文件的调用 —— 与「变更」行的归属判定同源,
  //      而不是退回会话 cwd 去收另一个文件的调用。
  it("collects the calls of the current work root, not of the session cwd", async () => {
    const WT = "/w-ia";
    useChatSidebarStore.getState().setWorkRoot(7, WT);

    renderToolDiff([
      editCall(`${CWD}/app/x.ts`, [
        ctx(" ", "keep"),
        { op: "+", new: 2, text: "in-session-cwd" },
      ]),
      editCall(`${WT}/app/x.ts`, [
        ctx(" ", "keep"),
        { op: "+", new: 2, text: "in-worktree" },
      ]),
    ]);

    const diff = await screen.findByTestId("replayed-file-diff");
    expect(within(diff).getByText("in-worktree")).toBeInTheDocument();
    expect(within(diff).queryByText("in-session-cwd")).toBeNull();
  });

  // Given 该文件在本会话里的首个操作是全量写入,
  // When 点开这一行,
  // Then 整篇按新增呈现,并显式标注它没有与写入前的内容比较(spec 决策 14)。
  it("labels a leading whole-file write instead of faking a comparison", async () => {
    renderToolDiff([writeCall(`${CWD}/app/x.ts`, "alpha\nbeta\n")]);

    const diff = await screen.findByTestId("replayed-file-diff");
    expect(within(diff).getByText("alpha")).toBeInTheDocument();
    expect(
      within(diff).getByText(
        /not compared against the content before the write/i,
      ),
    ).toBeInTheDocument();
  });

  // Given 某次调用被产出方截断,When 点开这一行,Then 明确告知这次改动不完整。
  it("says the diff is incomplete when a call was truncated", async () => {
    renderToolDiff([
      editCall(
        "app/x.ts",
        [ctx(" ", "keep"), { op: "+", new: 2, text: "added" }],
        { truncated: true },
      ),
    ]);

    const diff = await screen.findByTestId("replayed-file-diff");
    expect(within(diff).getByText(/incomplete/i)).toBeInTheDocument();
  });

  // Given 增量改动之后又来一次全量写入(重放合不出一致结果),
  // When 点开这一行,
  // Then 降级为按调用顺序分段列出,并说明为什么没能合并 —— 不是一个空 diff。
  it("degrades to per-call segments with a stated reason, never an empty diff", async () => {
    renderToolDiff([
      editCall("app/x.ts", [
        ctx(" ", "keep"),
        { op: "-", old: 2, text: "one" },
        { op: "+", new: 2, text: "two" },
      ]),
      writeCall("app/x.ts", "rewritten\n"),
    ]);

    const diff = await screen.findByTestId("replayed-file-diff");
    expect(
      within(diff).getByText(/could not be merged into one continuous diff/i),
    ).toBeInTheDocument();
    expect(
      within(diff).getByText(
        /whole-file write landed after incremental edits/i,
      ),
    ).toBeInTheDocument();
    expect(
      within(diff).getAllByTestId("replayed-file-diff-segment"),
    ).toHaveLength(2);
    // 分段里两次改动的内容都还在。
    expect(within(diff).getByText("one")).toBeInTheDocument();
    expect(within(diff).getByText("rewritten")).toBeInTheDocument();
  });

  // Given 工作根子树之外的调用(spec「归属过滤」),When 点开这一行,
  // Then 它不参与重放,面板明说本会话没有工具改过这个文件 —— 不是一个空 diff。
  it("says no tool call touched the file instead of showing an empty diff", async () => {
    renderToolDiff([
      editCall("/tmp/x.ts", [
        ctx(" ", "keep"),
        { op: "+", new: 2, text: "outside" },
      ]),
    ]);

    const panel = await screen.findByRole("complementary", {
      name: "File preview",
    });
    expect(
      within(panel).getByText(
        /no tool call in this session changed this file/i,
      ),
    ).toBeInTheDocument();
    expect(within(panel).queryByText("outside")).toBeNull();
    expect(readFileMock).not.toHaveBeenCalled();
  });
});
