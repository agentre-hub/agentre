/**
 * 拿 wire 帧的那些面（控制台的 relay、桌面端 Peer Tab、远端补齐）上的
 * `tool_approval` 审批卡。
 *
 * Go 侧的历史投影把落库的 `tool_approval` 块折成 `tool_approval_requested`（外加终态
 * 那一帧 `tool_approval_resolved`）。它此前落 `unrecognized_block`，在这些面上只是一条
 * 印着 JSON 的提示条 —— 控制台看不到审批卡，也答不了。这里从**帧**一路走到渲染：
 * 归约 → 行 → 行视图，证明它落到与桌面端同一张卡上（ctl → 变更清单卡，其余 → 原卡）。
 */
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import {
  EventToolApprovalRequested,
  EventToolApprovalResolved,
} from "../event-kinds.gen";
import { reduceFrames, type TranscriptFrame } from "./frames";
import type { TranscriptPorts } from "./ports";
import { TranscriptPortsProvider } from "./ports-context";
import {
  TranscriptRenderContext,
  TranscriptRowView,
} from "./transcript-row-view";
import { buildTranscriptRows } from "./transcript-rows";

const SID = 5;

const ports: TranscriptPorts = {
  answerToolPermission: vi.fn(async () => {}),
  answerUserQuestion: vi.fn(async () => {}),
  answerToolApproval: vi.fn(async () => {}),
  resolveExecApproval: vi.fn(async () => ({ status: "resolved" })),
  resolvePlanAction: vi.fn(async () => ({})),
};

function frame(seq: number, event: Record<string, unknown>): TranscriptFrame {
  return { seq, event };
}

const ctlRequested = {
  kind: EventToolApprovalRequested,
  toolKey: "ctl",
  requestId: "ctl-1",
  toolName: "ctl_update_provider",
  toolInput: {
    command: "agrctl update provider openrouter --base-url https://x/v1",
    changes: [
      {
        op: "update",
        kind: "provider",
        id: 4,
        name: "openrouter",
        fields: [
          { field: "baseURL", before: "https://x", after: "https://x/v1" },
        ],
      },
    ],
  },
};

const orgRequested = {
  kind: EventToolApprovalRequested,
  toolKey: "org",
  requestId: "org-1",
  toolName: "org_create_department",
  toolInput: { name: "研发部", parentId: 1 },
};

function renderFrames(frames: TranscriptFrame[]) {
  const { rows } = buildTranscriptRows({
    displayMessages: reduceFrames(frames, SID),
    autonomousIds: new Set(),
  });
  return render(
    <TranscriptPortsProvider ports={ports}>
      <TranscriptRenderContext.Provider
        value={{ agentName: "A", agentAvatar: <span />, sessionId: SID }}
      >
        {rows.map((row) => (
          <TranscriptRowView
            key={row.key}
            row={row}
            liveTail=""
            liveBlocks={undefined}
            liveRetry={null}
            showIndicator={false}
            compacting={false}
            reconnecting={false}
          />
        ))}
      </TranscriptRenderContext.Provider>
    </TranscriptPortsProvider>,
  );
}

describe("tool_approval frames", () => {
  it("Given a ctl tool_approval_requested frame, When reduced, Then it lands as a pending tool_approval block carrying the whole card", () => {
    const [msg] = reduceFrames([frame(1, ctlRequested)], SID);

    expect(msg.blocks).toHaveLength(1);
    expect(msg.blocks[0]).toMatchObject({
      type: "tool_approval",
      toolApproval: {
        toolKey: "ctl",
        requestId: "ctl-1",
        toolName: "ctl_update_provider",
        toolInput: ctlRequested.toolInput,
        status: "pending",
      },
    });
  });

  it("Given the resolution frame, When reduced, Then it backfills the same card instead of adding a block", () => {
    const [msg] = reduceFrames(
      [
        frame(1, ctlRequested),
        frame(2, {
          kind: EventToolApprovalResolved,
          requestId: "ctl-1",
          status: "approved",
          result: "updated provider #4",
        }),
      ],
      SID,
    );

    expect(msg.blocks).toHaveLength(1);
    expect(msg.blocks[0].toolApproval).toMatchObject({
      status: "approved",
      result: "updated provider #4",
    });
  });

  it("Given a resolution for an unknown request, When reduced, Then nothing is invented", () => {
    const messages = reduceFrames(
      [
        frame(1, {
          kind: EventToolApprovalResolved,
          requestId: "missing",
          status: "denied",
        }),
      ],
      SID,
    );

    expect(messages.flatMap((m) => m.blocks)).toHaveLength(0);
  });

  it("Given a malformed request frame, When reduced, Then it still renders a card with empty fields rather than crashing", () => {
    const [msg] = reduceFrames(
      [frame(1, { kind: EventToolApprovalRequested, toolInput: "nope" })],
      SID,
    );

    expect(msg.blocks[0]).toMatchObject({
      type: "tool_approval",
      toolApproval: {
        toolKey: "",
        requestId: "",
        toolName: "",
        status: "pending",
      },
    });
  });

  it("Given a pending ctl approval over the wire, When rendered, Then the change-list card shows with approve/reject", () => {
    renderFrames([frame(1, ctlRequested)]);

    expect(screen.getByTestId("ctl-approval-card")).toBeDefined();
    expect(
      screen.getByText(
        "agrctl update provider openrouter --base-url https://x/v1",
      ),
    ).toBeDefined();
    expect(screen.getByText("Approve")).toBeDefined();
    expect(screen.queryByTestId("tool-approval-card")).toBeNull();
  });

  it("Given an approved ctl approval over the wire, When rendered, Then the card is read-only with the result", () => {
    renderFrames([
      frame(1, ctlRequested),
      frame(2, {
        kind: EventToolApprovalResolved,
        requestId: "ctl-1",
        status: "approved",
        result: "updated provider #4",
      }),
    ]);

    expect(screen.getByTestId("ctl-approval-card")).toBeDefined();
    expect(screen.getByText("Approved")).toBeDefined();
    expect(screen.getByText("updated provider #4")).toBeDefined();
    expect(screen.queryByText("Approve")).toBeNull();
  });

  it("Given an org approval over the wire, When rendered, Then the original tool approval card shows", () => {
    renderFrames([
      frame(1, orgRequested),
      frame(2, {
        kind: EventToolApprovalResolved,
        requestId: "org-1",
        status: "expired",
      }),
    ]);

    expect(screen.getByTestId("tool-approval-card")).toBeDefined();
    expect(screen.getByText("Create department")).toBeDefined();
    expect(screen.getByText("Expired")).toBeDefined();
    expect(screen.queryByTestId("ctl-approval-card")).toBeNull();
  });
});
