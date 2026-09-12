import {
  fireEvent,
  render as rtlRender,
  screen,
  waitFor,
} from "@testing-library/react";
import { TranscriptPortsProvider } from "./ports-context";
import type { ReadFileResult, TranscriptPorts } from "./ports";
import * as React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: { error: vi.fn() },
}));

vi.mock("sonner", () => sonnerMocks);

import { classifyMarkdownImage, MarkdownImage } from "./markdown-image";

// 组件的副作用全部走 TranscriptPorts(不再直接 import Wails 绑定),所以这里注入
// 端口 mock,断言打在端口上。
const portMocks = {
  readWorkspaceFile:
    vi.fn<(sessionId: number, path: string) => Promise<ReadFileResult>>(),
  openPath: vi.fn<(path: string) => Promise<void>>(),
};

const requiredPorts: TranscriptPorts = {
  answerToolPermission: async () => {},
  answerUserQuestion: async () => {},
  answerToolApproval: async () => {},
  resolveExecApproval: async () => ({ status: "resolved" }),
  resolvePlanAction: async () => ({}),
};

const testPorts: TranscriptPorts = {
  ...requiredPorts,
  readWorkspaceFile: portMocks.readWorkspaceFile,
  openPath: portMocks.openPath,
};

// 宿主没有「在文件管理器里打开」这个能力(例如浏览器宿主)时,可选端口整个缺席。
const portsWithoutOpenPath: TranscriptPorts = {
  ...requiredPorts,
  readWorkspaceFile: portMocks.readWorkspaceFile,
};

function makeWrapper(ports: TranscriptPorts) {
  return function PortsWrapper({ children }: { children: React.ReactNode }) {
    return (
      <TranscriptPortsProvider ports={ports}>
        {children}
      </TranscriptPortsProvider>
    );
  };
}

function render(ui: React.ReactElement, ports: TranscriptPorts = testPorts) {
  return rtlRender(ui, { wrapper: makeWrapper(ports) });
}

beforeEach(() => {
  portMocks.readWorkspaceFile.mockReset();
  portMocks.openPath.mockReset();
  portMocks.openPath.mockResolvedValue(undefined);
  sonnerMocks.toast.error.mockReset();
});

