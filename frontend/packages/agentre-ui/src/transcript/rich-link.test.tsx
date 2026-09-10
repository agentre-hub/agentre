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

  describe("Popover hint does not promise a destination", () => {
    // 浮层在**点击之前**渲染，而去向由点击时的 previewFile 握手定下来（跟随
    // files.open_action，且控制台宿主根本没有外部应用这条退路）。所以这一行只能
    // 陈述「点一下会打开它」，不能指名由哪一侧打开。
    it("local-internal popover states the action without naming the app", async () => {
      render(
        <RichLink href="/Users/me/proj/src/foo.go" cwd={CWD}>
          foo.go
        </RichLink>,
      );
      fireEvent.focus(screen.getByRole("link", { name: /foo\.go/ }));

      expect(await screen.findByText(/Click to open this file/)).toBeTruthy();
      expect(screen.queryByText(/default system app/i)).toBeNull();
    });

    // 「设置」进到包里的唯一形状就是 previewFile 的返回值(宿主自己看
    // files.open_action)。浮层在**点击之前**渲染,两档必须是同一句话 —— 否则
    // 就等于在渲染期承诺了一个只有点击时才定得下来的去向。
    it.each([true, false])(
      "keeps the same hint when previewFile answers %s",
      async (handled) => {
        previewFileMock.mockReturnValue(handled);
        render(
          <RichLink href="/Users/me/proj/src/foo.go" cwd={CWD} sessionId={7}>
            foo.go
          </RichLink>,
        );
        fireEvent.focus(screen.getByRole("link", { name: /foo\.go/ }));

        expect(await screen.findByText(/Click to open this file/)).toBeTruthy();
        expect(screen.queryByText(/default system app/i)).toBeNull();
        // 浮层只是展开,不该替用户先按一次去向握手。
        expect(previewFileMock).not.toHaveBeenCalled();
      },
    );

    // cwd 之外的路径在任何设置、任何宿主下都只能交给外部应用，这句话本来就是对的。
    it("local-external popover keeps naming the default system app", async () => {
      render(
        <RichLink href="/usr/local/bin/agentred" cwd={CWD}>
          agentred
        </RichLink>,
      );
      fireEvent.focus(screen.getByRole("link", { name: /agentred/ }));

      expect(
        await screen.findByText(/Click to open with the default system app/),
      ).toBeTruthy();
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

      expect(previewFileMock).toHaveBeenCalledWith(SESSION_ID, "src/foo.go");
      expect(openPathMock).toHaveBeenCalledWith("/Users/me/proj/src/foo.go:42");
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
