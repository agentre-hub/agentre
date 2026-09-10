import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type * as React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { TranscriptBlock } from "../../dto";
import type { TranscriptPorts } from "../../ports";
import {
  TranscriptLiveStateProvider,
  type TranscriptLiveState,
} from "../../live-state";
import { TranscriptPortsProvider } from "../../ports-context";
import { __resetChatPanelScrollStateForTesting } from "../../chat-panel-scroll-state";

import { PlanApproveCard } from "./card";

// 卡片不再直接调 Wails,而是从 TranscriptPortsProvider 取动作端口;这里注入
// 一份 mock 端口,断言打在端口上。
const resolvePlanAction = vi.fn().mockResolvedValue(undefined);
const ports: TranscriptPorts = {
  answerToolPermission: vi.fn().mockResolvedValue(undefined),
  answerUserQuestion: vi.fn().mockResolvedValue(undefined),
  answerToolApproval: vi.fn().mockResolvedValue(undefined),
  resolveExecApproval: vi.fn().mockResolvedValue({ status: "resolved" }),
  resolvePlanAction,
};

// 「会话是否有流在跑」是宿主状态,卡片只通过 TranscriptLiveState 契约读它。
// liveState 必须是模块级常量(它的成员被当 hook 调用),开关放在外面的变量上,
// 由每条用例在 render 之前设定。
let streamActive = false;
const liveState: TranscriptLiveState = {
  useIsStreamActive: () => streamActive,
  markToolPermissionResolved: () => {},
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

// PlanApproveCard 现在只看 canonical.actions[],不再读 session meta
// 的 permissionModeAtLaunch(那条规则迁到 backend handlers/plan_approve.go)。

function blockWithActions(
  actions: {
    id: string;
    kind: "approve" | "refine";
    requiresFeedback?: boolean;
  }[],
): TranscriptBlock {
  return {
    type: "tool_use",
    toolName: "ExitPlanMode",
    canonical: {
      kind: "plan.approve_request",
      planApprove: {
        requestId: "req-1",
        planText: "# plan\n- step 1",
        actions,
      },
    },
  } as unknown as TranscriptBlock;
}

function actionPlanBlock(): TranscriptBlock {
  return {
    type: "plan",
    text: "# Plan\n- inspect\n- patch",
    canonical: {
      kind: "plan.update",
      planUpdate: {
        text: "# Plan\n- inspect\n- patch",
        actions: [
          { id: "plan.execute", kind: "approve" },
          { id: "plan.refine", kind: "refine", requiresFeedback: true },
        ],
        steps: [],
      },
    },
  } as unknown as TranscriptBlock;
}

describe("PlanApproveCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    __resetChatPanelScrollStateForTesting();
    streamActive = false;
  });

  it("renders nothing without canonical", () => {
    const block = { type: "tool_use" } as unknown as TranscriptBlock;
    const { container } = renderCard(
      <PlanApproveCard toolBlock={block} sessionId={1} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it("renders pending state with header copy", () => {
    const block = blockWithActions([
      { id: "plan.approve.accept_edits", kind: "approve" },
      { id: "plan.approve.manual", kind: "approve" },
      { id: "plan.refine", kind: "refine", requiresFeedback: true },
    ]);
    renderCard(<PlanApproveCard toolBlock={block} sessionId={1} />);
    expect(screen.getByText("AI Submitted an Execution Plan")).toBeDefined();
    expect(screen.getByText("Continue Planning")).toBeDefined();
  });

  it("non-bypass actions: 渲染 accept_edits + manual + refine, 无 bypass", () => {
    const block = blockWithActions([
      { id: "plan.approve.accept_edits", kind: "approve" },
      { id: "plan.approve.manual", kind: "approve" },
      { id: "plan.refine", kind: "refine", requiresFeedback: true },
    ]);
    renderCard(<PlanApproveCard toolBlock={block} sessionId={1} />);
    expect(screen.getByText("Approve and Switch to Auto Mode")).toBeDefined();
    expect(screen.getByText("Approve, Confirm Edits Manually")).toBeDefined();
    expect(screen.queryByText("Approve and Bypass Permissions")).toBeNull();
  });

  it("bypass actions: 渲染 bypass + manual + refine, 无 accept_edits", () => {
    const block = blockWithActions([
      { id: "plan.approve.bypass_permissions", kind: "approve" },
      { id: "plan.approve.manual", kind: "approve" },
      { id: "plan.refine", kind: "refine", requiresFeedback: true },
    ]);
    renderCard(<PlanApproveCard toolBlock={block} sessionId={1} />);
    expect(screen.getByText("Approve and Bypass Permissions")).toBeDefined();
    expect(screen.getByText("Approve, Confirm Edits Manually")).toBeDefined();
    expect(screen.queryByText("Approve and Switch to Auto Mode")).toBeNull();
  });

  it("点 accept_edits → resolvePlanAction(plan.approve.accept_edits)", async () => {
    const block = blockWithActions([
      { id: "plan.approve.accept_edits", kind: "approve" },
      { id: "plan.approve.manual", kind: "approve" },
      { id: "plan.refine", kind: "refine", requiresFeedback: true },
    ]);
    renderCard(<PlanApproveCard toolBlock={block} sessionId={1} />);
    fireEvent.click(screen.getByText("Approve and Switch to Auto Mode"));
    await waitFor(() => {
      expect(resolvePlanAction).toHaveBeenCalledWith(
        expect.objectContaining({
          sessionId: 1,
          requestId: "req-1",
          actionId: "plan.approve.accept_edits",
          feedback: "",
        }),
      );
    });
  });

  it("点 manual → resolvePlanAction(plan.approve.manual)", async () => {
    const block = blockWithActions([
      { id: "plan.approve.accept_edits", kind: "approve" },
      { id: "plan.approve.manual", kind: "approve" },
      { id: "plan.refine", kind: "refine", requiresFeedback: true },
    ]);
    renderCard(<PlanApproveCard toolBlock={block} sessionId={1} />);
    fireEvent.click(screen.getByText("Approve, Confirm Edits Manually"));
    await waitFor(() => {
      expect(resolvePlanAction).toHaveBeenCalledWith(
        expect.objectContaining({
          sessionId: 1,
          requestId: "req-1",
          actionId: "plan.approve.manual",
        }),
      );
    });
  });

  it("点 bypass → resolvePlanAction(plan.approve.bypass_permissions)", async () => {
    const block = blockWithActions([
      { id: "plan.approve.bypass_permissions", kind: "approve" },
      { id: "plan.approve.manual", kind: "approve" },
      { id: "plan.refine", kind: "refine", requiresFeedback: true },
    ]);
    renderCard(<PlanApproveCard toolBlock={block} sessionId={1} />);
    fireEvent.click(screen.getByText("Approve and Bypass Permissions"));
    await waitFor(() => {
      expect(resolvePlanAction).toHaveBeenCalledWith(
        expect.objectContaining({
          sessionId: 1,
          requestId: "req-1",
          actionId: "plan.approve.bypass_permissions",
        }),
      );
    });
  });

  it("refine 按钮展开 feedback 并传给 resolvePlanAction(plan.refine)", async () => {
    const block = blockWithActions([
      { id: "plan.approve.accept_edits", kind: "approve" },
      { id: "plan.approve.manual", kind: "approve" },
      { id: "plan.refine", kind: "refine", requiresFeedback: true },
    ]);
    renderCard(<PlanApproveCard toolBlock={block} sessionId={1} />);
    fireEvent.click(screen.getByText("Continue Planning"));
    fireEvent.change(screen.getByPlaceholderText(/step 2/), {
      target: { value: "再细一些" },
    });
    fireEvent.click(screen.getByText("Send Feedback and Continue Planning"));
    await waitFor(() => {
      expect(resolvePlanAction).toHaveBeenCalledWith(
        expect.objectContaining({
          sessionId: 1,
          requestId: "req-1",
          actionId: "plan.refine",
          feedback: "再细一些",
        }),
      );
    });
  });

  it("Given feedback draft, When the plan card remounts in the same tab, Then it restores the open editor and text", () => {
    const block = blockWithActions([
      { id: "plan.approve.accept_edits", kind: "approve" },
      { id: "plan.refine", kind: "refine", requiresFeedback: true },
    ]);
    const view = renderCard(
      <PlanApproveCard
        toolBlock={block}
        sessionId={1}
        tabStateKey="tab-a"
        uiStateKey="plan:req-1"
      />,
    );

    fireEvent.click(screen.getByText("Continue Planning"));
    fireEvent.change(screen.getByPlaceholderText(/step 2/), {
      target: { value: "keep this feedback" },
    });

    view.unmount();
    renderCard(
      <PlanApproveCard
        toolBlock={block}
        sessionId={1}
        tabStateKey="tab-a"
        uiStateKey="plan:req-1"
      />,
    );

    expect(screen.getByPlaceholderText(/step 2/)).toHaveValue(
      "keep this feedback",
    );
  });

  it("Given feedback draft in one tab, When the same plan remounts in another tab, Then it does not restore the old draft", () => {
    const block = blockWithActions([
      { id: "plan.approve.accept_edits", kind: "approve" },
      { id: "plan.refine", kind: "refine", requiresFeedback: true },
    ]);
    const view = renderCard(
      <PlanApproveCard
        toolBlock={block}
        sessionId={1}
        tabStateKey="tab-a"
        uiStateKey="plan:req-1"
      />,
    );
    fireEvent.click(screen.getByText("Continue Planning"));
    fireEvent.change(screen.getByPlaceholderText(/step 2/), {
      target: { value: "tab A feedback" },
    });

    view.unmount();
    renderCard(
      <PlanApproveCard
        toolBlock={block}
        sessionId={1}
        tabStateKey="tab-b"
        uiStateKey="plan:req-1"
      />,
    );

    expect(screen.queryByPlaceholderText(/step 2/)).toBeNull();
  });

  it("Given saved feedback draft, When refine is submitted, Then remounting does not restore it", async () => {
    const block = blockWithActions([
      { id: "plan.approve.accept_edits", kind: "approve" },
      { id: "plan.refine", kind: "refine", requiresFeedback: true },
    ]);
    const view = renderCard(
      <PlanApproveCard
        toolBlock={block}
        sessionId={1}
        tabStateKey="tab-a"
        uiStateKey="plan:req-1"
      />,
    );
    fireEvent.click(screen.getByText("Continue Planning"));
    fireEvent.change(screen.getByPlaceholderText(/step 2/), {
      target: { value: "feedback before submit" },
    });
    fireEvent.click(screen.getByText("Send Feedback and Continue Planning"));
    await waitFor(() => expect(resolvePlanAction).toHaveBeenCalled());

    view.unmount();
    renderCard(
      <PlanApproveCard
        toolBlock={block}
        sessionId={1}
        tabStateKey="tab-a"
        uiStateKey="plan:req-1"
      />,
    );

    expect(screen.queryByPlaceholderText(/step 2/)).toBeNull();
  });

  it("renders resolved-allowed banner without action buttons", () => {
    const block = {
      type: "tool_use",
      toolName: "ExitPlanMode",
      canonical: {
        kind: "plan.approve_request",
        planApprove: {
          requestId: "req-1",
          planText: "x",
          resolved: true,
          allowed: true,
        },
      },
    } as unknown as TranscriptBlock;
    renderCard(<PlanApproveCard toolBlock={block} sessionId={1} />);
    expect(screen.getByText("Plan Approved")).toBeDefined();
    expect(screen.getByText("Start executing the plan")).toBeDefined();
    expect(screen.queryByText("Approve and Switch to Auto Mode")).toBeNull();
    expect(screen.queryByText("Continue Planning")).toBeNull();
  });

  it("renders type=plan block from canonical.plan.update actions", () => {
    renderCard(<PlanApproveCard toolBlock={actionPlanBlock()} sessionId={1} />);
    expect(screen.getByTestId("plan-card")).toBeDefined();
    expect(screen.getByText("Execute Plan")).toBeDefined();
    expect(screen.getByText("Refine Plan")).toBeDefined();
    expect(
      screen.getByText(
        "Choose the next action, or send feedback to keep planning",
      ),
    ).toBeDefined();
  });

  it("plan.execute action does not require a requestId", async () => {
    renderCard(<PlanApproveCard toolBlock={actionPlanBlock()} sessionId={1} />);
    fireEvent.click(screen.getByText("Execute Plan"));
    await waitFor(() => {
      expect(resolvePlanAction).toHaveBeenCalledWith(
        expect.objectContaining({
          sessionId: 1,
          requestId: "",
          actionId: "plan.execute",
          feedback: "",
        }),
      );
    });
  });

  it("keeps request-backed approval actions enabled while the session stream is waiting", () => {
    streamActive = true;
    const block = blockWithActions([
      { id: "plan.approve.accept_edits", kind: "approve" },
      { id: "plan.refine", kind: "refine", requiresFeedback: true },
    ]);

    renderCard(<PlanApproveCard toolBlock={block} sessionId={1} />);

    const approveButton = screen
      .getByText("Approve and Switch to Auto Mode")
      .closest("button") as HTMLButtonElement;
    const refineButton = screen
      .getByText("Continue Planning")
      .closest("button") as HTMLButtonElement;
    expect(approveButton.disabled).toBe(false);
    expect(refineButton.disabled).toBe(false);
  });

  it("disables requestless plan actions while the session has an active stream", () => {
    streamActive = true;

    renderCard(<PlanApproveCard toolBlock={actionPlanBlock()} sessionId={1} />);

    const executeButton = screen
      .getByText("Execute Plan")
      .closest("button") as HTMLButtonElement;
    expect(executeButton.disabled).toBe(true);
  });

  it("plan.execute starts the returned stream in the parent transcript", async () => {
    const onPlanActionStarted = vi.fn();
    resolvePlanAction.mockResolvedValueOnce({
      sessionId: 1,
      userMessageId: 10,
      assistantMessageId: 11,
      stream: "chat.stream.1.11",
    });

    renderCard(
      <PlanApproveCard
        toolBlock={actionPlanBlock()}
        sessionId={1}
        onPlanActionStarted={onPlanActionStarted}
      />,
    );
    fireEvent.click(screen.getByText("Execute Plan"));

    await waitFor(() => {
      expect(onPlanActionStarted).toHaveBeenCalledWith(
        {
          sessionId: 1,
          userMessageId: 10,
          assistantMessageId: 11,
          stream: "chat.stream.1.11",
        },
        "Implement the plan.",
      );
    });
  });

  it("hides requestless plan actions after successful submission", async () => {
    resolvePlanAction.mockResolvedValueOnce({
      sessionId: 1,
      userMessageId: 10,
      assistantMessageId: 11,
      stream: "chat.stream.1.11",
    });

    renderCard(<PlanApproveCard toolBlock={actionPlanBlock()} sessionId={1} />);
    fireEvent.click(screen.getByText("Execute Plan"));

    await waitFor(() => {
      expect(screen.queryByText("Execute Plan")).toBeNull();
    });
    expect(screen.getByText("Plan Approved")).toBeDefined();
  });

  it("shows backend error detail when plan action submission rejects", async () => {
    const err = {};
    Object.defineProperty(err, "message", {
      value: "当前会话已有进行中的对话，请稍后再试",
      enumerable: false,
    });
    resolvePlanAction.mockRejectedValueOnce(err);

    renderCard(<PlanApproveCard toolBlock={actionPlanBlock()} sessionId={1} />);
    fireEvent.click(screen.getByText("Execute Plan"));

    expect(
      await screen.findByText("当前会话已有进行中的对话，请稍后再试"),
    ).toBeDefined();
  });

  // TranscriptCard tone 派生:未决→pending(border-primary)、已批准→done
  // (border-status-running/50)、其余已解决态(如 refine)→default(border-border)。
  // 三态各锁一个断言,防止 tone 三元表达式静默回归。
  it("Given an unresolved plan, When rendered, Then the card uses the pending tone border", () => {
    const block = blockWithActions([
      { id: "plan.approve.accept_edits", kind: "approve" },
      { id: "plan.refine", kind: "refine", requiresFeedback: true },
    ]);
    renderCard(<PlanApproveCard toolBlock={block} sessionId={1} />);
    expect(screen.getByTestId("plan-card")).toHaveClass("border-primary");
  });

  it("Given a resolved and approved plan, When rendered, Then the card uses the done tone border", () => {
    const block = {
      type: "tool_use",
      toolName: "ExitPlanMode",
      canonical: {
        kind: "plan.approve_request",
        planApprove: {
          requestId: "req-1",
          planText: "x",
          resolved: true,
          allowed: true,
        },
      },
    } as unknown as TranscriptBlock;
    renderCard(<PlanApproveCard toolBlock={block} sessionId={1} />);
    expect(screen.getByTestId("plan-card")).toHaveClass(
      "border-status-running/50",
    );
  });

  it("Given a resolved but not-approved plan, When rendered, Then the card uses the default tone border", () => {
    const block = {
      type: "tool_use",
      toolName: "ExitPlanMode",
      canonical: {
        kind: "plan.approve_request",
        planApprove: {
          requestId: "req-1",
          planText: "x",
          resolved: true,
          allowed: false,
        },
      },
    } as unknown as TranscriptBlock;
    renderCard(<PlanApproveCard toolBlock={block} sessionId={1} />);
    expect(screen.getByTestId("plan-card")).toHaveClass("border-border");
  });

  it("requestless plan.refine action sends feedback through resolvePlanAction", async () => {
    renderCard(<PlanApproveCard toolBlock={actionPlanBlock()} sessionId={1} />);
    fireEvent.click(screen.getByText("Refine Plan"));
    fireEvent.change(screen.getByPlaceholderText(/step 2/), {
      target: { value: "把测试写具体一点" },
    });
    fireEvent.click(screen.getByText("Send Feedback and Continue Planning"));
    await waitFor(() => {
      expect(resolvePlanAction).toHaveBeenCalledWith(
        expect.objectContaining({
          sessionId: 1,
          requestId: "",
          actionId: "plan.refine",
          feedback: "把测试写具体一点",
        }),
      );
    });
  });
});
