import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type * as React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  TranscriptLiveStateProvider,
  TranscriptPortsProvider,
} from "@agentre-ai/agentre-ui";
import type {
  TranscriptBlock,
  TranscriptLiveState,
  TranscriptPorts,
} from "@agentre-ai/agentre-ui";

import { ToolPermissionCard } from "./card";

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
    // 卡片默认折叠;展开后 JSON 入参出现并带折叠控制
    fireEvent.click(screen.getByRole("button", { expanded: false }));
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
    const header = screen.getByRole("button", { expanded: false });
    fireEvent.click(header);
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

    fireEvent.click(screen.getByRole("button", { name: "Allow Once" }));

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

    expect(await screen.findByText("no waiting tool permission")).toBeDefined();
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
