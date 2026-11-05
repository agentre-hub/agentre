import { describe, expect, it } from "vitest";

import { deriveAppStatusBarState, railApprovalCount } from "../app-status-bar";

describe("deriveAppStatusBarState", () => {
  it("counts actual agents and running sessions", () => {
    const state = deriveAppStatusBarState(
      [
        {
          sessions: [
            { id: 1, status: "running" },
            { id: 2, status: "idle" },
          ],
        },
        { sessions: [{ id: 3, status: "running" }] },
      ],
      new Map(),
      new Map(),
      new Map(),
    );

    expect(state).toMatchObject({
      agentCount: 2,
      runningCount: 2,
      approvalIds: [],
      unreadIds: [],
      indicatorStatus: "running",
    });
  });

  it("uses live status overlays before stale list snapshots", () => {
    const state = deriveAppStatusBarState(
      [{ sessions: [{ id: 1, status: "idle" }] }],
      new Map([[1, { agentStatus: "running", needsAttention: false }]]),
      new Map(),
      new Map(),
    );

    expect(state.agentCount).toBe(1);
    expect(state.runningCount).toBe(1);
    expect(state.approvalIds).toEqual([]);
    expect(state.unreadIds).toEqual([]);
    expect(state.indicatorStatus).toBe("running");
  });

  it("collects approval and unread session ids for attention", () => {
    const state = deriveAppStatusBarState(
      [
        {
          sessions: [
            { id: 1, status: "waiting", needsAttention: true },
            { id: 2, status: "idle", lastMessageAt: 200, lastReadAt: 100 },
            { id: 3, status: "running", lastMessageAt: 300, lastReadAt: 0 },
          ],
        },
      ],
      new Map(),
      new Map(),
      new Map(),
    );

    expect(state.agentCount).toBe(1);
    expect(state.runningCount).toBe(1);
    expect(state.approvalIds).toEqual([1]);
    expect(state.unreadIds).toEqual([2]);
    expect(state.indicatorStatus).toBe("waiting");
  });

  it("uses read overrides when deciding whether a session is unread", () => {
    const state = deriveAppStatusBarState(
      [{ sessions: [{ id: 1, status: "idle", lastMessageAt: 200 }] }],
      new Map(),
      new Map(),
      new Map([[1, 200]]),
    );

    expect(state.approvalIds).toEqual([]);
    expect(state.unreadIds).toEqual([]);
    expect(state.indicatorStatus).toBe("idle");
  });

  it("keeps the indicator running/idle when sessions are only unread", () => {
    const unreadOnly = deriveAppStatusBarState(
      [{ sessions: [{ id: 1, status: "idle", lastMessageAt: 200 }] }],
      new Map(),
      new Map(),
      new Map(),
    );
    expect(unreadOnly.unreadIds).toEqual([1]);
    expect(unreadOnly.indicatorStatus).toBe("idle");

    const unreadWhileRunning = deriveAppStatusBarState(
      [
        {
          sessions: [
            { id: 1, status: "idle", lastMessageAt: 200 },
            { id: 2, status: "running" },
          ],
        },
      ],
      new Map(),
      new Map(),
      new Map(),
    );
    expect(unreadWhileRunning.indicatorStatus).toBe("running");
  });

  it("resolves attention items with the session title and its agent identity", () => {
    const state = deriveAppStatusBarState(
      [
        {
          name: "Reviewer",
          avatarColor: "agent-3",
          avatarIcon: "bot",
          avatarDataUrl: "",
          sessions: [
            {
              id: 1,
              title: "Fix login",
              status: "waiting",
              needsAttention: true,
            },
            { id: 2, title: "", status: "idle", lastMessageAt: 5 },
          ],
        },
      ],
      new Map(),
      new Map(),
      new Map(),
    );

    expect(state.approvals).toEqual([
      expect.objectContaining({
        id: 1,
        title: "Fix login",
        agentName: "Reviewer",
        avatarColor: "agent-3",
        avatarIcon: "bot",
      }),
    ]);
    expect(state.unread).toEqual([
      expect.objectContaining({ id: 2, title: "", agentName: "Reviewer" }),
    ]);
  });
});

describe("railApprovalCount", () => {
  it("shows pending approvals on the rail only away from the chat page", () => {
    expect(railApprovalCount(3, false)).toBe(3);
    expect(railApprovalCount(3, true)).toBe(0);
    expect(railApprovalCount(0, false)).toBe(0);
  });
});
