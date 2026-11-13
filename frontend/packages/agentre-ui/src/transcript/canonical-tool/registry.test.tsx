import { render, screen } from "@testing-library/react";
import type * as React from "react";
import { describe, it, expect, vi } from "vitest";

import type { TranscriptBlock } from "../dto";
import type { TranscriptPorts } from "../ports";
import { TranscriptPortsProvider } from "../ports-context";
import {
  TranscriptRenderContext,
  TranscriptRowView,
  type TranscriptRenderContextValue,
} from "../transcript-row-view";
import type { TranscriptRow } from "../transcript-rows";

import { CanonicalToolRouter } from "./registry";
import { ShortcutHintsProvider } from "./tool-permission/shortcut-hints";

// 路由出来的交互卡片从 TranscriptPortsProvider 取动作端口,少了 provider 会在
// 挂载期就抛 —— 这里给整棵路由树注入一份 mock 端口。
const ports: TranscriptPorts = {
  answerToolPermission: vi.fn().mockResolvedValue(undefined),
  answerUserQuestion: vi.fn().mockResolvedValue(undefined),
  answerToolApproval: vi.fn().mockResolvedValue(undefined),
  resolveExecApproval: vi.fn().mockResolvedValue({ status: "resolved" }),
  resolvePlanAction: vi.fn().mockResolvedValue(undefined),
};

function renderRouted(ui: React.ReactElement) {
  return render(
    <TranscriptPortsProvider ports={ports}>{ui}</TranscriptPortsProvider>,
  );
}

describe("CanonicalToolRouter", () => {
  it("falls back to RawToolCard when canonical is missing", () => {
    const block = {
      type: "tool_use",
      toolName: "Bash",
      toolInput: { command: "ls" },
    } as unknown as TranscriptBlock;
    renderRouted(<CanonicalToolRouter toolBlock={block} />);
    expect(screen.getByTestId("raw-tool-card")).toBeDefined();
  });

  // file.write / file.edit 刻意**不注册**：它们一律折进活动块，正文由活动行的
  // 展开体直接调 content-renderer / hunk-renderer 渲染，卡壳已删。这里守的是
  // 「别有人顺手把卡壳加回注册表」——真走到路由也只该是通用回落。
  it.each(["file.write", "file.edit"])(
    "leaves %s unregistered so it can only fold into an activity block",
    (kind) => {
      const block = {
        type: "tool_use",
        toolName: "Write",
        toolInput: { file_path: "/a.ts", content: "x" },
        canonical: { kind },
      } as unknown as TranscriptBlock;
      renderRouted(<CanonicalToolRouter toolBlock={block} />);
      expect(screen.getByTestId("raw-tool-card")).toBeDefined();
      expect(screen.queryByTestId("file-write-card")).toBeNull();
      expect(screen.queryByTestId("file-edit-card")).toBeNull();
    },
  );

  it("routes user.ask → UserAskCard", () => {
    const block = {
      type: "tool_use",
      canonical: {
        kind: "user.ask",
        userAsk: {
          requestId: "r1",
          questions: [
            {
              question: "?",
              header: "h",
              options: [{ label: "A", description: "" }],
            },
          ],
        },
      },
    } as unknown as TranscriptBlock;
    renderRouted(<CanonicalToolRouter toolBlock={block} sessionId={1} />);
    expect(screen.getByTestId("user-ask-card")).toBeDefined();
  });

  it("falls back plan.update to RawToolCard", () => {
    const block = {
      type: "tool_use",
      toolName: "update_plan",
      toolInput: { plan: "- [ ] s" },
      canonical: {
        kind: "plan.update",
        planUpdate: {
          steps: [{ step: "s", status: "pending" }],
        },
      },
    } as unknown as TranscriptBlock;
    renderRouted(<CanonicalToolRouter toolBlock={block} />);
    expect(screen.getByTestId("raw-tool-card")).toBeDefined();
    expect(screen.getByText("update_plan")).toBeDefined();
    expect(screen.queryByText("plan.update")).toBeNull();
    expect(screen.queryByTestId("plan-card")).toBeNull();
  });

  it("routes plan.approve_request → PlanApproveCard", () => {
    const block = {
      type: "tool_use",
      canonical: {
        kind: "plan.approve_request",
        planApprove: { requestId: "r", planText: "# p" },
      },
    } as unknown as TranscriptBlock;
    renderRouted(<CanonicalToolRouter toolBlock={block} sessionId={1} />);
    expect(screen.getByTestId("plan-card")).toBeDefined();
  });

  it("routes agent.spawn → AgentSpawnCard", () => {
    const block = {
      type: "tool_use",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: { taskId: "1" },
      },
    } as unknown as TranscriptBlock;
    renderRouted(<CanonicalToolRouter toolBlock={block} />);
    expect(screen.getByTestId("agent-spawn-card")).toBeDefined();
  });

  it("falls back to RawToolCard for unknown kind", () => {
    const block = {
      type: "tool_use",
      toolName: "Custom",
      toolInput: { foo: "bar" },
      canonical: { kind: "totally.unknown" },
    } as unknown as TranscriptBlock;
    renderRouted(<CanonicalToolRouter toolBlock={block} />);
    expect(screen.getByTestId("raw-tool-card")).toBeDefined();
  });

  it("routes a shell call carrying an agrctl overlay to the subagent card", () => {
    const block = {
      type: "tool_use",
      toolName: "Bash",
      toolUseId: "sh",
      toolInput: { command: "agrctl send --wait" },
      canonical: { kind: "command.run", commandRun: {} },
      subagent: {
        origin: "agrctl",
        kind: "agrctl_send",
        status: "running",
        runs: [{ id: "r0", index: 0, agent: "reviewer", task: "review" }],
      },
    } as unknown as TranscriptBlock;
    renderRouted(<CanonicalToolRouter toolBlock={block} />);
    expect(screen.getByTestId("agent-spawn-card")).toBeDefined();
    expect(screen.getByText("reviewer")).toBeDefined();
  });
});

