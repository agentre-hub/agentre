import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi, type Mock } from "vitest";

import {
  FilePreviewPanel,
  type FilePreviewPanelProps,
} from "./file-preview-panel";
import type { MonacoNS } from "./monaco";
import type { FilePreviewPorts } from "./ports";

/**
 * 预览面板搬进包之后的契约：**读取走注入的端口、状态走 props**，包内既不 import
 * Wails 绑定也不 import 宿主 store。这份用例把七个态逐个钉在面板自己渲染出来的
 * 东西上：四类内容各自渲染，`tooLarge` / `binary` / 文件不存在三种终态各出各自的
 * 说明且**不带动作按钮**，只有「对端离线」那一态给重试。
 */

type FakeMonaco = {
  editor: {
    create: ReturnType<typeof vi.fn>;
    createDiffEditor: ReturnType<typeof vi.fn>;
    createModel: ReturnType<typeof vi.fn>;
    setTheme: ReturnType<typeof vi.fn>;
  };
};

function createFakeMonaco(): FakeMonaco {
  return {
    editor: {
      setTheme: vi.fn(),
      create: vi.fn((_el: HTMLElement, options: Record<string, unknown>) => ({
        options,
        setValue: vi.fn(),
        dispose: vi.fn(),
      })),
      createDiffEditor: vi.fn(
        (_el: HTMLElement, options: Record<string, unknown>) => ({
          options,
          setModel: vi.fn(),
          dispose: vi.fn(),
        }),
      ),
      createModel: vi.fn((value: string, lang: string) => ({
        value,
        lang,
        dispose: vi.fn(),
      })),
    },
  };
}

function textView(content: string) {
  return { content, contentType: "", binary: false, tooLarge: false };
}

let fakeMonaco: FakeMonaco;
let readFile: Mock<FilePreviewPorts["readFile"]>;
let gitFileContent: Mock<FilePreviewPorts["gitFileContent"]>;

beforeEach(() => {
  fakeMonaco = createFakeMonaco();
  readFile = vi.fn<FilePreviewPorts["readFile"]>();
  readFile.mockResolvedValue(textView(""));
  gitFileContent = vi.fn<FilePreviewPorts["gitFileContent"]>();
  gitFileContent.mockResolvedValue({
    content: "",
    notARepo: false,
    hasHead: true,
  });
});

function renderPanel(overrides: Partial<FilePreviewPanelProps> = {}) {
  const handlers = {
    onSegmentChange: vi.fn(),
    onActivate: vi.fn(),
    onPromote: vi.fn(),
    onPin: vi.fn(),
    onClose: vi.fn(),
    onCloseOthers: vi.fn(),
    onCloseAll: vi.fn(),
  };
  const path = overrides.activePath ?? "README.md";
  const props: FilePreviewPanelProps = {
    tabs: [{ path, isPreview: false, isPinned: false }],
    activePath: path,
    segment: null,
    sourceMode: "directory",
    ports: {
      readFile: (p: string) => readFile(p),
      gitFileContent: (p: string) => gitFileContent(p),
    },
    sourceKey: "7\n/w",
    monaco: fakeMonaco as unknown as MonacoNS,
    ...handlers,
    ...overrides,
  };
  const view = render(<FilePreviewPanel {...props} />);
  return { ...handlers, ...view, props };
}

// 面板**不带容器**（spec 决策 3：容器是宿主的布局问题），所以这里抓的是面板根
// 本身，而不是某个宿主才会给的 landmark。
function panel() {
  return screen.findByTestId("file-preview-panel");
}

