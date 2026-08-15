import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  TranscriptPortsProvider,
  type TranscriptPorts,
} from "@agentre-ai/agentre-ui";

import type { ExecApprovalData } from "@/stores/chat-streams-store";

import { OpenClawExecApprovalCard } from "./card";

// 卡片不再认识 Wails 绑定,只认注入的动作端口;测试注入自己的端口实现,
// 断言决议落到 resolveExecApproval 上,并按它的回包渲染终态。
const resolveExecApproval = vi.fn();

function makePorts(): TranscriptPorts {
  return {
    answerToolPermission: vi.fn(async () => {}),
    answerUserQuestion: vi.fn(async () => {}),
    answerToolApproval: vi.fn(async () => {}),
    resolveExecApproval,
    resolvePlanAction: vi.fn(async () => ({})),
  };
}

function renderCard(approval: ExecApprovalData) {
  return render(
    <TranscriptPortsProvider ports={makePorts()}>
      <OpenClawExecApprovalCard approval={approval} sessionId={42} />
    </TranscriptPortsProvider>,
  );
}

function pending(overrides: Partial<ExecApprovalData> = {}): ExecApprovalData {
  return {
    id: "approval-1",
    commandText: "git status --short",
    commandPreview: "git status --short",
    allowedDecisions: ["allow-once", "deny"],
    host: "node",
    nodeId: "node-7",
    agentId: "coder",
    status: "pending",
    expiresAtMs: Date.now() + 60_000,
    ...overrides,
  };
}

describe("OpenClawExecApprovalCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    resolveExecApproval.mockResolvedValue({
      status: "resolved",
      decision: "allow-once",
    });
  });

  it("Given a pending approval, When decisions are rendered, Then only Gateway allowedDecisions are offered", () => {
    renderCard(pending());

    expect(screen.getByText("Execution approval")).toBeDefined();
    expect(screen.getByText("git status --short")).toBeDefined();
    expect(screen.getByText("node-7")).toBeDefined();
    expect(screen.getByRole("button", { name: "Allow once" })).toBeDefined();
    expect(screen.getByRole("button", { name: "Deny" })).toBeDefined();
    expect(screen.queryByRole("button", { name: "Always allow" })).toBeNull();
  });

  it("Given allow-always is granted, When rendered, Then the optional decision is available", () => {
    renderCard(
      pending({
        allowedDecisions: ["allow-once", "allow-always", "deny"],
      }),
    );

    expect(screen.getByRole("button", { name: "Always allow" })).toBeDefined();
  });

  it("Given a resolution is in flight, When a decision is clicked repeatedly, Then one port call is made and all decisions are disabled", async () => {
    let settle!: (value: { status: string; decision: string }) => void;
    resolveExecApproval.mockReturnValue(
      new Promise((resolve) => {
        settle = resolve;
      }),
    );
    const user = userEvent.setup();
    renderCard(
      pending({
        allowedDecisions: ["allow-once", "allow-always", "deny"],
      }),
    );

    const allowOnce = screen.getByRole("button", { name: "Allow once" });
    await user.dblClick(allowOnce);

    expect(resolveExecApproval).toHaveBeenCalledTimes(1);
    expect(resolveExecApproval).toHaveBeenCalledWith({
      sessionId: 42,
      approvalId: "approval-1",
      decision: "allow-once",
    });
    for (const button of screen.getAllByRole("button")) {
      expect(button).toBeDisabled();
    }
    expect(screen.getByText("Submitting decision…")).toBeDefined();

    settle({ status: "resolved", decision: "allow-once" });
    await waitFor(() => expect(screen.getByText("Allowed once")).toBeDefined());
  });

  it("Given the port rejects, When the user retries, Then an inline error is cleared and the call can succeed", async () => {
    resolveExecApproval
      .mockRejectedValueOnce(new Error("gateway disconnected"))
      .mockResolvedValueOnce({ status: "resolved", decision: "deny" });
    const user = userEvent.setup();
    renderCard(pending());

    await user.click(screen.getByRole("button", { name: "Deny" }));
    expect(
      await screen.findByText("Could not submit the approval decision."),
    ).toBeDefined();
    expect(screen.getByRole("button", { name: "Deny" })).toBeEnabled();

    await user.click(screen.getByRole("button", { name: "Deny" }));
    await waitFor(() => expect(resolveExecApproval).toHaveBeenCalledTimes(2));
    expect(
      screen.queryByText("Could not submit the approval decision."),
    ).toBeNull();
    expect(screen.getByText("Denied")).toBeDefined();
  });

  it("Given an expired approval, When rendered, Then it is read-only", () => {
    renderCard(pending({ status: "expired" }));

    expect(screen.getByText("Expired")).toBeDefined();
    expect(screen.queryAllByRole("button")).toHaveLength(0);
  });

  it("Given an approval resolved by another client, When rendered, Then resolution metadata is shown without an exec-finished claim", () => {
    renderCard(
      pending({
        status: "resolved",
        decision: "allow-always",
        resolvedBy: "operator-device-2",
      }),
    );

    expect(screen.getByText("Always allowed")).toBeDefined();
    expect(screen.getByText("operator-device-2")).toBeDefined();
    expect(screen.queryByText(/execution finished/i)).toBeNull();
    expect(screen.queryAllByRole("button")).toHaveLength(0);
  });
});
