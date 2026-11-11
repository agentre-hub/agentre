import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type * as React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { TranscriptBlock } from "../../dto";
import type { TranscriptPorts } from "../../ports";
import {
  TranscriptLiveStateProvider,
  type TranscriptLiveState,
} from "../../live-state";
import { TranscriptPortsProvider } from "../../ports-context";

import { ToolPermissionCard } from "./card";
import { ShortcutHintsProvider } from "./shortcut-hints";

// 卡片从 TranscriptPortsProvider 取动作端口,少了 provider 会在挂载期就抛。
const answerToolPermission = vi.fn().mockResolvedValue(undefined);
const ports: TranscriptPorts = {
  answerToolPermission,
  answerUserQuestion: vi.fn().mockResolvedValue(undefined),
  answerToolApproval: vi.fn().mockResolvedValue(undefined),
  resolveExecApproval: vi.fn().mockResolvedValue({ status: "resolved" }),
  resolvePlanAction: vi.fn().mockResolvedValue(undefined),
};

// 乐观更新写回宿主的实时流,卡片只认 TranscriptLiveState 契约(桌面端接
// chat-streams-store,只读转录的宿主什么都不接)。liveState 必须是模块级常量
// —— 它的成员被当 hook 调用。
const markToolPermissionResolved = vi.fn();
const liveState: TranscriptLiveState = {
  useIsStreamActive: () => false,
  markToolPermissionResolved,
};

function renderCard(ui: React.ReactElement) {
  return render(
    <TranscriptPortsProvider ports={ports}>
      <TranscriptLiveStateProvider value={liveState}>
        {ui}
      </TranscriptLiveStateProvider>
    </TranscriptPortsProvider>,
  );
}

describe("ToolPermissionCard JSON input", () => {
  it("collapses a long tool input JSON with an expand control once expanded", () => {
    const block = {
      type: "tool_use",
      toolName: "org_update_agent",
      toolUseId: "perm-1",
      canonical: {
        kind: "tool.permission",
        toolPermission: {
          requestId: "req-1",
          toolName: "org_update_agent",
          toolInput: { system_prompt: "x".repeat(300) },
          resolved: false,
        },
      },
    } as unknown as TranscriptBlock;
    renderCard(
      <ToolPermissionCard toolBlock={block} sessionId={1} messageId={1} />,
    );
    // 待审批卡默认展开;JSON 入参直接可见
    expect(screen.getByRole("button", { expanded: true })).toBeDefined();
    expect(screen.queryByRole("button", { name: "Expand all" })).toBeNull();
  });

  it("Given an expanded card, When collapsed, Then the input JSON stays mounted through the collapse transition and unmounts after it ends", () => {
    const block = {
      type: "tool_use",
      toolName: "org_update_agent",
      toolUseId: "perm-1",
      canonical: {
        kind: "tool.permission",
        toolPermission: {
          requestId: "req-1",
          toolName: "org_update_agent",
          toolInput: { system_prompt: "x".repeat(300) },
          resolved: false,
        },
      },
    } as unknown as TranscriptBlock;
    renderCard(
      <ToolPermissionCard toolBlock={block} sessionId={1} messageId={1} />,
    );
    const header = screen.getByRole("button", { expanded: true });
    expect(header).toHaveAttribute("aria-expanded", "true");
    const grid = header.nextElementSibling as HTMLElement;
    expect(within(grid).getByText(/system_prompt/)).toBeDefined();

    fireEvent.click(header);
    expect(header).toHaveAttribute("aria-expanded", "false");
    // 收缩动画期间内容仍挂载,过渡结束才卸载。
    expect(within(grid).getByText(/system_prompt/)).toBeDefined();
    fireEvent.transitionEnd(grid);
    expect(within(grid).queryByText(/system_prompt/)).toBeNull();
  });
});

