import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, it, expect, vi } from "vitest";

import type { TranscriptBlockToolApproval } from "../dto";
import type { TranscriptPorts } from "../ports";
import { TranscriptPortsProvider } from "../ports-context";

import { ToolApprovalCard } from "./card";

// 卡片不再认识 Wails 绑定,只认注入的动作端口;测试注入自己的端口实现,
// 断言「用户按下批准/拒绝」落到 answerToolApproval 上。
const answerToolApproval = vi.fn();

function makePorts(): TranscriptPorts {
  return {
    answerToolPermission: vi.fn(async () => {}),
    answerUserQuestion: vi.fn(async () => {}),
    answerToolApproval,
    resolveExecApproval: vi.fn(async () => ({ status: "resolved" })),
    resolvePlanAction: vi.fn(async () => ({})),
  };
}

function renderCard(approval: TranscriptBlockToolApproval) {
  return render(
    <TranscriptPortsProvider ports={makePorts()}>
      <ToolApprovalCard approval={approval} sessionId={42} />
    </TranscriptPortsProvider>,
  );
}

describe("ToolApprovalCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    answerToolApproval.mockResolvedValue(undefined);
  });

  const pending = (
    overrides: Partial<TranscriptBlockToolApproval> = {},
  ): TranscriptBlockToolApproval => ({
    toolKey: "org",
    requestId: "org-1",
    toolName: "org_create_department",
    toolInput: { name: "研发部", parentId: 1 },
    status: "pending",
    ...overrides,
  });

  it("renders the tool label, the input payload and approve/reject buttons when pending", () => {
    renderCard(pending());
    // tools.org_create_department → "Create department" (setup forces en locale)
    expect(screen.getByText("Create department")).toBeDefined();
    // 入参 JSON 原样渲染(动态内容不翻译)
    expect(screen.getByText(/研发部/)).toBeDefined();
    expect(screen.getByText("Approve")).toBeDefined();
    expect(screen.getByText("Reject")).toBeDefined();
  });

  it("scrolls a long tool input JSON without an expand control", () => {
    renderCard(
      pending({
        toolInput: { system_prompt: "x".repeat(300) },
      }),
    );
    expect(screen.queryByRole("button", { name: "Expand all" })).toBeNull();
  });

  it("calls the answerToolApproval port with allow:true when approve is clicked", async () => {
    const user = userEvent.setup();
    renderCard(pending());
    await user.click(screen.getByText("Approve"));
    await waitFor(() => {
      expect(answerToolApproval).toHaveBeenCalledTimes(1);
    });
    expect(answerToolApproval).toHaveBeenCalledWith({
      sessionId: 42,
      requestId: "org-1",
      allow: true,
    });
  });

  it("calls the answerToolApproval port with allow:false when reject is clicked", async () => {
    const user = userEvent.setup();
    renderCard(pending());
    await user.click(screen.getByText("Reject"));
    await waitFor(() => {
      expect(answerToolApproval).toHaveBeenCalledTimes(1);
    });
    expect(answerToolApproval).toHaveBeenCalledWith({
      sessionId: 42,
      requestId: "org-1",
      allow: false,
    });
  });

  it("shows an inline error and stays retryable when the port rejects", async () => {
    answerToolApproval.mockRejectedValueOnce(new Error("relay offline"));
    const user = userEvent.setup();
    renderCard(pending());

    await user.click(screen.getByText("Approve"));

    expect(await screen.findByText("Approval submission failed")).toBeDefined();
    expect(screen.getByRole("button", { name: "Approve" })).toBeEnabled();
  });

  it("renders a read-only status badge with no buttons once denied", () => {
    renderCard(
      pending({
        status: "denied",
        result: "用户拒绝了删除操作",
      }),
    );
    expect(screen.getByText("Rejected")).toBeDefined();
    expect(screen.getByText("用户拒绝了删除操作")).toBeDefined();
    expect(screen.queryByText("Approve")).toBeNull();
    expect(screen.queryByText("Reject")).toBeNull();
  });

  it("renders an approved badge with the result text", () => {
    renderCard(
      pending({
        status: "approved",
        result: "已创建部门 研发部",
      }),
    );
    expect(screen.getByText("Approved")).toBeDefined();
    expect(screen.getByText("已创建部门 研发部")).toBeDefined();
    expect(screen.queryByText("Approve")).toBeNull();
  });

  it("renders an expired badge for status=expired", () => {
    renderCard(pending({ status: "expired" }));
    expect(screen.getByText("Expired")).toBeDefined();
    expect(screen.queryByText("Approve")).toBeNull();
  });
});