function toolPermissionBlock(resolved: boolean): TranscriptBlock {
  return {
    type: "tool_permission_request",
    toolName: "Bash",
    canonical: {
      kind: "tool.permission",
      toolPermission: {
        requestId: "perm-1",
        toolName: "Bash",
        toolInput: { command: "pnpm add left-pad" },
        resolved,
        allowed: resolved,
      },
    },
  } as unknown as TranscriptBlock;
}

describe("CanonicalToolRouter outside the transcript (decision panel)", () => {
  // 控制台的决策面板直接用路由器画卡，不在 TranscriptRenderContext 里 ——
  // 去键帽的开关必须不经渲染上下文也给得到。
  it("Given ShortcutHintsProvider hidden, When a pending permission is routed, Then no keycaps are drawn", () => {
    renderRouted(
      <ShortcutHintsProvider hidden>
        <CanonicalToolRouter
          toolBlock={toolPermissionBlock(false)}
          sessionId={1}
        />
      </ShortcutHintsProvider>,
    );
    expect(screen.getByRole("button", { name: "Allow" })).toBeDefined();
    expect(screen.queryByText("↵")).toBeNull();
    expect(screen.queryByText("Esc")).toBeNull();
  });

  it("Given no provider, When a pending permission is routed, Then keycaps are drawn as today", () => {
    renderRouted(
      <CanonicalToolRouter
        toolBlock={toolPermissionBlock(false)}
        sessionId={1}
      />,
    );
    expect(screen.getByText("↵").tagName).toBe("KBD");
    expect(screen.getByText("Esc").tagName).toBe("KBD");
  });
});

function rowFor(
  item: TranscriptRow["item"],
  edges: { first: boolean; last: boolean } = { first: false, last: false },
): TranscriptRow {
  return {
    key: "message:7:item",
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
    item,
    isFirstOfMessage: edges.first,
    isLastOfMessage: edges.last,
    autonomous: false,
  } as unknown as TranscriptRow;
}

function renderRow(
  row: TranscriptRow,
  extra: Partial<TranscriptRenderContextValue> = {},
  showIndicator = false,
) {
  return render(
    <TranscriptPortsProvider ports={ports}>
      <TranscriptRenderContext.Provider
        value={{
          agentName: "Agent",
          agentAvatar: <span />,
          sessionId: 1,
          ...extra,
        }}
      >
        <TranscriptRowView
          row={row}
          liveTail=""
          liveBlocks={undefined}
          liveRetry={null}
          showIndicator={showIndicator}
          compacting={false}
          reconnecting={false}
        />
      </TranscriptRenderContext.Provider>
    </TranscriptPortsProvider>,
  );
}

const permissionItem = (resolved: boolean) =>
  ({
    type: "tool_permission_request",
    uiStateKey: "message:7:permission:perm-1",
    block: toolPermissionBlock(resolved),
  }) as unknown as TranscriptRow["item"];

