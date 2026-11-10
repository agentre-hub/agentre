import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type * as React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { TranscriptBlock } from "../../dto";
import type { TranscriptPorts } from "../../ports";
import { TranscriptPortsProvider } from "../../ports-context";
import { __resetChatPanelScrollStateForTesting } from "../../chat-panel-scroll-state";

import { PlanCard } from "./card";

const resolvePlanAction = vi.fn().mockResolvedValue(undefined);
const ports: TranscriptPorts = {
  answerToolPermission: vi.fn().mockResolvedValue(undefined),
  answerUserQuestion: vi.fn().mockResolvedValue(undefined),
  answerToolApproval: vi.fn().mockResolvedValue(undefined),
  resolveExecApproval: vi.fn().mockResolvedValue({ status: "resolved" }),
  resolvePlanAction,
};

function renderCard(ui: React.ReactElement) {
  return render(
    <TranscriptPortsProvider ports={ports}>{ui}</TranscriptPortsProvider>,
  );
}

function planBlock(): TranscriptBlock {
  return {
    type: "tool_use",
    toolName: "ExitPlanMode",
    canonical: {
      kind: "plan.approve_request",
      planApprove: {
        requestId: "req-plan",
        planText: "1. do the thing",
        actions: [
          { id: "plan.approve", kind: "approve" },
          { id: "plan.refine", kind: "refine" },
        ],
      },
    },
  } as unknown as TranscriptBlock;
}

function approveButton() {
  return screen.getByRole("button", { name: /Approve/ });
}

describe("PlanCard 提交失败", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    __resetChatPanelScrollStateForTesting();
  });

  it("Given the plan port rejects with a machine message, When an action fails, Then the card shows the localized fact with the raw text in title and stays actionable", async () => {
    resolvePlanAction.mockRejectedValueOnce(
      new Error("rpc error: code = Unavailable desc = transport is closing"),
    );
    renderCard(<PlanCard toolBlock={planBlock()} sessionId={3} />);

    fireEvent.click(approveButton());

    const fact = await screen.findByText("Couldn't submit the plan action");
    expect(fact.getAttribute("title")).toBe(
      "rpc error: code = Unavailable desc = transport is closing",
    );
    expect(screen.queryByText(/rpc error/)).toBeNull();

    // 仍可再提交；成功后失败句消失。
    await waitFor(() => expect(approveButton()).not.toBeDisabled());
    fireEvent.click(approveButton());
    await waitFor(() => expect(resolvePlanAction).toHaveBeenCalledTimes(2));
    await waitFor(() =>
      expect(screen.queryByText("Couldn't submit the plan action")).toBeNull(),
    );
  });

  it("Given the plan port rejects with nothing readable, When an action fails, Then the fact is localized and no English fallback or empty title appears", async () => {
    resolvePlanAction.mockRejectedValueOnce(undefined);
    renderCard(<PlanCard toolBlock={planBlock()} sessionId={3} />);

    fireEvent.click(approveButton());

    const fact = await screen.findByText("Couldn't submit the plan action");
    expect(fact.hasAttribute("title")).toBe(false);
    expect(screen.queryByText("Submit failed")).toBeNull();
  });
});
