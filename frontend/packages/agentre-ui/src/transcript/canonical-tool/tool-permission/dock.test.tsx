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

describe("ToolPermissionDock connecting / feedback (opt-in via connecting prop)", () => {
  beforeEach(() => vi.clearAllMocks());

  const names = ["Allow", "Always Allow This Session", "Reject"];

  it("Given connecting, Then all three buttons are disabled and a spinner line says approval waits for the connection", () => {
    renderDock(
      <ToolPermissionDock toolBlock={bash()} sessionId={7} connecting />,
    );
    for (const name of names) {
      expect(
        (screen.getByRole("button", { name }) as HTMLButtonElement).disabled,
      ).toBe(true);
    }
    const note = screen.getByTestId("tool-permission-dock-connecting");
    expect(note.textContent).toContain(
      "Connecting. You can approve once it's connected",
    );
    expect(note.querySelector('[role="status"]')).not.toBeNull();
  });

  it("Given connecting={false} or no prop, Then buttons are enabled and there is no connecting line", () => {
    renderDock(
      <ToolPermissionDock
        toolBlock={bash()}
        sessionId={7}
        connecting={false}
      />,
    );
    expect(screen.queryByTestId("tool-permission-dock-connecting")).toBeNull();
    for (const name of names) {
      expect(
        (screen.getByRole("button", { name }) as HTMLButtonElement).disabled,
      ).toBe(false);
    }
  });

  it("Given no new props, Then a pending submit shows no spinner and a failure has no Retry button", async () => {
    answerToolPermission.mockRejectedValueOnce(new Error("boom"));
    renderDock(<ToolPermissionDock toolBlock={bash()} sessionId={7} />);
    fireEvent.click(screen.getByRole("button", { name: "Allow" }));
    await screen.findByRole("alert");
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
    expect(document.querySelector('[role="status"]')).toBeNull();
  });

  it("When one button is tapped and pending, Then only that button shows a spinner", async () => {
    answerToolPermission.mockImplementationOnce(() => new Promise(() => {}));
    renderDock(
      <ToolPermissionDock
        toolBlock={bash()}
        sessionId={7}
        connecting={false}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /Reject/ }));
    const reject = screen.getByRole("button", { name: /Reject/ });
    expect(reject.querySelector('[role="status"]')).not.toBeNull();
    expect(
      screen
        .getByRole("button", { name: /^Allow$/ })
        .querySelector('[role="status"]'),
    ).toBeNull();
    expect(
      screen
        .getByRole("button", { name: "Always Allow This Session" })
        .querySelector('[role="status"]'),
    ).toBeNull();
  });

  it("Given a failure, Then the reason is shown with a Retry that repeats the same decision", async () => {
    answerToolPermission.mockRejectedValueOnce(new Error("no waiting"));
    renderDock(
      <ToolPermissionDock
        toolBlock={bash()}
        sessionId={7}
        messageId={42}
        connecting={false}
      />,
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Always Allow This Session" }),
    );
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Approval submission failed");
    // 原因要看得见（触屏没有 title 悬停）。
    expect(alert.textContent).toContain("no waiting");
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(answerToolPermission).toHaveBeenCalledTimes(2));
    expect(answerToolPermission).toHaveBeenLastCalledWith({
      sessionId: 7,
      requestId: "req-1",
      allow: true,
      alwaysAllowSession: true,
    });
  });

  it("Given the port times out, Then the copy says no confirmation arrived and it may already have taken effect", async () => {
    const err = new Error("request timed out after 30000ms");
    err.name = "ApiTimeoutError";
    answerToolPermission.mockRejectedValueOnce(err);
    renderDock(
      <ToolPermissionDock
        toolBlock={bash()}
        sessionId={7}
        connecting={false}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Allow" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe(
      "No confirmation received. It may already have taken effect",
    );
  });

  it("Given a failure, When the connection drops, Then the stale failure and Retry give way to the connecting line", async () => {
    answerToolPermission.mockRejectedValueOnce(new Error("no waiting"));
    const { rerender } = renderDock(
      <ToolPermissionDock
        toolBlock={bash()}
        sessionId={7}
        connecting={false}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Allow" }));
    await screen.findByRole("alert");
    rerender(
      <TranscriptPortsProvider ports={ports}>
        <TranscriptLiveStateProvider value={liveState}>
          <ToolPermissionDock toolBlock={bash()} sessionId={7} connecting />
        </TranscriptLiveStateProvider>
      </TranscriptPortsProvider>,
    );
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
    expect(screen.getByTestId("tool-permission-dock-connecting")).toBeDefined();
  });

  it("Given no new props, When a submit fails, Then the reason stays only in the tooltip (desktop unchanged)", async () => {
    answerToolPermission.mockRejectedValueOnce(new Error("no waiting"));
    renderDock(<ToolPermissionDock toolBlock={bash()} sessionId={7} />);
    fireEvent.click(screen.getByRole("button", { name: "Allow" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe("Approval submission failed");
    expect(alert).toHaveAttribute("title", "no waiting");
  });
});
