import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, it, expect, vi } from "vitest";

import type { TranscriptBlockToolApproval } from "../dto";
import type { TranscriptPorts } from "../ports";
import { TranscriptPortsProvider } from "../ports-context";

import { ToolApprovalCard } from "./card";

// toolKey="ctl" 卡片(方案 B「变更清单卡」,spec 决策 6)独立测试文件:覆盖
// ChangeRow 的字段前后值/密钥掩码/级联删除文案,以及各 status 的整卡渲染。
// 路由分派(toolKey!=="ctl" 仍走旧 JSON 卡、畸形 toolInput 兜底)的用例留在
// card.test.tsx,因为那正是 ToolApprovalCard 这个既有路由入口的职责。
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

function renderCtlCard(overrides: Partial<TranscriptBlockToolApproval> = {}) {
  const approval: TranscriptBlockToolApproval = {
    toolKey: "ctl",
    requestId: "ctl-1",
    toolName: "ctl_update_provider",
    status: "pending",
    toolInput: {
      command:
        "agrctl update provider openrouter --base-url https://openrouter.ai/api/v1 --api-key=…",
      changes: [
        {
          op: "update",
          kind: "provider",
          id: 4,
          name: "openrouter",
          fields: [
            {
              field: "baseURL",
              before: "https://openrouter.ai/api",
              after: "https://openrouter.ai/api/v1",
            },
            { field: "apiKey", secret: true },
          ],
        },
      ],
    },
    ...overrides,
  };

  return render(
    <TranscriptPortsProvider ports={makePorts()}>
      <ToolApprovalCard approval={approval} sessionId={7} />
    </TranscriptPortsProvider>,
  );
}

describe("CtlApprovalCard (toolKey=ctl change-list card)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    answerToolApproval.mockResolvedValue(undefined);
  });

  it("renders the fixed title, command line, op/kind/name and field before→after while pending", () => {
    renderCtlCard();

    expect(screen.getByText("Config change")).toBeDefined();
    expect(screen.getByText("Approval needed")).toBeDefined();
    expect(
      screen.getByText(
        "agrctl update provider openrouter --base-url https://openrouter.ai/api/v1 --api-key=…",
      ),
    ).toBeDefined();
    expect(screen.getByText("Update")).toBeDefined();
    expect(screen.getByText("LLM provider")).toBeDefined();
    expect(screen.getByText("openrouter")).toBeDefined();
    expect(screen.getByText(/baseURL/)).toBeDefined();
    expect(screen.getByText("https://openrouter.ai/api")).toBeDefined();
    expect(screen.getByText("https://openrouter.ai/api/v1")).toBeDefined();
  });

  it("never shows a secret field's value, only the redaction copy", () => {
    renderCtlCard();

    expect(screen.getByText("Updated (value hidden)")).toBeDefined();
    // 密钥字段名仍然可见(用户要知道改了哪个字段),但没有任何值字符串泄出。
    expect(screen.getByText(/apiKey/)).toBeDefined();
  });

  it("shows a create row without a before value and a localized kind label", () => {
    renderCtlCard({
      toolInput: {
        command:
          "agrctl create agent --name reviewer --department 研发部 --backend claude-local",
        changes: [
          {
            op: "create",
            kind: "agent",
            name: "reviewer",
            fields: [{ field: "department", after: "研发部" }],
          },
        ],
      },
    });

    expect(screen.getByText("Create")).toBeDefined();
    expect(screen.getByText("Agent")).toBeDefined();
    expect(screen.getByText("reviewer")).toBeDefined();
    expect(screen.queryByText("line-through")).toBeNull();
  });

  it("shows the cascade counts for a delete with --cascade", () => {
    renderCtlCard({
      toolInput: {
        command: "agrctl delete department 临时小组 --cascade",
        changes: [
          {
            op: "delete",
            kind: "department",
            name: "临时小组",
            cascade: { departments: 1, agents: 2 },
          },
        ],
      },
    });

    expect(screen.getByText("Delete")).toBeDefined();
    expect(screen.getByText("Department")).toBeDefined();
    expect(
      screen.getByText(/also deletes 1 sub-department\(s\) and 2 agent\(s\)/),
    ).toBeDefined();
  });

  it("submits allow:true via the shared answerToolApproval port on approve", async () => {
    const user = userEvent.setup();
    renderCtlCard();

    await user.click(screen.getByText("Approve"));

    await waitFor(() => {
      expect(answerToolApproval).toHaveBeenCalledWith({
        sessionId: 7,
        requestId: "ctl-1",
        allow: true,
      });
    });
  });

  it("shows an inline error and stays retryable when the answer port rejects, then succeeds on retry", async () => {
    answerToolApproval.mockRejectedValueOnce(new Error("relay offline"));
    const user = userEvent.setup();
    renderCtlCard();

    await user.click(screen.getByText("Approve"));

    expect(await screen.findByText("Approval submission failed")).toBeDefined();
    const approveButton = screen.getByRole("button", { name: "Approve" });
    expect(approveButton).toBeEnabled();

    await user.click(approveButton);
    await waitFor(() => {
      expect(answerToolApproval).toHaveBeenCalledTimes(2);
    });
  });

  it("renders a read-only status pill and result text once approved, with no buttons", () => {
    renderCtlCard({ status: "approved", result: "已更新 provider openrouter" });

    expect(screen.getByText("Approved")).toBeDefined();
    expect(screen.getByText("已更新 provider openrouter")).toBeDefined();
    expect(screen.queryByText("Approve")).toBeNull();
    expect(screen.queryByText("Reject")).toBeNull();
    // 已批准之后卡片仍展示变更清单,方便回看批的是什么。
    expect(screen.getByText("openrouter")).toBeDefined();
  });

  it("renders a read-only status pill once denied", () => {
    renderCtlCard({ status: "denied", result: "用户拒绝了删除操作" });

    expect(screen.getByText("Rejected")).toBeDefined();
    expect(screen.getByText("用户拒绝了删除操作")).toBeDefined();
    expect(screen.queryByText("Approve")).toBeNull();
  });

  it("renders a read-only status pill once expired", () => {
    renderCtlCard({ status: "expired" });

    expect(screen.getByText("Expired")).toBeDefined();
    expect(screen.queryByText("Approve")).toBeNull();
  });
});
