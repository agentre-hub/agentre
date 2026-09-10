import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import type { TFunction } from "i18next";

import { useGroupRows } from "../session-index/use-group-rows";
import type { AgentStatus } from "@/stores/types";
import { useSessionMetaStore } from "@/stores/session-meta-store";
import { useSessionReadStore } from "@/stores/session-read-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

const t = ((key: string) => key) as unknown as TFunction;

function seed(
  sid: number,
  meta: { lastMessageAt?: number; lastReadAt?: number; title?: string },
  status?: {
    agentStatus?: AgentStatus;
    needsAttention?: boolean;
    bgRunning?: boolean;
  },
) {
  useSessionMetaStore
    .getState()
    .bulkUpsert([
      [sid, { title: `s-${sid}`, lastMessageAt: 0, lastReadAt: 0, ...meta }],
    ]);
  if (status) {
    useSessionStatusStore.getState().upsert(sid, {
      agentStatus: "idle",
      needsAttention: false,
      ...status,
    });
  }
}

describe("useGroupRows", () => {
  beforeEach(() => {
    useSessionMetaStore.getState().__reset();
    useSessionStatusStore.getState().__reset();
    useSessionReadStore.setState({ overrides: new Map() });
  });

  it("Given ids in the group, When rows are built, Then the regular list keeps the given order", () => {
    seed(1, { lastMessageAt: 300 });
    seed(2, { lastMessageAt: 200 });

    const { result } = renderHook(() => useGroupRows([1, 2], 0, t));

    expect(result.current.sessions.map((s) => s.id)).toEqual(["1", "2"]);
  });

  it("Given a session with no meta yet, When rows are built, Then it is skipped rather than rendered as a blank row", () => {
    seed(1, { lastMessageAt: 300 });

    const { result } = renderHook(() => useGroupRows([1, 99], 0, t));

    expect(result.current.sessions.map((s) => s.id)).toEqual(["1"]);
  });

  it("Given sessions needing attention, When rows are built, Then the bubble carries them ranked by latest activity, not by reason", () => {
    // 五个 reason 平权。按 reason 排会让「刚动过的那条」被一条更早的审批压下去。
    seed(1, { lastMessageAt: 100 }, { needsAttention: true });
    seed(2, { lastMessageAt: 300 }, { agentStatus: "running" });

    const { result } = renderHook(() => useGroupRows([1, 2], 0, t));

    expect(result.current.attentionSessions.map((s) => s.id)).toEqual([
      "2",
      "1",
    ]);
    expect(result.current.attentionSessions[0].attentionRank).toBe("running");
  });

  it("Given an unread session, When rows are built, Then attention is computed from the shared store rather than inline (problem 3)", () => {
    seed(1, { lastMessageAt: 500, lastReadAt: 100 }, { agentStatus: "idle" });

    const { result } = renderHook(() => useGroupRows([1], 0, t));

    expect(result.current.attentionSessions[0]?.attentionRank).toBe("unread");
  });

  it("Given a background subagent on an idle session, When rows are built, Then it surfaces — the project axis used to miss this entirely", () => {
    seed(1, { lastMessageAt: 100, lastReadAt: 100 }, { bgRunning: true });

    const { result } = renderHook(() => useGroupRows([1], 0, t));

    expect(result.current.attentionSessions[0]?.attentionRank).toBe(
      "bg_running",
    );
  });

  it("Given the open session needs no attention, When rows are built, Then it is pinned to the end of the bubble as the selected anchor", () => {
    seed(1, { lastMessageAt: 300 }, { agentStatus: "running" });
    seed(2, { lastMessageAt: 100, lastReadAt: 100 });

    const { result } = renderHook(() => useGroupRows([1, 2], 2, t));

    const bubble = result.current.attentionSessions;
    expect(bubble.map((s) => s.id)).toEqual(["1", "2"]);
    expect(bubble[1].attentionRank).toBe("selected");
  });

  it("Given the open session already needs attention, When rows are built, Then it is not duplicated as an anchor", () => {
    seed(1, { lastMessageAt: 300 }, { needsAttention: true });

    const { result } = renderHook(() => useGroupRows([1], 1, t));

    expect(result.current.attentionSessions).toHaveLength(1);
    expect(result.current.attentionSessions[0].attentionRank).toBe(
      "needs_attention",
    );
  });

  it("Given the open session belongs to another group, When rows are built, Then this group does not pin it", () => {
    seed(1, { lastMessageAt: 300, lastReadAt: 300 });

    const { result } = renderHook(() => useGroupRows([1], 42, t));

    expect(result.current.attentionSessions).toEqual([]);
  });

  it("Given an empty group, When rows are built, Then both lists are empty and stable", () => {
    const { result, rerender } = renderHook(() => useGroupRows([], 0, t));
    const first = result.current;
    rerender();

    expect(first.sessions).toEqual([]);
    expect(result.current).toBe(first);
  });

  it("Given a read error only in the attention pool, When the regular list and the pool are passed separately, Then it stays out of both (sess-1819 regression)", () => {
    seed(11, { lastMessageAt: 3000, lastReadAt: 3000 });
    seed(12, { lastMessageAt: 2000, lastReadAt: 2000 });
    seed(
      1819,
      { lastMessageAt: 1000, lastReadAt: 1000 },
      { agentStatus: "error" },
    );

    const { result } = renderHook(() =>
      useGroupRows(
        [11, 12], // 常规列表 = 前 5 条
        0,
        t,
        { attentionPoolIDs: [11, 12, 1819] }, // attention 池 = 前 5 + error
      ),
    );

    // 常规列表只有前 5 条，已读 error 不漏进去。
    expect(result.current.sessions.map((s) => s.id)).toEqual(["11", "12"]);
    // 气泡也不进（error + 已读 → null）。
    expect(result.current.attentionSessions).toEqual([]);
  });

  it("Given an unread error in the attention pool, When the pool feeds the bubble, Then it still surfaces there (pool still powers attention)", () => {
    seed(11, { lastMessageAt: 3000, lastReadAt: 3000 });
    seed(
      1819,
      { lastMessageAt: 1000, lastReadAt: 500 },
      { agentStatus: "error" },
    );

    const { result } = renderHook(() =>
      useGroupRows([11], 0, t, { attentionPoolIDs: [11, 1819] }),
    );

    // 常规列表不含池里的未读 error（它不属于前 5 条常规列表）。
    expect(result.current.sessions.map((s) => s.id)).toEqual(["11"]);
    // 但气泡里它在 —— 未读 error 依然需要用户去看。
    expect(result.current.attentionSessions.map((s) => s.id)).toEqual(["1819"]);
    expect(result.current.attentionSessions[0].attentionRank).toBe("error");
  });
});
