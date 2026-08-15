import {
  fireEvent,
  render as rtlRender,
  screen,
  waitFor,
  type RenderOptions,
} from "@testing-library/react";
import { TranscriptPortsProvider } from "./ports-context";
import type { TranscriptPorts } from "./ports";
import * as React from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
  },
}));
vi.mock("sonner", () => sonnerMocks);

import { RichLink } from "./rich-link";

// RichLink 的副作用全部走 TranscriptPorts(不再直接 import Wails 绑定),断言打在端口上。
const openPathMock = vi.fn<(path: string) => Promise<void>>();
const openExternalURLMock = vi.fn<(url: string) => void>();

const testPorts: TranscriptPorts = {
  answerToolPermission: async () => {},
  answerUserQuestion: async () => {},
  answerToolApproval: async () => {},
  resolveExecApproval: async () => ({ status: "resolved" }),
  resolvePlanAction: async () => ({}),
  openPath: openPathMock,
  openExternalURL: openExternalURLMock,
};

function PortsWrapper({ children }: { children: React.ReactNode }) {
  return (
    <TranscriptPortsProvider ports={testPorts}>
      {children}
    </TranscriptPortsProvider>
  );
}

function render(
  ui: React.ReactElement,
  options?: Omit<RenderOptions, "wrapper">,
) {
  return rtlRender(ui, { wrapper: PortsWrapper, ...options });
}

const CWD = "/Users/me/proj";

beforeEach(() => {
  openPathMock.mockReset().mockResolvedValue(undefined);
  openExternalURLMock.mockReset();
  sonnerMocks.toast.success.mockReset();
  sonnerMocks.toast.error.mockReset();
});

afterEach(() => {
  vi.useRealTimers();
});

function mockClipboard() {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
  return writeText;
}

