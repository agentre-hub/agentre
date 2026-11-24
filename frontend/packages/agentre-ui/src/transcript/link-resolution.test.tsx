import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import * as React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

import { TooltipProvider } from "../ui/tooltip";
import type { TranscriptBlock, TranscriptMessage } from "./dto";
import type { TranscriptPorts } from "./ports";
import { TranscriptPortsProvider } from "./ports-context";
import { RichLink } from "./rich-link";
import { SessionLocationProvider } from "./session-location";
import {
  TranscriptRenderContext,
  TranscriptRowView,
} from "./transcript-row-view";
import { buildTranscriptRows, type TranscriptRow } from "./transcript-rows";

/*
 * spec 2026-11-24-chat-right-toolwindows「文件与 localhost 链接」：链接按「AI 当时所在
 * 目录」解析，绝对路径归最深的已认领工作根。问题 8（链接按会话工作目录解析）与问题 9
 * （兄弟 worktree 的绝对路径只能交给系统打开）的复现在最前面。
 */

const SESSION = 42;
const PRIMARY = "/repo";
const WT = "/repo-wt";
const WT2 = "/repo-wt2";

const ROOTS = [
  { path: PRIMARY, name: "repo", primary: true },
  { path: WT, name: "repo-wt" },
  { path: WT2, name: "repo-wt2" },
];

const openPath = vi.fn<(path: string) => Promise<void>>();
const openExternalURL =
  vi.fn<(url: string, opts?: { sessionId?: number }) => void | Promise<void>>();
const previewFile = vi.fn<(...args: unknown[]) => boolean>();
const claimPath =
  vi.fn<
    (
      sessionId: number,
      absPath: string,
    ) => Promise<{ path: string; name?: string; primary?: boolean }[]>
  >();

function ports(overrides: Partial<TranscriptPorts> = {}): TranscriptPorts {
  return {
    answerToolPermission: async () => {},
    answerUserQuestion: async () => {},
    answerToolApproval: async () => {},
    resolveExecApproval: async () => ({ status: "resolved" }),
    resolvePlanAction: async () => ({}),
    openPath,
    openExternalURL,
    previewFile: previewFile as TranscriptPorts["previewFile"],
    ...overrides,
  } as TranscriptPorts;
}

beforeEach(() => {
  openPath.mockReset().mockResolvedValue(undefined);
  openExternalURL.mockReset();
  previewFile.mockReset().mockReturnValue(true);
  claimPath.mockReset();
  sonnerMocks.toast.error.mockReset();
  sonnerMocks.toast.success.mockReset();
});

function message(id: number, blocks: TranscriptBlock[]): TranscriptMessage {
  return {
    id,
    sessionId: SESSION,
    role: "assistant",
    blocks,
    model: "",
    promptTokens: 0,
    completionTokens: 0,
    cachedTokens: 0,
    cacheCreationTokens: 0,
    reasoningTokens: 0,
    durationMs: 0,
    createtime: id,
  } as unknown as TranscriptMessage;
}

function userMessage(id: number, text: string): TranscriptMessage {
  return {
    ...message(id, [{ type: "text", text }]),
    role: "user",
  } as TranscriptMessage;
}

function bash(id: string, cwd?: string): TranscriptBlock[] {
  return [
    {
      type: "tool_use",
      toolUseId: id,
      toolName: "Bash",
      toolInput: { command: "ls" },
      ...(cwd ? { cwd } : {}),
    },
    {
      type: "tool_result",
      toolUseId: id,
      text: "ok",
      ...(cwd ? { cwd } : {}),
    },
  ];
}

function rows(messages: TranscriptMessage[]): TranscriptRow[] {
  return buildTranscriptRows({
    displayMessages: messages,
    autonomousIds: new Set(),
  }).rows;
}

/** 第 `messageId` 条消息里第 `nth` 个正文行。 */
function textRow(
  all: TranscriptRow[],
  messageId: number,
  nth = 0,
): TranscriptRow {
  const found = all.filter(
    (r) => r.messageId === messageId && r.item.type === "text",
  )[nth];
  if (!found) throw new Error(`no text row ${nth} in message ${messageId}`);
  return found;
}

type HostOpts = {
  roots?: typeof ROOTS;
  location?: "local" | "remote";
  ports?: TranscriptPorts;
};