describe("ToolPermissionCard optimistic resolution", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  function pendingBlock(): TranscriptBlock {
    return {
      type: "tool_use",
      toolName: "Bash",
      toolUseId: "perm-2",
      canonical: {
        kind: "tool.permission",
        toolPermission: {
          requestId: "req-1",
          toolName: "Bash",
          toolInput: { command: "ls" },
          resolved: false,
        },
      },
    } as unknown as TranscriptBlock;
  }

  it("Given a pending permission, When Allow Once is clicked, Then the host live state is marked resolved for this session and message", async () => {
    renderCard(
      <ToolPermissionCard
        toolBlock={pendingBlock()}
        sessionId={7}
        messageId={42}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Allow" }));

    expect(markToolPermissionResolved).toHaveBeenCalledWith({
      sessionId: 7,
      assistantMessageId: 42,
      toolPermission: {
        requestId: "req-1",
        toolName: "Bash",
        toolInput: { command: "ls" },
        resolved: true,
        allowed: true,
        alwaysAllow: false,
      },
    });
    await waitFor(() => {
      expect(answerToolPermission).toHaveBeenCalledWith({
        sessionId: 7,
        requestId: "req-1",
        allow: true,
        alwaysAllowSession: false,
      });
    });
  });

  it("Given the answer port rejects, When the approval fails, Then the optimistic resolution is rolled back and the error surfaces", async () => {
    answerToolPermission.mockRejectedValueOnce(
      new Error("no waiting tool permission"),
    );

    renderCard(
      <ToolPermissionCard
        toolBlock={pendingBlock()}
        sessionId={7}
        messageId={42}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Reject" }));

    // 正文是本地化事实句，机器原话只进 title。
    const fact = await screen.findByText("Approval submission failed");
    expect(fact.getAttribute("title")).toBe("no waiting tool permission");
    expect(screen.queryByText("no waiting tool permission")).toBeNull();
    expect(markToolPermissionResolved).toHaveBeenLastCalledWith({
      sessionId: 7,
      assistantMessageId: 42,
      toolPermission: {
        requestId: "req-1",
        toolName: "Bash",
        toolInput: { command: "ls" },
        resolved: false,
        allowed: false,
        alwaysAllow: false,
      },
    });
  });
});

function permBlock(
  toolName: string,
  toolInput: Record<string, unknown>,
  requestId = "req-1",
  resolved = false,
): TranscriptBlock {
  return {
    type: "tool_use",
    toolName,
    toolUseId: `perm-${requestId}`,
    canonical: {
      kind: "tool.permission",
      toolPermission: { requestId, toolName, toolInput, resolved },
    },
  } as unknown as TranscriptBlock;
}

describe("ToolPermissionCard expansion and summaries", () => {
  beforeEach(() => vi.clearAllMocks());

  it("is expanded while pending and collapsed once resolved", () => {
    const { unmount } = renderCard(
      <ToolPermissionCard
        toolBlock={permBlock("Bash", { command: "ls" })}
        sessionId={1}
        messageId={1}
      />,
    );
    expect(screen.getByRole("button", { expanded: true })).toBeDefined();
    unmount();
    renderCard(
      <ToolPermissionCard
        toolBlock={permBlock("Bash", { command: "ls" }, "req-1", true)}
        sessionId={1}
        messageId={1}
      />,
    );
    expect(screen.getByRole("button", { expanded: false })).toBeDefined();
  });

  it("shows the command for Bash", () => {
    renderCard(
      <ToolPermissionCard
        toolBlock={permBlock("Bash", { command: "rm -rf build && make" })}
        sessionId={1}
        messageId={1}
      />,
    );
    const body = screen.getByTestId("tool-permission-body");
    expect(body.textContent).toContain("rm -rf build && make");
    expect(body.textContent).not.toContain('"command"');
  });

  it("shows path and a small diff for Edit", () => {
    renderCard(
      <ToolPermissionCard
        toolBlock={permBlock("Edit", {
          file_path: "/src/app.ts",
          old_string: "const a = 1;",
          new_string: "const a = 2;",
        })}
        sessionId={1}
        messageId={1}
      />,
    );
    const body = screen.getByTestId("tool-permission-body");
    expect(body.textContent).toContain("/src/app.ts");
    expect(
      screen.getByTestId("tool-permission-diff-del").textContent,
    ).toContain("const a = 1;");
    expect(
      screen.getByTestId("tool-permission-diff-add").textContent,
    ).toContain("const a = 2;");
    expect(body.textContent).not.toContain("old_string");
  });

  it("shows path and line count for Write", () => {
    renderCard(
      <ToolPermissionCard
        toolBlock={permBlock("Write", {
          file_path: "/src/new.ts",
          content: "a\nb\nc",
        })}
        sessionId={1}
        messageId={1}
      />,
    );
    const body = screen.getByTestId("tool-permission-body");
    expect(body.textContent).toContain("/src/new.ts");
    expect(body.textContent).toContain("3 lines");
    expect(body.textContent).not.toContain('"content"');
  });

  it("falls back to formatted JSON for other tools", () => {
    renderCard(
      <ToolPermissionCard
        toolBlock={permBlock("WebFetch", { url: "https://x.dev" })}
        sessionId={1}
        messageId={1}
      />,
    );
    expect(screen.getByTestId("tool-permission-body").textContent).toContain(
      '"url": "https://x.dev"',
    );
  });

  it("falls back to JSON when Edit input is malformed", () => {
    renderCard(
      <ToolPermissionCard
        toolBlock={permBlock("Edit", { file_path: 5 })}
        sessionId={1}
        messageId={1}
      />,
    );
    expect(screen.getByTestId("tool-permission-body").textContent).toContain(
      '"file_path": 5',
    );
  });
});

describe("ToolPermissionCard buttons and keyboard", () => {
  beforeEach(() => vi.clearAllMocks());

  const one = (id = "req-1") => (
    <ToolPermissionCard
      toolBlock={permBlock("Bash", { command: "ls" }, id)}
      sessionId={7}
      messageId={42}
    />
  );

  it("styles Allow primary, session-allow ghost, Reject ghost destructive", () => {
    renderCard(one());
    expect(screen.getByRole("button", { name: "Allow" })).toHaveAttribute(
      "data-variant",
      "default",
    );
    const session = screen.getByRole("button", {
      name: "Always Allow This Session",
    });
    expect(session).toHaveAttribute("data-variant", "ghost");
    const reject = screen.getByRole("button", { name: "Reject" });
    expect(reject).toHaveAttribute("data-variant", "ghost");
    expect(reject.className).toContain("text-destructive-text");
  });

  it("shows kbd hints on Allow and Reject", () => {
    renderCard(one());
    expect(
      within(screen.getByRole("button", { name: "Allow" })).getByText("↵")
        .tagName,
    ).toBe("KBD");
    expect(
      within(screen.getByRole("button", { name: "Reject" })).getByText("Esc")
        .tagName,
    ).toBe("KBD");
  });

  it("Given ShortcutHintsProvider hidden, Then Allow and Reject carry no keycaps", () => {
    renderCard(<ShortcutHintsProvider hidden>{one()}</ShortcutHintsProvider>);
    expect(screen.getByRole("button", { name: "Allow" })).toBeDefined();
    expect(screen.getByRole("button", { name: "Reject" })).toBeDefined();
    expect(document.querySelector("kbd")).toBeNull();
  });

  it("Given a nested provider without a value, Then the outer hidden switch still applies", () => {
    renderCard(
      <ShortcutHintsProvider hidden>
        <ShortcutHintsProvider>{one()}</ShortcutHintsProvider>
      </ShortcutHintsProvider>,
    );
    expect(document.querySelector("kbd")).toBeNull();
  });

  it("focuses Allow on mount", () => {
    renderCard(one());
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "Allow" }),
    );
  });

  it("does not steal focus from a composer textarea", () => {
    const ta = document.createElement("textarea");
    document.body.appendChild(ta);
    ta.focus();
    renderCard(one());
    expect(document.activeElement).toBe(ta);
    ta.remove();
  });

  it("Enter allows and Esc denies", async () => {
    const { unmount } = renderCard(one());
    fireEvent.keyDown(screen.getByRole("button", { name: "Allow" }), {
      key: "Enter",
    });
    await waitFor(() =>
      expect(answerToolPermission).toHaveBeenCalledWith(
        expect.objectContaining({ allow: true, alwaysAllowSession: false }),
      ),
    );
    unmount();
    vi.clearAllMocks();
    renderCard(one());
    fireEvent.keyDown(screen.getByRole("button", { name: "Allow" }), {
      key: "Escape",
    });
    await waitFor(() =>
      expect(answerToolPermission).toHaveBeenCalledWith(
        expect.objectContaining({ allow: false }),
      ),
    );
  });

  it("ignores keys during IME composition", () => {
    renderCard(one());
    fireEvent.keyDown(screen.getByRole("button", { name: "Allow" }), {
      key: "Enter",
      isComposing: true,
    });
    fireEvent.keyDown(screen.getByRole("button", { name: "Allow" }), {
      key: "Escape",
      keyCode: 229,
    });
    expect(answerToolPermission).not.toHaveBeenCalled();
  });

  it("only the card containing focus responds when several are pending", async () => {
    renderCard(
      <>
        {one("req-1")}
        {one("req-2")}
      </>,
    );
    const allows = screen.getAllByRole("button", { name: "Allow" });
    expect(document.activeElement).toBe(allows[0]);
    fireEvent.keyDown(allows[1], { key: "Escape" });
    await waitFor(() => expect(answerToolPermission).toHaveBeenCalledTimes(1));
    expect(answerToolPermission).toHaveBeenCalledWith(
      expect.objectContaining({ requestId: "req-2", allow: false }),
    );
  });

  it("does not respond to keys outside the card", () => {
    renderCard(one());
    fireEvent.keyDown(document.body, { key: "Enter" });
    expect(answerToolPermission).not.toHaveBeenCalled();
  });

  it("does not hijack Enter on the Reject button", () => {
    renderCard(one());
    fireEvent.keyDown(screen.getByRole("button", { name: "Reject" }), {
      key: "Enter",
    });
    expect(answerToolPermission).not.toHaveBeenCalled();
  });
});