describe("TranscriptRowView decision switches", () => {
  it("Given hideShortcutHints in the render context, When a pending permission row renders, Then no keycaps are drawn", () => {
    renderRow(rowFor(permissionItem(false)), { hideShortcutHints: true });
    expect(screen.getByRole("button", { name: "Allow" })).toBeDefined();
    expect(screen.queryByText("↵")).toBeNull();
    expect(screen.queryByText("Esc")).toBeNull();
  });

  it("Given no decision switches, When a pending permission row renders, Then the inline card with keycaps renders as today", () => {
    renderRow(rowFor(permissionItem(false)));
    expect(screen.getByTestId("tool-permission-card")).toBeDefined();
    expect(screen.getByText("↵").tagName).toBe("KBD");
  });

  it('Given decisionPlacement="docked", When a pending permission row renders, Then the transcript draws nothing for it', () => {
    const { container } = renderRow(rowFor(permissionItem(false)), {
      decisionPlacement: "docked",
    });
    expect(screen.queryByTestId("tool-permission-card")).toBeNull();
    // 中间分片行没有别的内容 —— 连幽灵 gutter 都不留。
    expect(container.innerHTML).toBe("");
  });

  it("Given decisionPlacement=\"docked\" and the pending permission is the message's last row, Then the row's tail attachments still render", () => {
    renderRow(
      rowFor(permissionItem(false), { first: false, last: true }),
      { decisionPlacement: "docked" },
      true,
    );
    expect(screen.queryByTestId("tool-permission-card")).toBeNull();
    expect(screen.getByRole("status")).toBeDefined();
  });

  it('Given decisionPlacement="docked", When a resolved permission row renders, Then its card still shows', () => {
    renderRow(rowFor(permissionItem(true)), { decisionPlacement: "docked" });
    expect(screen.getByTestId("tool-permission-card")).toBeDefined();
    expect(screen.getByRole("img", { name: "Approved" })).toBeDefined();
  });

  it('Given decisionPlacement="docked", When a pending user.ask row renders, Then the question card stays in the transcript', () => {
    const item = {
      type: "tool",
      uiStateKey: "message:7:tool:ask-1",
      toolBlock: {
        type: "tool_use",
        canonical: {
          kind: "user.ask",
          userAsk: {
            requestId: "ask-1",
            questions: [
              {
                question: "?",
                header: "h",
                options: [{ label: "A", description: "" }],
              },
            ],
          },
        },
      },
    } as unknown as TranscriptRow["item"];
    renderRow(rowFor(item), { decisionPlacement: "docked" });
    expect(screen.getByTestId("user-ask-card")).toBeDefined();
  });

  it('Given decisionPlacement="docked", When a pending plan approval row renders, Then the plan card stays in the transcript', () => {
    const item = {
      type: "tool_permission_request",
      uiStateKey: "message:7:permission:plan-1",
      block: {
        type: "tool_permission_request",
        canonical: {
          kind: "plan.approve_request",
          planApprove: { requestId: "plan-1", planText: "# p" },
        },
      },
    } as unknown as TranscriptRow["item"];
    renderRow(rowFor(item), { decisionPlacement: "docked" });
    expect(screen.getByTestId("plan-card")).toBeDefined();
  });
});

describe("CanonicalToolRouter memory writes", () => {
  const writeCall = {
    type: "tool_use",
    toolName: "Bash",
    toolInput: { command: 'agrctl create memory --summary "x"' },
  } as unknown as TranscriptBlock;
  const writeResult = {
    type: "tool_result",
    text: "memory m7 created (global)\n",
  } as unknown as TranscriptBlock;

  it("renders the memory card when the host provides the memory port", async () => {
    const memory = {
      get: vi.fn().mockResolvedValue(null),
      undoWrite: vi.fn(),
      save: vi.fn(),
    };
    render(
      <TranscriptPortsProvider ports={{ ...ports, memory }}>
        <CanonicalToolRouter
          toolBlock={writeCall}
          resultBlock={writeResult}
          sessionId={70}
        />
      </TranscriptPortsProvider>,
    );
    expect(screen.getByTestId("memory-write-card")).toBeDefined();
    expect(screen.queryByTestId("raw-tool-card")).toBeNull();
  });

  it("falls back to the generic rendering when the host has no memory port", () => {
    renderRouted(
      <CanonicalToolRouter toolBlock={writeCall} resultBlock={writeResult} />,
    );
    expect(screen.getByTestId("raw-tool-card")).toBeDefined();
    expect(screen.queryByTestId("memory-write-card")).toBeNull();
  });

  it("falls back to the generic rendering while the command has no result yet", () => {
    const memory = {
      get: vi.fn(),
      undoWrite: vi.fn(),
      save: vi.fn(),
    };
    render(
      <TranscriptPortsProvider ports={{ ...ports, memory }}>
        <CanonicalToolRouter toolBlock={writeCall} />
      </TranscriptPortsProvider>,
    );
    expect(screen.getByTestId("raw-tool-card")).toBeDefined();
  });
});