function Host({
  opts,
  children,
}: {
  opts: HostOpts;
  children: React.ReactNode;
}) {
  return (
    <TooltipProvider>
      <TranscriptPortsProvider ports={opts.ports ?? ports()}>
        <SessionLocationProvider location={opts.location} roots={opts.roots}>
          <TranscriptRenderContext.Provider
            value={{
              agentName: "Agentre",
              agentAvatar: <span />,
              cwd: PRIMARY,
              sessionId: SESSION,
            }}
          >
            {children}
          </TranscriptRenderContext.Provider>
        </SessionLocationProvider>
      </TranscriptPortsProvider>
    </TooltipProvider>
  );
}

function renderRow(row: TranscriptRow, opts: HostOpts = { roots: ROOTS }) {
  return render(
    <Host opts={opts}>
      <TranscriptRowView
        row={row}
        liveTail=""
        liveBlocks={undefined}
        liveRetry={null}
        showIndicator={false}
        compacting={false}
        reconnecting={false}
      />
    </Host>,
  );
}

function renderLink(href: string, opts: HostOpts = { roots: ROOTS }) {
  return render(
    <Host opts={opts}>
      <RichLink href={href} cwd={PRIMARY} sessionId={SESSION}>
        link
      </RichLink>
    </Host>,
  );
}

function clickLink(name: RegExp | string = /link|a\.ts/) {
  fireEvent.click(screen.getByRole("link", { name }));
}

describe("问题 8：AI 在 worktree 里说的相对路径", () => {
  it("Given a turn whose tool ran in a worktree, When the text after it links src/a.ts, Then the link resolves in that worktree, not the session cwd", () => {
    const all = rows([
      message(1, [
        ...bash("t1", WT),
        { type: "text", text: "改了 src/a.ts 这个文件" },
      ]),
    ]);
    renderRow(textRow(all, 1));

    const link = screen.getByRole("link", { name: /a\.ts/ });
    expect(link).toHaveAttribute("href", `${WT}/src/a.ts`);
    fireEvent.click(link);
    expect(previewFile).toHaveBeenCalledWith(SESSION, {
      root: WT,
      path: "src/a.ts",
    });
  });
});

describe("问题 9：兄弟 worktree 的绝对路径", () => {
  it("Given an absolute path inside a claimed sibling worktree, When clicked, Then it is previewed in that root instead of going to system open", () => {
    renderLink(`${WT2}/src/b.ts`);

    clickLink();

    expect(previewFile).toHaveBeenCalledWith(SESSION, {
      root: WT2,
      path: "src/b.ts",
    });
    expect(openPath).not.toHaveBeenCalled();
  });
});

describe("AI 当时所在目录", () => {
  it("Given two tool calls in different worktrees, When text follows each, Then each text resolves against the tool call right before it", () => {
    const all = rows([
      message(1, [
        ...bash("t1", WT),
        { type: "text", text: "先看 src/a.ts 这里" },
        ...bash("t2", WT2),
        { type: "text", text: "再看 src/a.ts 那里" },
      ]),
    ]);
    renderRow(textRow(all, 1, 1));

    expect(screen.getByRole("link", { name: /a\.ts/ })).toHaveAttribute(
      "href",
      `${WT2}/src/a.ts`,
    );
  });

  it("Given a turn with no tool call yet, When its text links a relative path, Then it resolves where the AI was when the turn started", () => {
    const all = rows([
      message(1, [...bash("t1", WT)]),
      userMessage(2, "继续"),
      message(3, [
        { type: "text", text: "还在 src/a.ts 里" },
        ...bash("t2", WT2),
      ]),
    ]);
    renderRow(textRow(all, 3));

    expect(screen.getByRole("link", { name: /a\.ts/ })).toHaveAttribute(
      "href",
      `${WT}/src/a.ts`,
    );
  });

  it("Given an old message with no location record, When its text links a relative path, Then it falls back to the session cwd", () => {
    const all = rows([
      message(1, [...bash("t1"), { type: "text", text: "看 src/a.ts 吧" }]),
    ]);
    renderRow(textRow(all, 1));

    const link = screen.getByRole("link", { name: /a\.ts/ });
    expect(link).toHaveAttribute("href", `${PRIMARY}/src/a.ts`);
    fireEvent.click(link);
    expect(previewFile).toHaveBeenCalledWith(SESSION, {
      root: PRIMARY,
      path: "src/a.ts",
    });
  });

  it("Given a subagent ran elsewhere, When the main text follows, Then the subagent's location does not move the main one", () => {
    const all = rows([
      message(1, [
        ...bash("t1", WT),
        {
          type: "tool_use",
          toolUseId: "c1",
          toolName: "Bash",
          parentToolUseId: "agent-1",
          cwd: WT2,
        },
        { type: "text", text: "看 src/a.ts 吧" },
      ]),
    ]);
    renderRow(textRow(all, 1));

    expect(screen.getByRole("link", { name: /a\.ts/ })).toHaveAttribute(
      "href",
      `${WT}/src/a.ts`,
    );
  });
});