describe("classifyMarkdownImage", () => {
  it("classifies http/https/www as remote", () => {
    expect(classifyMarkdownImage("https://x/a.png", {})).toEqual({
      kind: "remote",
      src: "https://x/a.png",
    });
    expect(classifyMarkdownImage("http://x/a.png", {})).toEqual({
      kind: "remote",
      src: "http://x/a.png",
    });
    expect(classifyMarkdownImage("www.x.com/a.png", {})).toEqual({
      kind: "remote",
      src: "www.x.com/a.png",
    });
  });

  it("classifies empty, data: and javascript: as plain with no src", () => {
    expect(classifyMarkdownImage("", {})).toEqual({
      kind: "plain",
      src: undefined,
    });
    expect(classifyMarkdownImage("data:image/png;base64,xx", {})).toEqual({
      kind: "plain",
      src: undefined,
    });
    expect(classifyMarkdownImage("javascript:alert(1)", {})).toEqual({
      kind: "plain",
      src: undefined,
    });
  });

  it("classifies a local path without sessionId as plain with stripped src", () => {
    expect(classifyMarkdownImage("a.png", { cwd: "/proj" })).toEqual({
      kind: "plain",
      src: undefined,
    });
  });

  it("keeps an absolute local path without sessionId as plain with its src (today's broken-image behaviour)", () => {
    expect(classifyMarkdownImage("/abs/a.png", {})).toEqual({
      kind: "plain",
      src: "/abs/a.png",
    });
  });

  it("treats a non-positive sessionId as absent instead of calling the workspace API", () => {
    expect(
      classifyMarkdownImage("a.png", { cwd: "/proj", sessionId: 0 }),
    ).toEqual({
      kind: "plain",
      src: undefined,
    });
  });

  it("classifies relative path + cwd + image extension + sessionId as fetch", () => {
    expect(
      classifyMarkdownImage("a.png", { cwd: "/proj", sessionId: 7 }),
    ).toEqual({
      kind: "fetch",
      relPath: "a.png",
      absolutePath: "/proj/a.png",
      basename: "a.png",
    });
  });

  it("decodes percent-encoded non-ASCII relative paths before fetch", () => {
    expect(
      classifyMarkdownImage("docs/%E6%9C%AC%E5%9C%B0.png", {
        cwd: "/proj",
        sessionId: 7,
      }),
    ).toEqual({
      kind: "fetch",
      relPath: "docs/本地.png",
      absolutePath: "/proj/docs/本地.png",
      basename: "本地.png",
    });
  });

  it("resolves file:// to a local path", () => {
    expect(
      classifyMarkdownImage("file:///proj/a.png", {
        cwd: "/proj",
        sessionId: 7,
      }),
    ).toEqual({
      kind: "fetch",
      relPath: "a.png",
      absolutePath: "/proj/a.png",
      basename: "a.png",
    });
  });

  it("resolves file://localhost to a local path", () => {
    expect(
      classifyMarkdownImage("file://localhost/proj/a.png", {
        cwd: "/proj",
        sessionId: 7,
      }),
    ).toEqual({
      kind: "fetch",
      relPath: "a.png",
      absolutePath: "/proj/a.png",
      basename: "a.png",
    });
  });

  it("strips a file:// URL with a remote host instead of auto-loading a network share", () => {
    expect(
      classifyMarkdownImage("file://server/share/a.png", {
        cwd: "/proj",
        sessionId: 7,
      }),
    ).toEqual({ kind: "plain", src: undefined });
  });

  it("resolves a localhost file URL with a Windows drive path", () => {
    expect(
      classifyMarkdownImage("file://localhost/C:/Users/x/a.png", {
        cwd: "C:\\Users\\x",
        sessionId: 7,
      }),
    ).toEqual({
      kind: "fetch",
      relPath: "a.png",
      absolutePath: "C:/Users/x/a.png",
      basename: "a.png",
    });
  });

  it("matches Windows absolute paths case-insensitively and across slash styles", () => {
    expect(
      classifyMarkdownImage("c:/USERS/x/a.png", {
        cwd: "C:\\Users\\x",
        sessionId: 7,
      }),
    ).toEqual({
      kind: "fetch",
      relPath: "a.png",
      absolutePath: "c:/USERS/x/a.png",
      basename: "a.png",
    });
  });

  it("classifies a protocol-relative image URL as remote", () => {
    expect(
      classifyMarkdownImage("//cdn.example.com/a.png", {
        cwd: "/proj",
        sessionId: 7,
      }),
    ).toEqual({ kind: "remote", src: "//cdn.example.com/a.png" });
  });

  it("classifies a non-image extension as fallback (clickable inside cwd)", () => {
    expect(
      classifyMarkdownImage("notes.txt", { cwd: "/proj", sessionId: 7 }),
    ).toEqual({
      kind: "fallback",
      absolutePath: "/proj/notes.txt",
      basename: "notes.txt",
    });
  });

  it("classifies an absolute path outside cwd as fallback (not clickable)", () => {
    expect(
      classifyMarkdownImage("/etc/passwd", { cwd: "/proj", sessionId: 7 }),
    ).toEqual({
      kind: "fallback",
      absolutePath: null,
      basename: "passwd",
    });
  });

  it("classifies a relative path without cwd as fallback (not clickable)", () => {
    expect(classifyMarkdownImage("a.png", { sessionId: 7 })).toEqual({
      kind: "fallback",
      absolutePath: null,
      basename: "a.png",
    });
  });

  it("classifies .. traversal that escapes cwd as fallback (not read, not clickable)", () => {
    expect(
      classifyMarkdownImage("../secret.png", { cwd: "/proj", sessionId: 7 }),
    ).toEqual({
      kind: "fallback",
      absolutePath: null,
      basename: "secret.png",
    });
  });

  it("classifies an absolute path with .. escaping cwd as fallback (not read, not clickable)", () => {
    expect(
      classifyMarkdownImage("/proj/../secret.png", {
        cwd: "/proj",
        sessionId: 7,
      }),
    ).toEqual({
      kind: "fallback",
      absolutePath: null,
      basename: "secret.png",
    });
  });

  it("classifies a file:// path with .. escaping cwd as fallback (not read, not clickable)", () => {
    expect(
      classifyMarkdownImage("file:///proj/../secret.png", {
        cwd: "/proj",
        sessionId: 7,
      }),
    ).toEqual({
      kind: "fallback",
      absolutePath: null,
      basename: "secret.png",
    });
  });

  it("classifies a Windows rooted path (\\foo) as fallback (not read, not clickable)", () => {
    expect(
      classifyMarkdownImage("\\Windows\\a.png", {
        cwd: "C:\\Users\\x",
        sessionId: 7,
      }),
    ).toEqual({
      kind: "fallback",
      absolutePath: null,
      basename: "a.png",
    });
  });

  it("classifies a UNC path (\\\\server\\share) as fallback (not read, not clickable)", () => {
    expect(
      classifyMarkdownImage("\\\\server\\share\\a.png", {
        cwd: "C:\\Users\\x",
        sessionId: 7,
      }),
    ).toEqual({
      kind: "fallback",
      absolutePath: null,
      basename: "a.png",
    });
  });
});

