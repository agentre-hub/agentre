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
        getModel: vi.fn(() => ({
          getLineCount: () => 100,
          getLineMaxColumn: () => 1,
        })),
        revealLineInCenter: vi.fn(),
        setSelection: vi.fn(),
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

function lastEditor() {
  const results = fakeMonaco.editor.create.mock.results;
  return results[results.length - 1]?.value as {
    revealLineInCenter: Mock;
  };
}

// 面板**不带容器**（spec 决策 3：容器是宿主的布局问题），所以这里抓的是面板根
// 本身，而不是某个宿主才会给的 landmark。
function panel() {
  return screen.findByTestId("file-preview-panel");
}

describe("FilePreviewPanel reveal target", () => {
  it("Given a code file and a reveal target, when the content lands, then the editor is scrolled to that line", async () => {
    readFile.mockResolvedValue(textView("package main"));
    renderPanel({
      activePath: "main.go",
      revealTarget: { line: 12, endLine: 20, nonce: 1 },
    });

    await panel();
    await waitFor(() =>
      expect(lastEditor().revealLineInCenter).toHaveBeenCalledWith(12),
    );
  });

  it("Given markdown in the text segment, when a reveal target is present, then the source view is scrolled too", async () => {
    readFile.mockResolvedValue(textView("# title"));
    renderPanel({
      activePath: "docs/guide.md",
      segment: "text",
      revealTarget: { line: 8, endLine: 9, nonce: 1 },
    });

    await panel();
    await waitFor(() =>
      expect(lastEditor().revealLineInCenter).toHaveBeenCalledWith(8),
    );
  });

  it("Given markdown left in the render segment, when a reveal target is present, then nothing is scrolled because that segment has no lines", async () => {
    readFile.mockResolvedValue(textView("# title"));
    renderPanel({
      activePath: "docs/guide.md",
      segment: "render",
      revealTarget: { line: 8, endLine: 9, nonce: 1 },
    });

    const view = await panel();
    await within(view).findByText("title");
    expect(fakeMonaco.editor.create).not.toHaveBeenCalled();
  });

  it("Given the session tool-diff segment, when a reveal target is present, then it is ignored", async () => {
    renderPanel({
      activePath: "main.go",
      sourceMode: "session",
      revealTarget: { line: 12, nonce: 1 },
    });

    await panel();
    expect(fakeMonaco.editor.create).not.toHaveBeenCalled();
  });
});

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

  // 这枚点由**这一次读取**说了算，宿主的声称只做兜底：两端宿主手里那些设备记录
  // 都是慢节奏探测（控制台上实测停掉那台机器 90 秒仍说在线），会造出「内容区说
  // 够不着、点却还绿着」的自相矛盾。
  it("读失败在 offline 上时画成离线，哪怕宿主还声称在线", async () => {
    readFile.mockRejectedValue(
      Object.assign(new Error("够不着"), { kind: "offline" }),
    );
    renderPanel({ deviceName: "dev-box", deviceOnline: true });

    expect(await screen.findByText("dev-box")).toBeTruthy();
    expect(screen.getByTestId("file-preview-device")).toHaveAttribute(
      "data-online",
      "false",
    );
  });

  it("读成功就证明够得着：宿主声称离线也画成在线", async () => {
    renderPanel({ deviceName: "dev-box", deviceOnline: false });

    expect(await screen.findByText("dev-box")).toBeTruthy();
    expect(screen.getByTestId("file-preview-device")).toHaveAttribute(
      "data-online",
      "true",
    );
  });

  it("没有新证据时（别的失败）用宿主给的那个值", async () => {
    readFile.mockRejectedValue(new Error("说不清的失败"));
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

// ── HTML：渲染 / 源码 / 双栏（spec「HTML 预览」）────────────────────────────
describe("FilePreviewPanel HTML", () => {
  const HTML = "<h1>Report</h1><script>1</script>";

  function frame(view: HTMLElement) {
    return within(view).queryByTitle("report.html rendered page");
  }

  it("Given an HTML file opened from the directory, when no segment is stored, then it renders in a sandboxed frame that may run scripts but gets no same-origin, popup or top-navigation rights", async () => {
    readFile.mockResolvedValue(textView(HTML));
    renderPanel({ activePath: "out/report.html" });

    const view = await panel();
    const iframe = await within(view).findByTitle("report.html rendered page");
    expect(iframe.getAttribute("srcdoc")).toBe(HTML);
    expect(iframe.getAttribute("sandbox")).toBe("allow-scripts");
    expect(
      within(view)
        .getByRole("button", { name: "Render" })
        .getAttribute("aria-pressed"),
    ).toBe("true");
    within(view).getByRole("button", { name: "Source" });
    within(view).getByRole("button", { name: "Split" });
    expect(fakeMonaco.editor.create).not.toHaveBeenCalled();
  });

  it("Given an HTML file, when the user picks Source, then the stored segment becomes text and the source shows in Monaco as html", async () => {
    readFile.mockResolvedValue(textView(HTML));
    const { onSegmentChange, rerender, props } = renderPanel({
      activePath: "out/report.html",
    });
    const view = await panel();
    await within(view).findByTitle("report.html rendered page");

    await userEvent.click(within(view).getByRole("button", { name: "Source" }));
    expect(onSegmentChange).toHaveBeenCalledWith("text");

    rerender(<FilePreviewPanel {...props} segment="text" />);
    await waitFor(() =>
      expect(fakeMonaco.editor.create).toHaveBeenCalledWith(
        expect.any(HTMLElement),
        expect.objectContaining({ language: "html", readOnly: true }),
      ),
    );
    expect(frame(view)).toBeNull();
  });

  it("Given an HTML file in the split segment, then source and rendered page show side by side", async () => {
    readFile.mockResolvedValue(textView(HTML));
    renderPanel({ activePath: "out/report.html", segment: "split" });

    const view = await panel();
    await within(view).findByTitle("report.html rendered page");
    await waitFor(() =>
      expect(fakeMonaco.editor.create).toHaveBeenCalledWith(
        expect.any(HTMLElement),
        expect.objectContaining({ language: "html" }),
      ),
    );
  });

  it("Given an HTML file opened from uncommitted changes, when no segment is stored, then the first view is still the HEAD diff in the Source segment", async () => {
    readFile.mockResolvedValue(textView(HTML));
    renderPanel({ activePath: "out/report.html", sourceMode: "git" });

    const view = await panel();
    await waitFor(() =>
      expect(fakeMonaco.editor.createDiffEditor).toHaveBeenCalled(),
    );
    expect(gitFileContent).toHaveBeenCalledWith("out/report.html");
    expect(
      within(view)
        .getByRole("button", { name: "Source" })
        .getAttribute("aria-pressed"),
    ).toBe("true");
    expect(frame(view)).toBeNull();
  });

  it("Given an HTML file from uncommitted changes in the split segment, then the left side is the HEAD diff and the right side the rendered page", async () => {
    readFile.mockResolvedValue(textView(HTML));
    renderPanel({
      activePath: "out/report.html",
      sourceMode: "git",
      segment: "split",
    });

    const view = await panel();
    await within(view).findByTitle("report.html rendered page");
    await waitFor(() =>
      expect(fakeMonaco.editor.createDiffEditor).toHaveBeenCalled(),
    );
  });

  it("Given an HTML file from uncommitted changes, when the user picks Render, then the rendered page shows instead of the diff", async () => {
    readFile.mockResolvedValue(textView(HTML));
    renderPanel({
      activePath: "out/report.html",
      sourceMode: "git",
      segment: "render",
    });

    const view = await panel();
    await within(view).findByTitle("report.html rendered page");
    expect(fakeMonaco.editor.createDiffEditor).not.toHaveBeenCalled();
  });

  it("Given the rendered segment, when the user clicks Re-render, then the frame is reloaded without reading the file again", async () => {
    readFile.mockResolvedValue(textView(HTML));
    renderPanel({ activePath: "out/report.html" });

    const view = await panel();
    const before = await within(view).findByTitle("report.html rendered page");
    expect(readFile).toHaveBeenCalledTimes(1);

    await userEvent.click(
      within(view).getByRole("button", { name: "Re-render" }),
    );
    const after = await within(view).findByTitle("report.html rendered page");
    expect(after).not.toBe(before);
    expect(readFile).toHaveBeenCalledTimes(1);
  });

  it("Given the source segment, then there is no Re-render button", async () => {
    readFile.mockResolvedValue(textView(HTML));
    renderPanel({ activePath: "out/report.html", segment: "text" });

    const view = await panel();
    await waitFor(() => expect(fakeMonaco.editor.create).toHaveBeenCalled());
    expect(
      within(view).queryByRole("button", { name: "Re-render" }),
    ).toBeNull();
  });

  it("Given the host can open files in the system, when the user clicks Open in browser on an HTML tab, then the host is asked to open that path", async () => {
    readFile.mockResolvedValue(textView(HTML));
    const openPath = vi.fn().mockResolvedValue(undefined);
    renderPanel({
      activePath: "out/report.html",
      ports: {
        readFile: (p: string) => readFile(p),
        gitFileContent: (p: string) => gitFileContent(p),
        openPath,
      },
    });

    const view = await panel();
    await userEvent.click(
      await within(view).findByRole("button", { name: "Open in browser" }),
    );
    expect(openPath).toHaveBeenCalledWith("out/report.html");
  });

  it("Given the host cannot open files in the system, then an HTML tab has no Open in browser button", async () => {
    readFile.mockResolvedValue(textView(HTML));
    renderPanel({ activePath: "out/report.html" });

    const view = await panel();
    await within(view).findByTitle("report.html rendered page");
    expect(
      within(view).queryByRole("button", { name: "Open in browser" }),
    ).toBeNull();
  });

  it("Given a non-HTML tab, then there is no Open in browser button even when the host can open files", async () => {
    readFile.mockResolvedValue(textView("package main"));
    renderPanel({
      activePath: "main.go",
      ports: {
        readFile: (p: string) => readFile(p),
        gitFileContent: (p: string) => gitFileContent(p),
        openPath: vi.fn(),
      },
    });

    const view = await panel();
    await waitFor(() => expect(fakeMonaco.editor.create).toHaveBeenCalled());
    expect(
      within(view).queryByRole("button", { name: "Open in browser" }),
    ).toBeNull();
  });

  it("Given an HTML file in the source segment with a reveal target, then the editor is scrolled to that line", async () => {
    readFile.mockResolvedValue(textView(HTML));
    renderPanel({
      activePath: "out/report.html",
      segment: "text",
      revealTarget: { line: 5, endLine: 5, nonce: 1 },
    });

    await panel();
    await waitFor(() =>
      expect(lastEditor().revealLineInCenter).toHaveBeenCalledWith(5),
    );
  });

  it("Given an HTML file from the session entry, then only the tool diff shows and there are no segments", async () => {
    renderPanel({ activePath: "out/report.html", sourceMode: "session" });

    const view = await panel();
    await within(view).findByText(
      "No tool call in this session changed this file",
    );
    expect(within(view).queryByRole("button", { name: "Render" })).toBeNull();
    expect(frame(view)).toBeNull();
  });

  it("Given an HTML file from the session entry and a host that can open files in the system, then the header still offers Open in browser", async () => {
    const openPath = vi.fn().mockResolvedValue(undefined);
    renderPanel({
      activePath: "out/report.html",
      sourceMode: "session",
      ports: {
        readFile: (p: string) => readFile(p),
        gitFileContent: (p: string) => gitFileContent(p),
        openPath,
      },
    });

    const view = await panel();
    await userEvent.click(
      await within(view).findByRole("button", { name: "Open in browser" }),
    );
    expect(openPath).toHaveBeenCalledWith("out/report.html");
    expect(
      within(view).queryByRole("button", { name: "Re-render" }),
    ).toBeNull();
  });
});
