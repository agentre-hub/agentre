import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type * as React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { TranscriptBlock } from "../../dto";
import {
  TranscriptLiveStateProvider,
  type TranscriptLiveState,
} from "../../live-state";
import type { TranscriptPorts } from "../../ports";
import { TranscriptPortsProvider } from "../../ports-context";

import { ToolPermissionDock } from "./dock";

const answerToolPermission = vi.fn().mockResolvedValue(undefined);
const ports: TranscriptPorts = {
  answerToolPermission,
  answerUserQuestion: vi.fn().mockResolvedValue(undefined),
  answerToolApproval: vi.fn().mockResolvedValue(undefined),
  resolveExecApproval: vi.fn().mockResolvedValue({ status: "resolved" }),
  resolvePlanAction: vi.fn().mockResolvedValue(undefined),
};

// 成员被当 hook 调用 —— 必须是模块级常量。
const markToolPermissionResolved = vi.fn();
const liveState: TranscriptLiveState = {
  useIsStreamActive: () => false,
  markToolPermissionResolved,
};

function renderDock(ui: React.ReactElement) {
  return render(
    <TranscriptPortsProvider ports={ports}>
      <TranscriptLiveStateProvider value={liveState}>
        {ui}
      </TranscriptLiveStateProvider>
    </TranscriptPortsProvider>,
  );
}

function permBlock(
  toolName: string,
  toolInput: Record<string, unknown>,
  resolved = false,
): TranscriptBlock {
  return {
    type: "tool_permission_request",
    toolName,
    canonical: {
      kind: "tool.permission",
      toolPermission: { requestId: "req-1", toolName, toolInput, resolved },
    },
  } as unknown as TranscriptBlock;
}

const bash = () => permBlock("Bash", { command: "pnpm add left-pad" });

describe("ToolPermissionDock layout", () => {
  beforeEach(() => vi.clearAllMocks());

  it("Given a pending Bash permission, Then it asks to run the command and shows tool, cwd and the command", () => {
    renderDock(
      <ToolPermissionDock
        toolBlock={bash()}
        sessionId={7}
        messageId={42}
        cwd="~/Code/app"
      />,
    );
    expect(screen.getByTestId("tool-permission-dock")).toBeDefined();
    expect(screen.getByText("Decision needed")).toBeDefined();
    expect(screen.getByText("Run this command?")).toBeDefined();
    expect(screen.getByText("Bash · ~/Code/app")).toBeDefined();
    expect(screen.getByText("pnpm add left-pad")).toBeDefined();
  });

  it("Given a non-Bash tool, Then the title names the tool", () => {
    renderDock(
      <ToolPermissionDock
        toolBlock={permBlock("WebFetch", { url: "https://x" })}
        sessionId={7}
      />,
    );
    expect(screen.getByText("Allow WebFetch?")).toBeDefined();
    expect(screen.getByText("WebFetch")).toBeDefined();
  });

  it("Allow spans the full width; session-allow and Reject sit side by side; every target is at least 44px", () => {
    renderDock(<ToolPermissionDock toolBlock={bash()} sessionId={7} />);
    const allow = screen.getByRole("button", { name: "Allow" });
    const session = screen.getByRole("button", {
      name: "Always Allow This Session",
    });
    const reject = screen.getByRole("button", { name: "Reject" });

    expect(allow.className).toContain("w-full");
    expect(session.parentElement).toBe(reject.parentElement);
    expect(session.parentElement?.className).toContain("grid-cols-2");
    expect(allow.compareDocumentPosition(session)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
    for (const b of [allow, session, reject]) {
      expect(b.className).toContain("min-h-11");
    }
    expect(reject.className).toContain("text-destructive-text");
  });

  it("never draws keycaps", () => {
    renderDock(<ToolPermissionDock toolBlock={bash()} sessionId={7} />);
    expect(document.querySelector("kbd")).toBeNull();
  });

  it("Given a stop slot, Then it renders below the decision buttons", () => {
    renderDock(
      <ToolPermissionDock
        toolBlock={bash()}
        sessionId={7}
        stop={<button type="button">Stop run</button>}
      />,
    );
    const stop = screen.getByRole("button", { name: "Stop run" });
    const reject = screen.getByRole("button", { name: "Reject" });
    expect(reject.compareDocumentPosition(stop)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
  });

  it("Given a resolved permission, Then the dock draws nothing", () => {
    const { container } = renderDock(
      <ToolPermissionDock
        toolBlock={permBlock("Bash", { command: "ls" }, true)}
        sessionId={7}
      />,
    );
    expect(container.innerHTML).toBe("");
  });
});

describe("ToolPermissionDock submits through the card's path", () => {
  beforeEach(() => vi.clearAllMocks());

  it.each([
    ["Allow", true, false],
    ["Always Allow This Session", true, true],
    ["Reject", false, false],
  ])(
    "When %s is tapped, Then live state is marked resolved and the answer port gets allow=%s alwaysAllowSession=%s",
    async (name, allow, alwaysAllowSession) => {
      renderDock(
        <ToolPermissionDock toolBlock={bash()} sessionId={7} messageId={42} />,
      );
      fireEvent.click(screen.getByRole("button", { name }));

      expect(markToolPermissionResolved).toHaveBeenCalledWith({
        sessionId: 7,
        assistantMessageId: 42,
        toolPermission: {
          requestId: "req-1",
          toolName: "Bash",
          toolInput: { command: "pnpm add left-pad" },
          resolved: true,
          allowed: allow,
          alwaysAllow: alwaysAllowSession,
        },
      });
      await waitFor(() => {
        expect(answerToolPermission).toHaveBeenCalledWith({
          sessionId: 7,
          requestId: "req-1",
          allow,
          alwaysAllowSession,
        });
      });
    },
  );

  it("Given the answer port rejects, Then the optimistic resolution is rolled back and the error shows in the dock", async () => {
    answerToolPermission.mockRejectedValueOnce(new Error("no waiting"));
    renderDock(
      <ToolPermissionDock toolBlock={bash()} sessionId={7} messageId={42} />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Allow" }));

    // 正文是本地化事实句，机器原话只进 title。
    const fact = await screen.findByRole("alert");
    expect(fact.textContent).toBe("Approval submission failed");
    expect(fact.getAttribute("title")).toBe("no waiting");
    expect(screen.queryByText("no waiting")).toBeNull();
    expect(markToolPermissionResolved).toHaveBeenLastCalledWith(
      expect.objectContaining({
        toolPermission: expect.objectContaining({ resolved: false }),
      }),
    );
  });

  it("Given a submit in flight, Then a second tap does not answer twice", async () => {
    let release: () => void = () => {};
    answerToolPermission.mockImplementationOnce(
      () => new Promise<void>((r) => (release = r)),
    );
    renderDock(<ToolPermissionDock toolBlock={bash()} sessionId={7} />);
    const allow = screen.getByRole("button", { name: "Allow" });
    fireEvent.click(allow);
    fireEvent.click(screen.getByRole("button", { name: "Reject" }));
    release();
    await waitFor(() => expect(answerToolPermission).toHaveBeenCalledTimes(1));
  });
});