describe("绝对路径归属", () => {
  it("Given a worktree nested inside the primary root, When an absolute path inside it is clicked, Then the deepest root owns it", () => {
    const nested = `${PRIMARY}/.claude/worktrees/x`;
    renderLink(`${nested}/src/a.ts`, {
      roots: [...ROOTS, { path: nested, name: "x" }],
    });

    clickLink();

    expect(previewFile).toHaveBeenCalledWith(SESSION, {
      root: nested,
      path: "src/a.ts",
    });
  });

  it("Given an absolute path in an unclaimed worktree of the same repo, When clicked, Then it is claimed and then previewed in the new root", async () => {
    const wt3 = "/repo-wt3";
    claimPath.mockResolvedValue([...ROOTS, { path: wt3, name: "repo-wt3" }]);
    renderLink(`${wt3}/src/c.ts:7`, {
      roots: ROOTS,
      ports: ports({ claimPath }),
    });

    clickLink();

    await waitFor(() =>
      expect(previewFile).toHaveBeenCalledWith(
        SESSION,
        { root: wt3, path: "src/c.ts" },
        { line: 7 },
      ),
    );
    expect(claimPath).toHaveBeenCalledWith(SESSION, `${wt3}/src/c.ts`);
    expect(openPath).not.toHaveBeenCalled();
  });

  it("Given the claim fails, When the unclaimed path is clicked, Then it is opened like today's external file", async () => {
    claimPath.mockRejectedValue(new Error("not a repo"));
    renderLink("/elsewhere/c.ts:7", {
      roots: ROOTS,
      ports: ports({ claimPath }),
    });

    clickLink();

    await waitFor(() =>
      expect(openPath).toHaveBeenCalledWith("/elsewhere/c.ts:7"),
    );
    expect(previewFile).not.toHaveBeenCalled();
  });

  it("Given the claim succeeds but no root contains the path, When clicked, Then it is opened like today's external file", async () => {
    claimPath.mockResolvedValue(ROOTS);
    renderLink("/tmp/c.ts", { roots: ROOTS, ports: ports({ claimPath }) });

    clickLink();

    await waitFor(() => expect(openPath).toHaveBeenCalledWith("/tmp/c.ts"));
    expect(previewFile).not.toHaveBeenCalled();
  });
});

describe("预览、复制路径、系统打开同一个解析结果", () => {
  function renderWorktreeText() {
    const all = rows([
      message(1, [
        ...bash("t1", WT),
        { type: "text", text: "见 src/a.ts:12 处" },
      ]),
    ]);
    renderRow(textRow(all, 1));
    return screen.getByRole("link", { name: /a\.ts/ });
  }

  it("Given a worktree link, When previewed, Then the preview gets that root and the anchor", () => {
    fireEvent.click(renderWorktreeText());

    expect(previewFile).toHaveBeenCalledWith(
      SESSION,
      { root: WT, path: "src/a.ts" },
      { line: 12 },
    );
  });

  it("Given the host declines preview (click setting = system app), When clicked, Then system open gets the same worktree path", () => {
    previewFile.mockReturnValue(false);
    fireEvent.click(renderWorktreeText());

    expect(openPath).toHaveBeenCalledWith(`${WT}/src/a.ts:12`);
  });

  it("Given a worktree link, When its path is copied, Then the same worktree path is copied", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    fireEvent.focus(renderWorktreeText());
    fireEvent.click(
      await screen.findByRole("button", { name: /Copy full path/ }),
    );

    await waitFor(() =>
      expect(writeText).toHaveBeenCalledWith(`${WT}/src/a.ts:12`),
    );
  });
});

