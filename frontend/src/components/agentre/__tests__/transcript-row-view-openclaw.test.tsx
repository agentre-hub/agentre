import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
  TranscriptPortsProvider,
  type TranscriptPorts,
} from "@agentre-ai/agentre-ui";

import {
  TranscriptRenderContext,
  TranscriptRowView,
} from "../transcript-row-view";

import type { TranscriptRow } from "@agentre-ai/agentre-ui";

// 转录树里的交互卡片从 context 取动作端口,少了 Provider 会在挂载期直接抛。
const ports: TranscriptPorts = {
  answerToolPermission: vi.fn(async () => {}),
  answerUserQuestion: vi.fn(async () => {}),
  answerToolApproval: vi.fn(async () => {}),
  resolveExecApproval: vi.fn(async () => ({ status: "resolved" })),
  resolvePlanAction: vi.fn(async () => ({})),
};

describe("TranscriptRowView OpenClaw approval", () => {
  it("Given an exec_approval transcript item, When rendered, Then the production approval card is used", () => {
    const row = {
      key: "message:7:exec_approval:exec-approval:exec-1",
      messageId: 7,
      message: {
        id: 7,
        role: "assistant",
        blocks: [],
        createtime: 0,
        durationMs: 0,
        promptTokens: 0,
        completionTokens: 0,
      },
      item: {
        type: "exec_approval",
        uiStateKey: "message:7:exec_approval:exec-approval:exec-1",
        block: {
          type: "exec_approval",
          execApproval: {
            id: "exec-1",
            commandText: "git status --short",
            allowedDecisions: ["allow-once", "deny"],
            status: "pending",
          },
        },
      },
      isFirstOfMessage: true,
      isLastOfMessage: true,
      autonomous: false,
    } as unknown as TranscriptRow;

    render(
      <TranscriptPortsProvider ports={ports}>
        <TranscriptRenderContext.Provider
          value={{
            agentName: "OpenClaw",
            agentColor: "agent-1",
            sessionId: 42,
          }}
        >
          <TranscriptRowView
            row={row}
            liveTail=""
            liveBlocks={undefined}
            liveRetry={null}
            showIndicator={false}
            compacting={false}
            reconnecting={false}
          />
        </TranscriptRenderContext.Provider>
      </TranscriptPortsProvider>,
    );

    expect(screen.getByTestId("openclaw-exec-approval-card")).toBeDefined();
    expect(screen.getByText("git status --short")).toBeDefined();
    expect(screen.queryByText(/Unknown block/i)).toBeNull();
  });
});