describe("RichLink", () => {
  describe("URL link", () => {
    it("clicking calls the openExternalURL port, not browser navigation", () => {
      render(
        <RichLink href="https://example.com" cwd={CWD}>
          example
        </RichLink>,
      );
      const link = screen.getByRole("link", { name: /example/ });
      fireEvent.click(link);
      expect(openExternalURLMock).toHaveBeenCalledWith("https://example.com");
      expect(openPathMock).not.toHaveBeenCalled();
    });

    it("renders open-link icon after text", () => {
      render(
        <RichLink href="https://example.com" cwd={CWD}>
          example
        </RichLink>,
      );
      expect(screen.getByTestId("rich-link-open-icon")).toHaveAttribute(
        "data-link-kind",
        "url",
      );
    });
  });

  describe("Local file link — in cwd", () => {
    it("clicking an encoded non-ASCII path calls openPath with the decoded filesystem path", () => {
      render(
        <RichLink
          href="/Users/me/proj/docs/%E6%9C%AC%E5%9C%B0%20E2E.md"
          cwd={CWD}
        >
          本地 E2E
        </RichLink>,
      );
      fireEvent.click(screen.getByRole("link", { name: /本地 E2E/ }));
      expect(openPathMock).toHaveBeenCalledWith(
        "/Users/me/proj/docs/本地 E2E.md",
      );
    });

    it("clicking calls openPath with full path + line suffix", () => {
      render(
        <RichLink href="/Users/me/proj/src/foo.go:42" cwd={CWD}>
          foo.go:42
        </RichLink>,
      );
      const link = screen.getByRole("link", { name: /foo\.go:42/ });
      fireEvent.click(link);
      expect(openPathMock).toHaveBeenCalledWith("/Users/me/proj/src/foo.go:42");
      expect(openExternalURLMock).not.toHaveBeenCalled();
    });

    it("renders file icon before text and open icon after text", () => {
      render(
        <RichLink href="/Users/me/proj/src/foo.go" cwd={CWD}>
          foo.go
        </RichLink>,
      );
      const link = screen.getByRole("link", { name: /foo\.go/ });
      expect(link.firstElementChild).toHaveAttribute(
        "data-testid",
        "rich-link-path-icon",
      );
      expect(screen.getByTestId("rich-link-path-icon")).toHaveAttribute(
        "data-path-kind",
        "file",
      );
      expect(screen.getByTestId("rich-link-open-icon")).toHaveAttribute(
        "data-link-kind",
        "local-internal",
      );
    });
  });

  describe("Local file link — outside cwd", () => {
    it("renders file icon, not folder icon", () => {
      render(
        <RichLink href="/usr/local/bin/agentred" cwd={CWD}>
          agentred
        </RichLink>,
      );
      expect(screen.getByTestId("rich-link-path-icon")).toHaveAttribute(
        "data-path-kind",
        "file",
      );
    });

    it("clicking calls openPath", () => {
      render(
        <RichLink href="/usr/local/bin/agentred" cwd={CWD}>
          agentred
        </RichLink>,
      );
      fireEvent.click(screen.getByRole("link", { name: /agentred/ }));
      expect(openPathMock).toHaveBeenCalledWith("/usr/local/bin/agentred");
    });
  });

  describe("Local folder link", () => {
    it("renders folder icon before text", () => {
      render(
        <RichLink href="/Users/me/proj/docs/" cwd={CWD}>
          docs
        </RichLink>,
      );
      expect(screen.getByTestId("rich-link-path-icon")).toHaveAttribute(
        "data-path-kind",
        "folder",
      );
    });
  });

  describe("Relative local path", () => {
    it("resolves against cwd and renders local file link affordances", () => {
      render(
        <RichLink href="relative/foo.go" cwd={CWD}>
          rel
        </RichLink>,
      );
      const link = screen.getByRole("link", { name: /rel/ });
      expect(link).toHaveAttribute("href", "/Users/me/proj/relative/foo.go");
      expect(screen.getByTestId("rich-link-path-icon")).toHaveAttribute(
        "data-path-kind",
        "file",
      );
      expect(screen.getByTestId("rich-link-open-icon")).toHaveAttribute(
        "data-link-kind",
        "local-internal",
      );
    });

    it("opens the cwd-resolved absolute path", () => {
      render(
        <RichLink href="relative/foo.go" cwd={CWD}>
          rel
        </RichLink>,
      );

      fireEvent.click(screen.getByRole("link", { name: /rel/ }));

      expect(openPathMock).toHaveBeenCalledWith(
        "/Users/me/proj/relative/foo.go",
      );
      expect(openExternalURLMock).not.toHaveBeenCalled();
    });
  });

  describe("Copy button in popover", () => {
    it("URL popover copy writes full URL + shows success toast", async () => {
      const writeText = mockClipboard();
      render(
        <RichLink href="https://example.com/long/path" cwd={CWD}>
          ex
        </RichLink>,
      );
      const link = screen.getByRole("link", { name: /ex/ });
      fireEvent.focus(link);
      const copyBtn = await screen.findByRole("button", { name: /Copy/ });
      fireEvent.click(copyBtn);
      await waitFor(() => {
        expect(writeText).toHaveBeenCalledWith("https://example.com/long/path");
      });
      expect(sonnerMocks.toast.success).toHaveBeenCalled();
    });

    it("local-internal popover copy writes full path with line suffix", async () => {
      const writeText = mockClipboard();
      render(
        <RichLink href="/Users/me/proj/src/foo.go:42" cwd={CWD}>
          foo.go:42
        </RichLink>,
      );
      fireEvent.focus(screen.getByRole("link", { name: /foo\.go:42/ }));
      const copyBtn = await screen.findByRole("button", { name: /Copy/ });
      fireEvent.click(copyBtn);
      await waitFor(() => {
        expect(writeText).toHaveBeenCalledWith("/Users/me/proj/src/foo.go:42");
      });
    });
  });

  describe("Popover content sanity", () => {
    it("local-internal popover shows both project root and relative path", async () => {
      render(
        <RichLink href="/Users/me/proj/src/foo.go" cwd={CWD}>
          foo
        </RichLink>,
      );
      fireEvent.focus(screen.getByRole("link", { name: /foo/ }));
      expect(await screen.findByText("/Users/me/proj")).toBeInTheDocument();
      expect(screen.getByText("src/foo.go")).toBeInTheDocument();
    });

    it("local-internal popover wraps long project root and relative path segments", async () => {
      const cwd =
        "/Users/codfrm/Code/agentre/agentre/a-very-long-project-root-name";
      render(
        <RichLink
          href={`${cwd}/frontend/src/components/agentre/__tests__/chat.test.tsx:89`}
          cwd={cwd}
        >
          chat.test.tsx:89
        </RichLink>,
      );
      fireEvent.focus(screen.getByRole("link", { name: /chat\.test\.tsx:89/ }));

      expect(await screen.findByText(cwd)).toHaveClass(
        "min-w-0",
        "break-all",
        "whitespace-normal",
      );
      expect(
        screen.getByText(
          "frontend/src/components/agentre/__tests__/chat.test.tsx",
        ),
      ).toHaveClass("min-w-0", "break-all", "whitespace-normal");
    });

    it("local-external popover shows full path but no project root segment", async () => {
      render(
        <RichLink href="/usr/local/bin/agentred" cwd={CWD}>
          ag
        </RichLink>,
      );
      fireEvent.focus(screen.getByRole("link", { name: /ag/ }));
      expect(
        await screen.findByText("/usr/local/bin/agentred"),
      ).toBeInTheDocument();
      // CWD value should NOT appear in external popover.
      expect(screen.queryByText(CWD)).not.toBeInTheDocument();
    });
  });
});