describe("MarkdownImage", () => {
  it("renders a remote image unchanged", () => {
    const { container } = render(
      <MarkdownImage src="https://x/a.png" alt="A" />,
    );
    const img = container.querySelector("img");
    expect(img?.getAttribute("src")).toBe("https://x/a.png");
    expect(img?.getAttribute("alt")).toBe("A");
  });

  it("renders a local path without sessionId as a plain img with stripped src", () => {
    const { container } = render(
      <MarkdownImage src="a.png" cwd="/proj" alt="A" />,
    );
    const img = container.querySelector("img");
    expect(img).toBeTruthy();
    expect(img?.getAttribute("src")).toBeNull();
    expect(portMocks.readWorkspaceFile).not.toHaveBeenCalled();
  });

  it("fetches a local image via the readWorkspaceFile port and renders a data URL", async () => {
    portMocks.readWorkspaceFile.mockResolvedValue({
      content: "aGVsbG8=",
      contentType: "image/png",
    });
    render(<MarkdownImage src="a.png" cwd="/proj" sessionId={7} alt="A" />);

    await waitFor(() =>
      expect(portMocks.readWorkspaceFile).toHaveBeenCalledWith(7, "a.png"),
    );
    const img = await screen.findByRole("img");
    expect(img.getAttribute("src")).toBe("data:image/png;base64,aGVsbG8=");
  });

  it("resets to loading when the image relPath changes", async () => {
    portMocks.readWorkspaceFile.mockResolvedValueOnce({
      content: "aGVsbG8=",
      contentType: "image/png",
    });
    const { rerender } = render(
      <MarkdownImage src="a.png" cwd="/proj" sessionId={7} alt="A" />,
    );
    await screen.findByRole("img");

    portMocks.readWorkspaceFile.mockResolvedValue({
      content: "d29ybGQ=",
      contentType: "image/png",
    });
    rerender(<MarkdownImage src="b.png" cwd="/proj" sessionId={7} alt="A" />);
    expect(screen.queryByRole("img")).toBeNull();
    await screen.findByRole("img");
    expect(screen.getByRole("img").getAttribute("src")).toBe(
      "data:image/png;base64,d29ybGQ=",
    );
  });

  it("resets to loading when the session changes for the same relPath", async () => {
    portMocks.readWorkspaceFile.mockResolvedValueOnce({
      content: "c2Vzc2lvbi03",
      contentType: "image/png",
    });
    const { rerender } = render(
      <MarkdownImage src="a.png" cwd="/proj" sessionId={7} alt="A" />,
    );
    await screen.findByRole("img");

    let resolveSecond:
      | ((value: { content: string; contentType: string }) => void)
      | undefined;
    portMocks.readWorkspaceFile.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveSecond = resolve;
        }),
    );
    rerender(<MarkdownImage src="a.png" cwd="/proj" sessionId={8} alt="A" />);

    expect(portMocks.readWorkspaceFile).toHaveBeenLastCalledWith(8, "a.png");
    expect(screen.queryByRole("img")).toBeNull();
    resolveSecond?.({ content: "c2Vzc2lvbi04", contentType: "image/png" });
    await screen.findByRole("img");
    expect(screen.getByRole("img").getAttribute("src")).toBe(
      "data:image/png;base64,c2Vzc2lvbi04",
    );
  });

  it("refetches when cwd changes for the same session and relPath", async () => {
    portMocks.readWorkspaceFile.mockResolvedValueOnce({
      content: "cHJvai0x",
      contentType: "image/png",
    });
    const { rerender } = render(
      <MarkdownImage src="a.png" cwd="/proj-1" sessionId={7} alt="A" />,
    );
    await screen.findByRole("img");

    portMocks.readWorkspaceFile.mockResolvedValueOnce({
      content: "cHJvai0y",
      contentType: "image/png",
    });
    rerender(<MarkdownImage src="a.png" cwd="/proj-2" sessionId={7} alt="A" />);

    await waitFor(() =>
      expect(portMocks.readWorkspaceFile).toHaveBeenCalledTimes(2),
    );
    await waitFor(() =>
      expect(screen.getByRole("img").getAttribute("src")).toBe(
        "data:image/png;base64,cHJvai0y",
      ),
    );
  });

  it("renders a tooLarge result as the tooLarge chip", async () => {
    portMocks.readWorkspaceFile.mockResolvedValue({
      content: "",
      contentType: "image/png",
      tooLarge: true,
    });
    render(<MarkdownImage src="big.png" cwd="/proj" sessionId={7} alt="A" />);

    expect(await screen.findByText("big.png")).toBeInTheDocument();
    expect(screen.getByText("Image too large")).toBeInTheDocument();
    expect(screen.queryByRole("img")).toBeNull();
  });

  it("renders a binary result as the cannot-preview chip", async () => {
    portMocks.readWorkspaceFile.mockResolvedValue({
      content: "",
      contentType: "image/png",
      binary: true,
    });
    render(<MarkdownImage src="b.png" cwd="/proj" sessionId={7} alt="A" />);

    expect(await screen.findByText("b.png")).toBeInTheDocument();
    expect(screen.getByText("Cannot preview")).toBeInTheDocument();
  });

  it("renders a read error as the cannot-preview chip", async () => {
    portMocks.readWorkspaceFile.mockRejectedValue(new Error("nope"));
    render(<MarkdownImage src="c.png" cwd="/proj" sessionId={7} alt="A" />);

    expect(await screen.findByText("c.png")).toBeInTheDocument();
    expect(screen.getByText("Cannot preview")).toBeInTheDocument();
  });

  it("renders an inside-cwd fallback chip as a clickable button that opens the path", () => {
    render(<MarkdownImage src="notes.txt" cwd="/proj" sessionId={7} alt="A" />);

    const button = screen.getByRole("button");
    expect(button).toHaveTextContent("notes.txt");
    expect(button).toHaveTextContent("Cannot preview");
    fireEvent.click(button);
    expect(portMocks.openPath).toHaveBeenCalledWith("/proj/notes.txt");
  });

  it("reports an openPath rejection instead of silently swallowing it", async () => {
    portMocks.openPath.mockRejectedValueOnce(new Error("denied"));
    render(<MarkdownImage src="notes.txt" cwd="/proj" sessionId={7} alt="A" />);

    fireEvent.click(screen.getByRole("button"));

    await waitFor(() =>
      expect(sonnerMocks.toast.error).toHaveBeenCalledWith(
        "Open failed: denied",
      ),
    );
  });

  // 宿主**有** openPath 能力,不可点的原因只在路径本身:越界的路径没有 absolutePath。
  it("renders an outside-cwd fallback chip as inert text (no button, no open)", () => {
    const { container } = render(
      <MarkdownImage src="/etc/passwd" cwd="/proj" sessionId={7} alt="A" />,
    );
    expect(container.textContent).toContain("passwd");
    expect(container.textContent).toContain("Cannot preview");
    expect(screen.queryByRole("button")).toBeNull();
    expect(portMocks.openPath).not.toHaveBeenCalled();
  });

  // 另一个不可点的原因:路径合法,但宿主没有 openPath 这项能力(能力探测分支)。
  it("renders a legal-path fallback chip as inert text when the host has no openPath port", () => {
    expect(
      classifyMarkdownImage("notes.txt", { cwd: "/proj", sessionId: 7 }),
    ).toMatchObject({ absolutePath: "/proj/notes.txt" });

    const { container } = render(
      <MarkdownImage src="notes.txt" cwd="/proj" sessionId={7} alt="A" />,
      portsWithoutOpenPath,
    );

    expect(container.textContent).toContain("notes.txt");
    expect(container.textContent).toContain("Cannot preview");
    expect(screen.queryByRole("button")).toBeNull();
    expect(portMocks.openPath).not.toHaveBeenCalled();
  });

  it("does not read a .. traversal that escapes cwd (inert chip)", () => {
    const { container } = render(
      <MarkdownImage src="../secret.png" cwd="/proj" sessionId={7} alt="A" />,
    );
    expect(container.textContent).toContain("secret.png");
    expect(container.textContent).toContain("Cannot preview");
    expect(screen.queryByRole("button")).toBeNull();
    expect(portMocks.readWorkspaceFile).not.toHaveBeenCalled();
  });

  it("does not read an absolute path with .. escaping cwd (inert chip)", () => {
    const { container } = render(
      <MarkdownImage
        src="/proj/../secret.png"
        cwd="/proj"
        sessionId={7}
        alt="A"
      />,
    );
    expect(container.textContent).toContain("secret.png");
    expect(container.textContent).toContain("Cannot preview");
    expect(screen.queryByRole("button")).toBeNull();
    expect(portMocks.readWorkspaceFile).not.toHaveBeenCalled();
  });
});
