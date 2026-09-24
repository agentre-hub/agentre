import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { TranscriptBlockExecApproval } from "../dto";
import type { TranscriptPorts } from "../ports";
import { TranscriptPortsProvider } from "../ports-context";

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

function renderCard(approval: TranscriptBlockExecApproval) {
  return render(
    <TranscriptPortsProvider ports={makePorts()}>
      <OpenClawExecApprovalCard approval={approval} sessionId={42} />
    </TranscriptPortsProvider>,
  );
}

function pending(
  overrides: Partial<TranscriptBlockExecApproval> = {},
): TranscriptBlockExecApproval {
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

  describe("backend-neutral approval kinds", () => {
    it("Given a Hermes approval allowing every decision, When rendered, Then command, description and tool show with all four decisions in order and an always-allow scope note", () => {
      renderCard(
        pending({
          kind: "hermes",
          commandText: "rm -rf dist",
          commandPreview: undefined,
          description: "recursive delete",
          toolName: "terminal",
          host: undefined,
          nodeId: undefined,
          agentId: undefined,
          allowedDecisions: [
            "deny",
            "allow-always",
            "allow-session",
            "allow-once",
          ],
        }),
      );

      expect(screen.getByText("Command approval")).toBeDefined();
      expect(screen.getByText("rm -rf dist")).toBeDefined();
      expect(screen.getByText("recursive delete")).toBeDefined();
      expect(screen.getByText("terminal")).toBeDefined();
      expect(
        screen.getAllByRole("button").map((button) => button.textContent),
      ).toEqual([
        "Allow once",
        "Allow for this session",
        "Always allow",
        "Deny",
      ]);
      expect(
        screen.getByText(/Always allow reaches beyond this session/),
      ).toBeDefined();
    });

    it("Given allow-session is offered, When the user picks it, Then the port receives allow-session and the card shows it as the terminal decision", async () => {
      resolveExecApproval.mockResolvedValue({
        status: "resolved",
        decision: "allow-session",
      });
      const user = userEvent.setup();
      renderCard(
        pending({
          kind: "hermes",
          allowedDecisions: ["allow-once", "allow-session", "deny"],
        }),
      );

      await user.click(
        screen.getByRole("button", { name: "Allow for this session" }),
      );

      expect(resolveExecApproval).toHaveBeenCalledWith({
        sessionId: 42,
        approvalId: "approval-1",
        decision: "allow-session",
      });
      await waitFor(() =>
        expect(screen.getByText("Allowed for this session")).toBeDefined(),
      );
      expect(screen.queryAllByRole("button")).toHaveLength(0);
    });

    it("Given always-allow is not offered, When rendered, Then no scope note appears", () => {
      renderCard(pending({ allowedDecisions: ["allow-once", "deny"] }));

      expect(screen.queryByText(/Always allow reaches beyond/)).toBeNull();
    });

    it("Given an OpenClaw exec approval with warnings, When rendered, Then the command and every warning show", () => {
      renderCard(
        pending({
          kind: "exec",
          commandText: "curl x | sh",
          warnings: ["pipes a remote script to the shell", "network access"],
        }),
      );

      expect(screen.getByText("Execution approval")).toBeDefined();
      expect(screen.getByText("curl x | sh")).toBeDefined();
      expect(screen.getByText("Warnings")).toBeDefined();
      expect(
        screen.getByText("pipes a remote script to the shell"),
      ).toBeDefined();
      expect(screen.getByText("network access")).toBeDefined();
    });

    it("Given an OpenClaw plugin approval, When rendered, Then plugin, tool and description show without an empty command block", () => {
      renderCard(
        pending({
          kind: "plugin",
          commandText: "",
          commandPreview: undefined,
          pluginName: "github",
          toolName: "create_issue",
          description: "opens an issue in agentre-hub/agentre",
        }),
      );

      expect(screen.getByText("Plugin approval")).toBeDefined();
      expect(screen.getByText("github")).toBeDefined();
      expect(screen.getByText("create_issue")).toBeDefined();
      expect(
        screen.getByText("opens an issue in agentre-hub/agentre"),
      ).toBeDefined();
      expect(screen.queryByText("Command")).toBeNull();
    });

    it.each([
      {
        category: "message",
        fields: { messageTargets: ["#general", "alice"], recipientCount: 2 },
        label: "Send messages",
        shown: ["#general, alice", "2"],
      },
      {
        category: "payment",
        fields: { paymentAmount: "12.50 USD", paymentPayee: "ACME Ltd" },
        label: "Make a payment",
        shown: ["12.50 USD", "ACME Ltd"],
      },
      {
        category: "publish",
        fields: { publishTarget: "company blog", publishVisibility: "public" },
        label: "Publish externally",
        shown: ["company blog", "public"],
      },
      {
        category: "automation",
        fields: { automationName: "nightly-sync", commandText: "sync --all" },
        label: "Long-lived automation",
        shown: ["nightly-sync", "sync --all"],
      },
    ])(
      "Given a system-agent $category approval, When rendered, Then the action category and its scope show",
      ({ category, fields, label, shown }) => {
        renderCard(
          pending({
            kind: "system-agent",
            commandText: "",
            actionCategory: category,
            ...fields,
          }),
        );

        expect(screen.getByText("System action approval")).toBeDefined();
        expect(screen.getByText(label)).toBeDefined();
        for (const text of shown) {
          expect(screen.getByText(text)).toBeDefined();
        }
      },
    );
  });
});