describe("FilePreviewPanel", () => {
  it("renders nothing and reads nothing while no file is active", () => {
    const { container } = renderPanel({ tabs: [], activePath: null });

    expect(container).toBeEmptyDOMElement();
    expect(readFile).not.toHaveBeenCalled();
  });

  // ── 四类成功渲染 ────────────────────────────────────────────────────────
  it("renders a code file through the injected readFile port and Monaco", async () => {
    readFile.mockResolvedValue(textView("package main"));
    renderPanel({ activePath: "main.go" });

    await panel();
    expect(readFile).toHaveBeenCalledWith("main.go");
    await waitFor(() =>
      expect(fakeMonaco.editor.create).toHaveBeenCalledWith(
        expect.any(HTMLElement),
        expect.objectContaining({ language: "go", readOnly: true }),
      ),
    );
  });

  it("renders markdown as GFM by default and as raw source in the text segment", async () => {
    readFile.mockResolvedValue(textView("**bold**"));
    const { onSegmentChange, rerender, props } = renderPanel({
      activePath: "README.md",
    });

    const view = await panel();
    await within(view).findByText("bold");

    await userEvent.click(within(view).getByRole("button", { name: "Text" }));
    expect(onSegmentChange).toHaveBeenCalledWith("text");

    rerender(<FilePreviewPanel {...props} segment="text" />);
    await waitFor(() =>
      expect(fakeMonaco.editor.create).toHaveBeenCalledWith(
        expect.any(HTMLElement),
        expect.objectContaining({ language: "markdown", readOnly: true }),
      ),
    );
  });

  it("renders an image as a data URL with the file name as alt text", async () => {
    readFile.mockResolvedValue({
      content: "aGVsbG8=",
      contentType: "image/png",
      binary: false,
      tooLarge: false,
    });
    renderPanel({ activePath: "assets/logo.png" });

    const view = await panel();
    expect(await within(view).findByAltText("logo.png")).toHaveAttribute(
      "src",
      "data:image/png;base64,aGVsbG8=",
    );
  });

  it("renders a git-opened code file as a diff against the HEAD version", async () => {
    readFile.mockResolvedValue(textView("package main\n"));
    gitFileContent.mockResolvedValue({
      content: "package main\n// old\n",
      notARepo: false,
      hasHead: true,
    });
    renderPanel({ activePath: "main.go", sourceMode: "git" });

    const view = await panel();
    await waitFor(() => expect(gitFileContent).toHaveBeenCalledWith("main.go"));
    expect(
      within(view).getByText("Diff · HEAD → working tree"),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(fakeMonaco.editor.createDiffEditor).toHaveBeenCalled(),
    );
  });

  // ── 三个终态：各出各自的说明，都不带动作 ────────────────────────────────
  it("states a too-large file with its own hint and offers no action", async () => {
    readFile.mockResolvedValue({
      content: "",
      contentType: "",
      binary: false,
      tooLarge: true,
    });
    renderPanel({ activePath: "photo.jpg" });

    const view = await panel();
    expect(
      await within(view).findByText(/File too large to preview/),
    ).toBeInTheDocument();
    expect(within(view).getByText(/Image larger than 10 MiB/)).toBeVisible();
    expect(within(view).queryByRole("button", { name: /Retry/i })).toBeNull();
  });

  it("states a binary file with its own hint and offers no action", async () => {
    readFile.mockResolvedValue({
      content: "",
      contentType: "",
      binary: true,
      tooLarge: false,
    });
    renderPanel({ activePath: "archive.bin" });

    const view = await panel();
    expect(
      await within(view).findByText(/Binary file, cannot preview/),
    ).toBeInTheDocument();
    expect(within(view).queryByRole("button", { name: /Retry/i })).toBeNull();
  });

  it("states a deleted file and offers no action", async () => {
    readFile.mockRejectedValue(
      Object.assign(new Error("stat: no such file"), { kind: "notFound" }),
    );
    renderPanel({ activePath: "gone.ts" });

    const view = await panel();
    expect(await within(view).findByText("File not found")).toBeInTheDocument();
    expect(within(view).queryByRole("button", { name: /Retry/i })).toBeNull();
  });

  // ── 唯一带动作的那一态 ──────────────────────────────────────────────────
  it("says the machine is unreachable and retries the read on demand", async () => {
    readFile.mockRejectedValueOnce(
      Object.assign(new Error("device offline"), { kind: "offline" }),
    );
    readFile.mockResolvedValue(textView("# back"));
    renderPanel({ activePath: "README.md" });

    const view = await panel();
    expect(
      await within(view).findByText("This machine is unreachable right now"),
    ).toBeInTheDocument();

    await userEvent.click(within(view).getByRole("button", { name: /Retry/i }));
    expect(readFile).toHaveBeenCalledTimes(2);
    await within(view).findByRole("heading", { level: 1, name: "back" });
  });

  it("surfaces an unclassified read failure with its own message and a retry", async () => {
    readFile.mockRejectedValue(new Error("Path is outside the working dir"));
    renderPanel({ activePath: "README.md" });

    const view = await panel();
    expect(
      await within(view).findByText("Path is outside the working dir"),
    ).toBeInTheDocument();
    expect(
      within(view).getByRole("button", { name: /Retry/i }),
    ).toBeInTheDocument();
  });

  // ── 装配：标签条与关闭都经注入的回调 ────────────────────────────────────
  it("renders the shared tab strip and reports activation through the injected callback", async () => {
    readFile.mockResolvedValue(textView("# hi"));
    const { onActivate } = renderPanel({
      activePath: "b.md",
      tabs: [
        { path: "docs/a.md", isPreview: false, isPinned: false },
        { path: "b.md", isPreview: true, isPinned: false },
      ],
    });

    const view = await panel();
    const strip = within(view).getByRole("tablist", { name: "Preview tabs" });
    await userEvent.click(within(strip).getAllByRole("tab")[0]);
    expect(onActivate).toHaveBeenCalledWith("docs/a.md");
  });

  it("closes the active file through the injected callback", async () => {
    readFile.mockResolvedValue(textView("# hi"));
    const { onClose } = renderPanel({ activePath: "README.md" });

    const view = await panel();
    await userEvent.click(
      within(view).getByRole("button", { name: "Close preview" }),
    );
    await waitFor(() => expect(onClose).toHaveBeenCalledWith("README.md"));
  });

  it("re-reads the file when the host bumps the refresh token", async () => {
    readFile.mockResolvedValue(textView("# v1"));
    const { rerender, props } = renderPanel({ activePath: "README.md" });

    await panel();
    await waitFor(() => expect(readFile).toHaveBeenCalledTimes(1));

    rerender(<FilePreviewPanel {...props} refreshToken={1} />);
    await waitFor(() => expect(readFile).toHaveBeenCalledTimes(2));
  });
});

// ── 内容来自哪台机器 ────────────────────────────────────────────────────────

// 控制台里预览的每一个文件都在**别人的机器**上（设计源 B1 的路径条右端画了一枚
// 状态点 + 机器名）。桌面端的会话外壳已经带机器名，所以它不传 —— 不传就不渲染，
// 与 previewFile 同一套能力探测约定。
describe("路径条上的设备指示", () => {
  it("宿主给了机器名就画出来，并说明它此刻在不在线", async () => {
    renderPanel({ deviceName: "dev-box", deviceOnline: true });

    expect(await screen.findByText("dev-box")).toBeTruthy();
    expect(screen.getByTestId("file-preview-device")).toHaveAttribute(
      "data-online",
      "true",
    );
  });

  it("离线时同一格如实标成离线，而不是消失", async () => {
    renderPanel({ deviceName: "dev-box", deviceOnline: false });

    expect(await screen.findByText("dev-box")).toBeTruthy();
    expect(screen.getByTestId("file-preview-device")).toHaveAttribute(
      "data-online",
      "false",
    );
  });

  it("宿主不给机器名时这一格整个不渲染", async () => {
    renderPanel();
    await panel();

    expect(screen.queryByTestId("file-preview-device")).toBeNull();
  });
});