describe("悬浮卡根名", () => {
  it("Given a link owned by a non-primary root, When hovered, Then the card labels the root name before the path", async () => {
    renderLink(`${WT2}/src/b.ts`);
    fireEvent.focus(screen.getByRole("link", { name: /link/ }));

    const label = await screen.findByTestId("rich-link-popover-root");
    expect(label).toHaveTextContent("repo-wt2");
    expect(screen.getByTestId("rich-link-popover-path")).toHaveTextContent(
      "src/b.ts",
    );
  });

  it("Given a link owned by the primary root, When hovered, Then no root label is shown", async () => {
    renderLink(`${PRIMARY}/src/b.ts`);
    fireEvent.focus(screen.getByRole("link", { name: /link/ }));

    await screen.findByTestId("rich-link-popover");
    expect(screen.queryByTestId("rich-link-popover-root")).toBeNull();
  });
});

describe("远端会话", () => {
  it("Given a remote session, When a file in a remote worktree is clicked, Then it is previewed through the session and never reported as missing here", () => {
    renderLink(`${WT2}/src/b.ts`, { roots: ROOTS, location: "remote" });

    clickLink();

    expect(previewFile).toHaveBeenCalledWith(SESSION, {
      root: WT2,
      path: "src/b.ts",
    });
    expect(openPath).not.toHaveBeenCalled();
    expect(sonnerMocks.toast.error).not.toHaveBeenCalled();
  });

  it("Given a remote session and an unclaimed remote worktree, When clicked, Then it is claimed and previewed instead of saying the file isn't here", async () => {
    const wt3 = "/repo-wt3";
    claimPath.mockResolvedValue([...ROOTS, { path: wt3, name: "repo-wt3" }]);
    renderLink(`${wt3}/src/c.ts`, {
      roots: ROOTS,
      location: "remote",
      ports: ports({ claimPath }),
    });

    clickLink();

    await waitFor(() =>
      expect(previewFile).toHaveBeenCalledWith(SESSION, {
        root: wt3,
        path: "src/c.ts",
      }),
    );
    expect(openPath).not.toHaveBeenCalled();
    expect(sonnerMocks.toast.error).not.toHaveBeenCalled();
  });
});

describe("localhost 链接", () => {
  it("Given a session, When a localhost link is clicked, Then the host gets the session id so it can forward the port", () => {
    renderLink("http://localhost:3000/p?q=1");

    clickLink();

    expect(openExternalURL).toHaveBeenCalledWith(
      "http://localhost:3000/p?q=1",
      { sessionId: SESSION },
    );
  });

  it("Given forwarding fails, When the link is clicked, Then the failure is shown instead of being swallowed", async () => {
    openExternalURL.mockRejectedValue(new Error("device offline"));
    renderLink("http://localhost:3000/");

    clickLink();

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    expect(String(sonnerMocks.toast.error.mock.calls[0][0])).toContain(
      "device offline",
    );
  });
});

describe("宿主不提供工作根时（agentre-server）行为不变", () => {
  it("Given no roots, When text after a worktree tool call links a relative path, Then it resolves against the session cwd and preview gets today's relPath", () => {
    const all = rows([
      message(1, [
        ...bash("t1", WT),
        { type: "text", text: "改了 src/a.ts 这个文件" },
      ]),
    ]);
    renderRow(textRow(all, 1), { roots: undefined });

    const link = screen.getByRole("link", { name: /a\.ts/ });
    expect(link).toHaveAttribute("href", `${PRIMARY}/src/a.ts`);
    fireEvent.click(link);
    expect(previewFile).toHaveBeenCalledWith(SESSION, "src/a.ts");
  });

  it("Given no roots, When a sibling worktree path is clicked, Then it is not claimed and goes to system open as today", () => {
    renderLink(`${WT2}/src/b.ts`, {
      roots: undefined,
      ports: ports({ claimPath }),
    });

    clickLink();

    expect(openPath).toHaveBeenCalledWith(`${WT2}/src/b.ts`);
    expect(claimPath).not.toHaveBeenCalled();
    expect(previewFile).not.toHaveBeenCalled();
  });

  it("Given no roots, When hovered, Then there is no root label", async () => {
    renderLink(`${PRIMARY}/src/b.ts`, { roots: undefined });
    fireEvent.focus(screen.getByRole("link", { name: /link/ }));

    await screen.findByTestId("rich-link-popover");
    expect(screen.queryByTestId("rich-link-popover-root")).toBeNull();
  });
});
