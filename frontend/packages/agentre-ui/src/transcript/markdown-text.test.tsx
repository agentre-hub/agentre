import { fireEvent, screen, waitFor } from "@testing-library/react";
import { makeTestPorts, renderWithPorts } from "./__testing__/ports";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
  },
}));

// RichLink / MarkdownImage 现在从 TranscriptPortsProvider 取宿主能力，
// 不再直接 import Wails 绑定，因此断言打在注入的端口上。
const portMocks = {
  readWorkspaceFile: vi.fn(),
  openPath: vi.fn(),
};

vi.mock("sonner", () => sonnerMocks);

const render: typeof renderWithPorts = (ui, options) =>
  renderWithPorts(ui, {
    ports: makeTestPorts({
      readWorkspaceFile: portMocks.readWorkspaceFile,
      openPath: portMocks.openPath,
    }),
    ...options,
  });

import {
  MarkdownText,
  type MarkdownInlineDecorator,
  type MarkdownInlineSegment,
} from "./markdown-text";

const originalClipboard = navigator.clipboard;

function mockClipboard() {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
  return writeText;
}

afterEach(() => {
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: originalClipboard,
  });
});

beforeEach(() => {
  portMocks.readWorkspaceFile.mockReset();
  portMocks.openPath.mockReset();
  portMocks.openPath.mockResolvedValue(undefined);
});

describe("MarkdownText", () => {
  it("copies fenced code block text from AI markdown replies", async () => {
    const writeText = mockClipboard();

    render(<MarkdownText text={"结果如下：\n\n```\npnpm test\n```\n"} />);

    fireEvent.click(screen.getByRole("button", { name: "Copy" }));

    await waitFor(() => {
      expect(writeText).toHaveBeenCalledWith("pnpm test\n");
    });
    expect(sonnerMocks.toast.success).toHaveBeenCalledWith(
      "Code copied",
      expect.objectContaining({
        duration: 5000,
        position: "bottom-right",
      }),
    );
    expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();
  });
});

describe("MarkdownText automatic RichLinks", () => {
  it("Given ordinary prose with multiple allowed targets, when rendered, then each target is a RichLink and relative clicks use the resolved absolute path", () => {
    const text =
      'Open "README.md", frontend/src/chat.tsx:42:7，and https://example.com/a.';
    const { container } = render(<MarkdownText text={text} cwd="/work/proj" />);

    const links = screen.getAllByRole("link");
    expect(links.map((link) => link.textContent)).toEqual([
      expect.stringContaining("README.md"),
      expect.stringContaining("frontend/src/chat.tsx:42:7"),
      expect.stringContaining("https://example.com/a"),
    ]);
    expect(container.textContent).toContain(text);

    fireEvent.click(screen.getByRole("link", { name: /chat\.tsx:42:7/ }));
    expect(portMocks.openPath).toHaveBeenCalledWith(
      "/work/proj/frontend/src/chat.tsx:42:7",
    );
  });

  it("Given existing Markdown links and images, when rendered, then their descendants are not scanned into nested links", () => {
    const { container } = render(
      <MarkdownText
        text={
          "[README.md](https://example.com/readme) ![frontend/src/logo.png](logo.png)"
        }
        cwd="/work/proj"
      />,
    );

    expect(container.querySelectorAll("a")).toHaveLength(1);
    expect(container.querySelector("a a")).toBeNull();
    expect(container.querySelector("img")?.getAttribute("alt")).toBe(
      "frontend/src/logo.png",
    );
  });

  it("Given an explicit Markdown link with a remote file authority, when rendered, then it exposes no navigable href", () => {
    const { container } = render(
      <MarkdownText
        text="[remote share](file://server/share/a.go)"
        cwd="/work/proj"
      />,
    );

    expect(container.querySelector("a")?.hasAttribute("href")).toBe(false);
  });

  it("Given whole-target inline code, when rendered, then it is clickable while retaining the existing code appearance", () => {
    render(<MarkdownText text="`docs/My Guide.md`" cwd="/work/proj" />);

    const link = screen.getByRole("link", { name: /docs\/My Guide\.md/ });
    const code = link.closest("code");
    expect(code).not.toBeNull();
    expect(code).toHaveClass("font-mono", "bg-muted");
    expect(code?.textContent).toContain("docs/My Guide.md");
  });

  it("Given mixed inline code and fenced code, when rendered, then neither code region is partially autolinked", () => {
    const { container } = render(
      <MarkdownText
        text={
          "Run `cat frontend/src/chat.tsx` or `https://example.com --verbose` instead.\n\n```sh\ncat frontend/src/chat.tsx\n```\n"
        }
        cwd="/work/proj"
      />,
    );

    expect(container.querySelectorAll("a")).toHaveLength(0);
    expect(container.textContent).toContain("cat frontend/src/chat.tsx");
    expect(container.textContent).toContain("https://example.com --verbose");
  });

  it("Given no cwd, when relative targets appear, then they remain non-interactive text", () => {
    const { container } = render(
      <MarkdownText text="README.md ./docs/guide.md frontend/src/chat.tsx" />,
    );

    expect(container.querySelector("a")).toBeNull();
    expect(container.textContent).toBe(
      "README.md ./docs/guide.md frontend/src/chat.tsx",
    );
  });

  it("Given unsafe and ambiguous prose, when rendered, then it creates no navigable href", () => {
    const { container } = render(
      <MarkdownText
        text={
          "javascript:alert(1) data:text/plain,x example.com github.com/owner/repo foo.bar 2026/08/14"
        }
        cwd="/work/proj"
      />,
    );

    expect(container.querySelector("a")).toBeNull();
  });
});

