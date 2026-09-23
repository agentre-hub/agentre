import {
  fireEvent,
  render as rtlRender,
  screen,
  waitFor,
  within,
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

import {
  installCopyCommandModel,
  removeClipboard,
  restoreClipboardEnv,
} from "../lib/__testing__/clipboard";
import { RichLink } from "./rich-link";

// RichLink 的副作用全部走 TranscriptPorts(不再直接 import Wails 绑定),断言打在端口上。
const openPathMock = vi.fn<(path: string) => Promise<void>>();
const openExternalURLMock = vi.fn<(url: string) => void>();
// previewFile 默认返回 falsy(undefined):现有用例都不传 sessionId,dispatchClick
// 因此根本不会调它;传了 sessionId 但没显式配置返回值的用例也据此退回 openPath。
const previewFileMock = vi.fn<(sessionId: number, path: string) => boolean>();

const testPorts: TranscriptPorts = {
  answerToolPermission: async () => {},
  answerUserQuestion: async () => {},
  answerToolApproval: async () => {},
  resolveExecApproval: async () => ({ status: "resolved" }),
  resolvePlanAction: async () => ({}),
  openPath: openPathMock,
  openExternalURL: openExternalURLMock,
  previewFile: previewFileMock,
};

function renderWithPorts(
  ui: React.ReactElement,
  ports: TranscriptPorts,
  options?: Omit<RenderOptions, "wrapper">,
) {
  return rtlRender(ui, {
    wrapper: ({ children }) => (
      <TranscriptPortsProvider ports={ports}>
        {children}
      </TranscriptPortsProvider>
    ),
    ...options,
  });
}

function render(
  ui: React.ReactElement,
  options?: Omit<RenderOptions, "wrapper">,
) {
  return renderWithPorts(ui, testPorts, options);
}

const CWD = "/Users/me/proj";

beforeEach(() => {
  openPathMock.mockReset().mockResolvedValue(undefined);
  openExternalURLMock.mockReset();
  previewFileMock.mockReset();
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

    it("clicking a start-end range keeps both ends in the path handed to openPath", () => {
      render(
        <RichLink href="/Users/me/proj/src/foo.go:311-330" cwd={CWD}>
          foo.go:311-330
        </RichLink>,
      );
      fireEvent.click(screen.getByRole("link", { name: /foo\.go:311-330/ }));
      expect(openPathMock).toHaveBeenCalledWith(
        "/Users/me/proj/src/foo.go:311-330",
      );
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

    it("keeps the long label inline so it wraps instead of overflowing the column", () => {
      // 触发锚点一旦是 flex 容器,标签文本就成了 flex item;flex item 的自动最小
      // 尺寸是 min-content(整条不可断的路径),继承来的 overflow-wrap:break-word
      // 不会降低这个值 —— 整个链接盒子撑破消息列,转录区横向溢出。
      // 图标间距因此必须由外边距提供,不能靠 flex gap。jsdom 不做布局,守类名。
      render(
        <RichLink
          href="internal/controller/relay_ctr/TestRelayClient_GivenAReservedChannelID_ThenTheClientCannotConnect"
          cwd={CWD}
        >
          internal/controller/relay_ctr/TestRelayClient_GivenAReservedChannelID_ThenTheClientCannotConnect
        </RichLink>,
      );

      const link = screen.getByRole("link", { name: /TestRelayClient/ });
      expect(link.className).not.toMatch(/\bflex\b/);
      expect(screen.getByTestId("rich-link-path-icon").className).toMatch(
        /\bmr-1\b/,
      );
      expect(screen.getByTestId("rich-link-open-icon").className).toMatch(
        /\bml-1\b/,
      );
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

    /*
      宿主可能部署在 http://<局域网 IP>:port 上（agentre-server 的控制台就是），
      那里 Clipboard API 整个对象都不存在。转录里复制一条链接不该因此直接判死：
      共享的复制层有 execCommand 兜底，能复制就该复制成。
    */
    it("Given no Clipboard API, When the popover copy is used, Then the shared fallback still copies it", async () => {
      removeClipboard();
      const selectedAtCopy = installCopyCommandModel();
      try {
        render(
          <RichLink href="https://example.com/long/path" cwd={CWD}>
            ex
          </RichLink>,
        );
        fireEvent.focus(screen.getByRole("link", { name: /ex/ }));
        fireEvent.click(await screen.findByRole("button", { name: /Copy/ }));

        await waitFor(() => {
          expect(selectedAtCopy).toEqual(["https://example.com/long/path"]);
        });
        expect(sonnerMocks.toast.success).toHaveBeenCalled();
        expect(sonnerMocks.toast.error).not.toHaveBeenCalled();
      } finally {
        restoreClipboardEnv();
      }
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

  // 浮窗只回答「这是哪个文件」与「还能怎么打开」（spec「文件链接浮窗」）：一行
  // 路径 + 行号 + 相反打开方式 + 复制，不再有胶囊、项目根、绝对路径与解释文案。
  describe("Popover · one row", () => {
    async function openPopover(name: RegExp) {
      fireEvent.focus(screen.getByRole("link", { name }));
      return screen.findByTestId("rich-link-popover");
    }

    it("Given an in-project file, then the popover shows only its relative path, with no project root, absolute path or click hint", async () => {
      render(
        <RichLink href="/Users/me/proj/src/foo.go" cwd={CWD}>
          foo
        </RichLink>,
      );
      const pop = await openPopover(/foo/);

      expect(screen.getByTestId("rich-link-popover-path").textContent).toBe(
        "src/foo.go",
      );
      expect(pop.textContent).not.toContain(CWD);
      expect(pop.textContent).not.toMatch(/Click to open/);
      expect(pop.textContent).not.toMatch(/Local File/);
    });

    it("Given a link with a line number, then the popover shows it as L<n>", async () => {
      render(
        <RichLink href="/Users/me/proj/src/foo.go:42" cwd={CWD}>
          foo.go:42
        </RichLink>,
      );
      await openPopover(/foo\.go:42/);
      expect(screen.getByText("L42")).toBeInTheDocument();
    });

    it("Given a file outside the project, then the popover shows its full path and no explanation", async () => {
      render(
        <RichLink href="/usr/local/bin/agentred" cwd={CWD}>
          ag
        </RichLink>,
      );
      const pop = await openPopover(/ag/);

      expect(screen.getByTestId("rich-link-popover-path").textContent).toBe(
        "/usr/local/bin/agentred",
      );
      expect(pop.textContent).not.toMatch(/cwd|Outside Project/i);
    });

    it("Given a URL, then the popover shows the URL with a copy button and nothing else to click", async () => {
      render(
        <RichLink href="https://example.com/a" cwd={CWD}>
          ex
        </RichLink>,
      );
      const pop = await openPopover(/ex/);

      expect(screen.getByTestId("rich-link-popover-path").textContent).toBe(
        "https://example.com/a",
      );
      const buttons = within(pop).getAllByRole("button");
      expect(buttons.map((b) => b.getAttribute("aria-label"))).toEqual([
        "Copy link",
      ]);
      expect(pop.textContent).not.toMatch(/Click to open/);
    });
  });

  // 两条路都在时浮窗给出与默认相反的那一条（spec 决策 4–7）。默认方式由宿主经
  // fileOpenDefault 告知；宿主不告知时按今天的分流、不出相反按钮。
  describe("Opposite open mode", () => {
    const fileOpenDefaultMock = vi.fn<() => "preview" | "external">();

    function portsWith(overrides: Partial<TranscriptPorts> = {}) {
      return {
        ...testPorts,
        fileOpenDefault: fileOpenDefaultMock,
        ...overrides,
      } as TranscriptPorts;
    }

    beforeEach(() => {
      fileOpenDefaultMock.mockReset();
    });

    function renderFile(
      href: string,
      ports: TranscriptPorts,
      sessionId: number | null = 7,
    ) {
      renderWithPorts(
        <RichLink href={href} cwd={CWD} sessionId={sessionId ?? undefined}>
          target
        </RichLink>,
        ports,
      );
      fireEvent.focus(screen.getByRole("link", { name: /target/ }));
      return screen.findByTestId("rich-link-popover");
    }

    it("Given the default is preview, When the user clicks the opposite button, Then the system app opens the full target with its line suffix and the popover closes", async () => {
      fileOpenDefaultMock.mockReturnValue("preview");
      await renderFile("/Users/me/proj/src/foo.go:42", portsWith());

      fireEvent.click(
        screen.getByRole("button", {
          name: "Open with the default system app",
        }),
      );

      expect(openPathMock).toHaveBeenCalledWith("/Users/me/proj/src/foo.go:42");
      expect(previewFileMock).not.toHaveBeenCalled();
      await waitFor(() =>
        expect(screen.queryByTestId("rich-link-popover")).toBeNull(),
      );
    });

    it("Given the default is the system app, When the user clicks the opposite button, Then the file is previewed regardless of the setting, anchored at its line", async () => {
      fileOpenDefaultMock.mockReturnValue("external");
      previewFileMock.mockReturnValue(true);
      await renderFile("/Users/me/proj/src/foo.go:42", portsWith());

      fireEvent.click(
        screen.getByRole("button", { name: "Open in the preview panel" }),
      );

      expect(previewFileMock).toHaveBeenCalledWith(
        7,
        "src/foo.go",
        { line: 42 },
        { force: true },
      );
      expect(openPathMock).not.toHaveBeenCalled();
    });

    it.each([
      ["preview", "System app"],
      ["external", "Preview"],
    ] as const)(
      "Given the default is %s, then the opposite button shows the short label %s",
      async (openDefault, label) => {
        fileOpenDefaultMock.mockReturnValue(openDefault);
        const pop = await renderFile("/Users/me/proj/src/foo.go", portsWith());
        expect(pop.textContent).toContain(label);
      },
    );

    it("Given the host does not tell the default, then there is no opposite button", async () => {
      await renderFile("/Users/me/proj/src/foo.go", testPorts);
      expect(
        screen.queryByRole("button", {
          name: /default system app|preview panel/,
        }),
      ).toBeNull();
    });

    it.each([
      ["a non-previewable extension", "/Users/me/proj/data.bin", 7],
      ["a path outside the project", "/usr/local/bin/agentred", 7],
      ["a folder", "/Users/me/proj/src/", 7],
      ["no session to preview in", "/Users/me/proj/src/foo.go", null],
    ])("Given %s, then there is no opposite button", async (_, href, sid) => {
      fileOpenDefaultMock.mockReturnValue("preview");
      await renderFile(href, portsWith(), sid);
      expect(
        screen.queryByRole("button", {
          name: /default system app|preview panel/,
        }),
      ).toBeNull();
    });

    it("Given a host without system open (the console), then there is no opposite button", async () => {
      fileOpenDefaultMock.mockReturnValue("preview");
      await renderFile(
        "/Users/me/proj/src/foo.go",
        portsWith({ openPath: undefined }),
      );
      expect(
        screen.queryByRole("button", { name: /default system app/ }),
      ).toBeNull();
    });
  });

  // 系统打开因本机没有这个文件而失败（远端会话的文件通常如此）：说事实，可预览时
  // 给「预览」出口；其他失败沿用原 toast（spec「系统打开失败」）。
  describe("System open failure", () => {
    const notFound = () =>
      Object.assign(new Error("not-found"), { kind: "notFound" });

    it("Given the link click goes to the system app and the file is not on this machine, Then the toast states the fact and offers Preview, which forces the preview at the link's line", async () => {
      openPathMock.mockRejectedValue(notFound());
      previewFileMock.mockReturnValue(false);
      render(
        <RichLink href="/Users/me/proj/src/foo.go:9" cwd={CWD} sessionId={7}>
          foo
        </RichLink>,
      );
      fireEvent.click(screen.getByRole("link", { name: /foo/ }));

      await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
      const [title, opts] = sonnerMocks.toast.error.mock.calls[0] as [
        string,
        {
          description: string;
          action?: { label: string; onClick: () => void };
        },
      ];
      expect(title).toBe("This file isn't on this machine");
      expect(opts.description).toBe("It may be on a remote machine");
      expect(opts.action?.label).toBe("Preview");
      expect(JSON.stringify(sonnerMocks.toast.error.mock.calls)).not.toContain(
        "not-found",
      );

      previewFileMock.mockClear();
      opts.action?.onClick();
      expect(previewFileMock).toHaveBeenCalledWith(
        7,
        "src/foo.go",
        { line: 9 },
        { force: true },
      );
    });

    it("Given a non-previewable file that is not on this machine, Then the toast states the fact without a Preview action", async () => {
      openPathMock.mockRejectedValue(notFound());
      render(
        <RichLink href="/Users/me/proj/data.bin" cwd={CWD} sessionId={7}>
          data
        </RichLink>,
      );
      fireEvent.click(screen.getByRole("link", { name: /data/ }));

      await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
      const [title, opts] = sonnerMocks.toast.error.mock.calls[0] as [
        string,
        { action?: unknown },
      ];
      expect(title).toBe("This file isn't on this machine");
      expect(opts.action).toBeUndefined();
    });

    it("Given any other failure, Then the existing open-failed toast is used", async () => {
      openPathMock.mockRejectedValue(new Error("boom"));
      render(
        <RichLink href="/usr/local/bin/agentred" cwd={CWD}>
          ag
        </RichLink>,
      );
      fireEvent.click(screen.getByRole("link", { name: /ag/ }));

      await waitFor(() =>
        expect(sonnerMocks.toast.error).toHaveBeenCalledWith(
          "Open failed: boom",
        ),
      );
    });
  });

  // 桌面端转录点一条 cwd 内、扩展名在 allowlist 内的文件路径时,去向跟随
  // files.open_action;设置读取留在宿主的 previewFile 实现里(见 ports.ts),
  // dispatchClick 只认它的布尔回执:true = 宿主已接手,不再退回 openPath;
  // false / 端口不存在 = 退回今天的外部打开路线,一字不改(spec「入口与可用性」)。
  describe("previewFile routing (files.open_action)", () => {
    const SESSION_ID = 7;

    it("Given the host reports it took over preview, When a previewable in-cwd path is clicked, Then previewFile is called with the session relPath and openPath is never called", () => {
      previewFileMock.mockReturnValue(true);
      render(
        <RichLink
          href="/Users/me/proj/src/foo.go"
          cwd={CWD}
          sessionId={SESSION_ID}
        >
          foo.go
        </RichLink>,
      );

      fireEvent.click(screen.getByRole("link", { name: /foo\.go/ }));

      expect(previewFileMock).toHaveBeenCalledWith(SESSION_ID, "src/foo.go");
      expect(openPathMock).not.toHaveBeenCalled();
    });

    it("Given the host declines (files.open_action = external), When a previewable in-cwd path is clicked, Then it falls back to openPath with the byte-identical full target", () => {
      previewFileMock.mockReturnValue(false);
      render(
        <RichLink
          href="/Users/me/proj/src/foo.go:42"
          cwd={CWD}
          sessionId={SESSION_ID}
        >
          foo.go:42
        </RichLink>,
      );

      fireEvent.click(screen.getByRole("link", { name: /foo\.go:42/ }));

      expect(previewFileMock).toHaveBeenCalledWith(SESSION_ID, "src/foo.go", {
        line: 42,
      });
      expect(openPathMock).toHaveBeenCalledWith("/Users/me/proj/src/foo.go:42");
    });

    it("Given a link carrying a start-end range, When the host takes over preview, Then both ends reach previewFile as the anchor", () => {
      previewFileMock.mockReturnValue(true);
      render(
        <RichLink
          href="/Users/me/proj/src/foo.go:311-330"
          cwd={CWD}
          sessionId={SESSION_ID}
        >
          foo.go:311-330
        </RichLink>,
      );

      fireEvent.click(screen.getByRole("link", { name: /foo\.go:311-330/ }));

      expect(previewFileMock).toHaveBeenCalledWith(SESSION_ID, "src/foo.go", {
        line: 311,
        endLine: 330,
      });
      expect(openPathMock).not.toHaveBeenCalled();
    });

    it("Given an extension outside the preview allowlist, When clicked, Then it always goes to openPath and previewFile is never consulted, regardless of what the host would return", () => {
      previewFileMock.mockReturnValue(true);
      render(
        <RichLink
          href="/Users/me/proj/data.bin"
          cwd={CWD}
          sessionId={SESSION_ID}
        >
          data.bin
        </RichLink>,
      );

      fireEvent.click(screen.getByRole("link", { name: /data\.bin/ }));

      expect(previewFileMock).not.toHaveBeenCalled();
      expect(openPathMock).toHaveBeenCalledWith("/Users/me/proj/data.bin");
    });

    it("Given a path outside the session cwd, When clicked, Then it always goes to openPath and previewFile is never consulted, regardless of what the host would return", () => {
      previewFileMock.mockReturnValue(true);
      render(
        <RichLink
          href="/usr/local/bin/agentred"
          cwd={CWD}
          sessionId={SESSION_ID}
        >
          agentred
        </RichLink>,
      );

      fireEvent.click(screen.getByRole("link", { name: /agentred/ }));

      expect(previewFileMock).not.toHaveBeenCalled();
      expect(openPathMock).toHaveBeenCalledWith("/usr/local/bin/agentred");
    });

    // 「在不在 cwd 内」只该判一次。classifyLink 已经判过了(Windows 上按大小写
    // 不敏感比对,这也是转录里 agent 自己写出来的路径的真实形状:盘符大小写与
    // 分隔符都跟 cwd 对不上),它给出的 relPath 就是标签的身份。分流处再拿一个
    // 大小写敏感、按 cwd 分隔符切的函数重判一次,两处的答案会在 Windows 上分叉:
    // 链接明明是 local-internal、popover 也照着 relPath 画着「src/foo.go」,预览
    // 却接不上——桌面端悄悄退回外部应用,只接 previewFile 的宿主(控制台)那侧
    // 更是把它渲染成不可点的纯文本。
    it("Given a Windows session whose transcript link differs from cwd in drive-letter case, When clicked, Then it routes to previewFile with the same relPath the classification already resolved", () => {
      previewFileMock.mockReturnValue(true);
      render(
        <RichLink
          href="c:/users/me/proj/src/foo.go"
          // JSX 字面量不处理转义,反斜杠 cwd 必须走表达式容器。
          cwd={"C:\\Users\\me\\proj"}
          sessionId={SESSION_ID}
        >
          foo.go
        </RichLink>,
      );

      fireEvent.click(screen.getByRole("link", { name: /foo\.go/ }));

      expect(previewFileMock).toHaveBeenCalledWith(SESSION_ID, "src/foo.go");
      expect(openPathMock).not.toHaveBeenCalled();
    });

    it("Given a host that still wires openPath, When previewFile is absent, Then it falls back to openPath exactly as a capability-less-for-preview host would", () => {
      const hostOpenPath = vi
        .fn<(path: string) => Promise<void>>()
        .mockResolvedValue(undefined);
      renderWithPorts(
        <RichLink
          href="/Users/me/proj/src/foo.go"
          cwd={CWD}
          sessionId={SESSION_ID}
        >
          foo.go
        </RichLink>,
        {
          answerToolPermission: async () => {},
          answerUserQuestion: async () => {},
          answerToolApproval: async () => {},
          resolveExecApproval: async () => ({ status: "resolved" }),
          resolvePlanAction: async () => ({}),
          openPath: hostOpenPath,
          // previewFile 未接入 —— 与真实的「未来某个不支持预览的宿主」一致。
        },
      );

      fireEvent.click(screen.getByRole("link", { name: /foo\.go/ }));

      expect(hostOpenPath).toHaveBeenCalledWith("/Users/me/proj/src/foo.go");
    });

    // 「入口不渲染」说的是这台宿主:previewFile 与 openPath 都没有,对这条 cwd 内、
    // 扩展名在 allowlist 内的路径真的什么都做不了。这种宿主上它必须是不可交互的
    // 纯文本,不能是一个点了没反应的死链接(spec「入口与可用性」)。
    it("Given a host with neither previewFile nor openPath, When a previewable in-cwd path would otherwise be clickable, Then it renders as non-interactive text instead of a dead link", () => {
      renderWithPorts(
        <RichLink
          href="/Users/me/proj/src/foo.go"
          cwd={CWD}
          sessionId={SESSION_ID}
        >
          foo.go
        </RichLink>,
        {
          answerToolPermission: async () => {},
          answerUserQuestion: async () => {},
          answerToolApproval: async () => {},
          resolveExecApproval: async () => ({ status: "resolved" }),
          resolvePlanAction: async () => ({}),
          // 既没有 openPath 也没有 previewFile —— 这台宿主对这个文件真的无处可去。
        },
      );

      expect(
        screen.queryByRole("link", { name: /foo\.go/ }),
      ).not.toBeInTheDocument();
      expect(screen.getByText("foo.go")).toBeInTheDocument();
    });

    // 控制台形状的宿主:接了 previewFile,但**没有** openPath ——「交给外部应用」
    // 这条退路在浏览器里根本不存在(spec「入口与可用性」:「控制台里不在
    // allowlist 的不出入口——它没有『交给外部应用』这条退路」;越出 cwd 的同样
    // 「不出入口」)。判据是「这条路径在这台宿主上有没有一个去处」,不是「它可不
    // 可预览」。
    const consolePorts = (
      previewFile: TranscriptPorts["previewFile"],
    ): TranscriptPorts => ({
      answerToolPermission: async () => {},
      answerUserQuestion: async () => {},
      answerToolApproval: async () => {},
      resolveExecApproval: async () => ({ status: "resolved" }),
      resolvePlanAction: async () => ({}),
      previewFile,
      // openPath 未接入 —— 浏览器里没有「外部应用」这个去向。
    });

    it("Given a host with previewFile but no openPath, When the extension is outside the preview allowlist, Then it renders as non-interactive text", () => {
      renderWithPorts(
        <RichLink
          href="/Users/me/proj/data.bin"
          cwd={CWD}
          sessionId={SESSION_ID}
        >
          data.bin
        </RichLink>,
        consolePorts(previewFileMock),
      );

      expect(
        screen.queryByRole("link", { name: /data\.bin/ }),
      ).not.toBeInTheDocument();
      expect(screen.getByText("data.bin")).toBeInTheDocument();
    });

    it("Given a host with previewFile but no openPath, When the path lies outside the session cwd, Then it renders as non-interactive text", () => {
      renderWithPorts(
        <RichLink
          href="/usr/local/bin/agentred"
          cwd={CWD}
          sessionId={SESSION_ID}
        >
          agentred
        </RichLink>,
        consolePorts(previewFileMock),
      );

      expect(
        screen.queryByRole("link", { name: /agentred/ }),
      ).not.toBeInTheDocument();
      expect(screen.getByText("agentred")).toBeInTheDocument();
    });

    it("Given a host with previewFile but no openPath, When the path is previewable and inside the cwd, Then it stays clickable and routes to previewFile", () => {
      previewFileMock.mockReturnValue(true);
      renderWithPorts(
        <RichLink
          href="/Users/me/proj/src/foo.go"
          cwd={CWD}
          sessionId={SESSION_ID}
        >
          foo.go
        </RichLink>,
        consolePorts(previewFileMock),
      );

      fireEvent.click(screen.getByRole("link", { name: /foo\.go/ }));

      expect(previewFileMock).toHaveBeenCalledWith(SESSION_ID, "src/foo.go");
    });
  });
});