describe("MarkdownText inline decorator", () => {
  // bobDecorator:测试用的最小装饰器 —— 把字面 "@Bob" 切成可点击 token,
  // 其余原样保留。只测接缝本身,不绑 mention 业务。
  function bobDecorator(
    onJump: (id: number) => void,
  ): MarkdownInlineDecorator<{ id: number; label: string }> {
    return {
      tokenize: (text) => {
        const segments: MarkdownInlineSegment<{ id: number; label: string }>[] =
          [];
        let rest = text;
        let idx = rest.indexOf("@Bob");
        while (idx >= 0) {
          if (idx > 0)
            segments.push({ type: "text", value: rest.slice(0, idx) });
          segments.push({ type: "token", data: { id: 7, label: "Bob" } });
          rest = rest.slice(idx + "@Bob".length);
          idx = rest.indexOf("@Bob");
        }
        if (rest) segments.push({ type: "text", value: rest });
        return segments;
      },
      render: (data) => (
        <button type="button" onClick={() => onJump(data.id)}>
          @{data.label}
        </button>
      ),
    };
  }

  it("renders decorator tokens as interactive nodes inside markdown output", () => {
    const onJump = vi.fn();
    const { container } = render(
      <MarkdownText
        text={"**bold** ping @Bob"}
        decorator={bobDecorator(onJump)}
      />,
    );
    expect(container.querySelector("strong")?.textContent).toBe("bold");
    fireEvent.click(screen.getByRole("button", { name: "@Bob" }));
    expect(onJump).toHaveBeenCalledWith(7);
  });

  it("does not decorate text inside inline code or fenced code blocks", () => {
    const onJump = vi.fn();
    const { container } = render(
      <MarkdownText
        text={"see `@Bob` and\n\n```\n@Bob\n```\n"}
        decorator={bobDecorator(onJump)}
      />,
    );
    expect(screen.queryByRole("button", { name: "@Bob" })).toBeNull();
    expect(container.textContent).toContain("@Bob");
  });
});

describe("MarkdownText URL whitelist", () => {
  it("preserves https href as-is", () => {
    const { container } = render(
      <MarkdownText text="[ex](https://example.com)" />,
    );
    expect(container.querySelector("a")?.getAttribute("href")).toBe(
      "https://example.com",
    );
  });

  it("preserves absolute POSIX path href as-is", () => {
    const { container } = render(
      <MarkdownText text="[f](/Users/me/foo.go:42)" cwd="/Users/me" />,
    );
    expect(container.querySelector("a")?.getAttribute("href")).toBe(
      "/Users/me/foo.go:42",
    );
  });

  it("decodes a Markdown-encoded non-ASCII absolute path before rendering the local link target", () => {
    const { container } = render(
      <MarkdownText
        text="[本地 E2E](/Users/me/docs/%E6%9C%AC%E5%9C%B0%20E2E.md)"
        cwd="/Users/me"
      />,
    );
    expect(container.querySelector("a")?.getAttribute("href")).toBe(
      "/Users/me/docs/本地 E2E.md",
    );
  });

  it("resolves file:// href to local path (RichLink handles it)", () => {
    const { container } = render(
      <MarkdownText text="[f](file:///Users/me/foo.go)" />,
    );
    const a = container.querySelector("a");
    // RichLink resolves file:// → local path via classifyLink/fullTarget
    expect(a?.getAttribute("href")).toBe("/Users/me/foo.go");
  });

  it("strips javascript: href", () => {
    const { container } = render(
      <MarkdownText text="[x](javascript:alert(1))" />,
    );
    const a = container.querySelector("a");
    // After url whitelist strips href, RichLink renders plain anchor with no href.
    expect(a?.getAttribute("href")).toBeFalsy();
  });
});

describe("MarkdownText image src whitelist + local rendering", () => {
  it("passes a relative image src through and renders a data URL when sessionId is present", async () => {
    portMocks.readWorkspaceFile.mockResolvedValue({
      content: "aGVsbG8=",
      contentType: "image/png",
    });
    const { container } = render(
      <MarkdownText text="![pic](foo.png)" cwd="/proj" sessionId={7} />,
    );

    await waitFor(() =>
      expect(portMocks.readWorkspaceFile).toHaveBeenCalledWith(7, "foo.png"),
    );
    const img = container.querySelector("img");
    expect(img?.getAttribute("src")).toBe("data:image/png;base64,aGVsbG8=");
    expect(img?.getAttribute("alt")).toBe("pic");
  });

  it("strips a data: image src (img renders without src)", () => {
    const { container } = render(
      <MarkdownText text="![d](data:image/png;base64,xx)" />,
    );
    const img = container.querySelector("img");
    expect(img?.getAttribute("src")).toBeNull();
  });

  it("strips a relative image src when sessionId is absent", () => {
    const { container } = render(
      <MarkdownText text="![pic](foo.png)" cwd="/proj" />,
    );
    const img = container.querySelector("img");
    expect(img?.getAttribute("src")).toBeNull();
    expect(portMocks.readWorkspaceFile).not.toHaveBeenCalled();
  });

  it("keeps the href whitelist unchanged: a relative link is still stripped", () => {
    const { container } = render(
      <MarkdownText text="[rel](relative/path)" cwd="/proj" />,
    );
    const a = container.querySelector("a");
    expect(a?.getAttribute("href")).toBeFalsy();
  });
});
