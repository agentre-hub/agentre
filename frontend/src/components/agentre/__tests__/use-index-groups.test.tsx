import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../../../../wailsjs/go/app/App", () => ({
  ListChatAgents: vi.fn(),
  ListChatIndexSessions: vi.fn(),
}));

import { ListChatIndexSessions } from "../../../../wailsjs/go/app/App";
import type { app } from "../../../../wailsjs/go/models";
import {
  scopesForAxis,
  useIndexGroups,
} from "../session-index/use-index-groups";
import { useChatAgentsStore, type AgentSlim } from "@/stores/chat-agents-store";
import { useSessionIndexStore } from "@/stores/session-index-store";
import { useSessionMetaStore } from "@/stores/session-meta-store";

const listIndex = ListChatIndexSessions as ReturnType<typeof vi.fn>;

function node(id: number, children: app.ProjectTreeNode[] = []) {
  return {
    project: { id, name: `p${id}` },
    children,
  } as unknown as app.ProjectTreeNode;
}

function seedAgents(
  ...rows: {
    id: number;
    pinned?: boolean;
    sessionIds?: number[];
    sessions?: { id: number }[];
    attentionSessions?: { id: number }[];
    totalSessions?: number;
  }[]
) {
  useChatAgentsStore.setState({
    agents: rows.map(
      (r) =>
        ({
          name: `a${r.id}`,
          sessions: [],
          attentionSessions: [],
          sessionIds: [],
          totalSessions: 0,
          ...r,
        }) as unknown as AgentSlim,
    ),
    loading: false,
    error: null,
  });
}

function seedMeta(entries: [number, number][]) {
  useSessionMetaStore
    .getState()
    .bulkUpsert(entries.map(([id, at]) => [id, { lastMessageAt: at }]));
}

describe("scopesForAxis", () => {
  it("Given the time axis, When scopes are resolved, Then only the global recent list is needed", () => {
    expect(scopesForAxis("time", [1, 2]).map((s) => s.kind)).toEqual([
      "recent",
    ]);
  });

  it("Given the agent axis, When scopes are resolved, Then nothing is fetched because ListChatAgents already carries the sessions", () => {
    expect(scopesForAxis("agent", [1, 2])).toEqual([]);
  });

  it("Given the project axis, When scopes are resolved, Then every project plus the free bucket is needed", () => {
    expect(scopesForAxis("project", [1, 2]).map((s) => s.kind)).toEqual([
      "project",
      "project",
      "free",
    ]);
  });
});

describe("useIndexGroups", () => {
  beforeEach(() => {
    listIndex.mockReset();
    listIndex.mockResolvedValue({ sessions: [], total: 0, hasMore: false });
    useChatAgentsStore.getState().__reset();
    useSessionIndexStore.getState().__reset();
    useSessionMetaStore.getState().__reset();
  });

  it("Given the project axis, When groups are built, Then they follow the tree order with depth and 随手对话 sits last", async () => {
    const tree = [node(1, [node(2)]), node(3)];
    const { result } = renderHook(() => useIndexGroups("project", tree));

    await waitFor(() => expect(listIndex).toHaveBeenCalled());

    expect(result.current.map((g) => [g.kind, g.refID, g.depth])).toEqual([
      ["project", 1, 0],
      ["project", 2, 1],
      ["project", 3, 0],
      ["free", 0, 0],
    ]);
  });

  it("Given no free sessions at all, When the project axis renders, Then the 随手对话 group is still there (decision 6)", async () => {
    const { result } = renderHook(() => useIndexGroups("project", [node(1)]));

    await waitFor(() => expect(listIndex).toHaveBeenCalled());

    const free = result.current.find((g) => g.kind === "free")!;
    expect(free).toBeDefined();
    expect(free.sessionIDs).toEqual([]);
  });

  it("Given the project axis, When it mounts, Then each project and the free bucket are fetched exactly once", async () => {
    renderHook(() => useIndexGroups("project", [node(1), node(2)]));

    await waitFor(() => expect(listIndex).toHaveBeenCalledTimes(3));
    const scopes = listIndex.mock.calls.map((c) => [
      c[0].scope,
      c[0].projectId,
    ]);
    expect(scopes).toEqual(
      expect.arrayContaining([
        ["project", 1],
        ["project", 2],
        ["free", 0],
      ]),
    );
  });

  it("Given the agent axis, When it renders, Then no index RPC is sent at all", async () => {
    seedAgents({ id: 1, sessions: [{ id: 10 }] });
    renderHook(() => useIndexGroups("agent", []));

    await new Promise((r) => setTimeout(r, 0));
    expect(listIndex).not.toHaveBeenCalled();
  });

  it("Given pinned and ordinary agents, When the agent axis renders, Then pinned float to the top and each segment sorts by latest activity", () => {
    seedMeta([
      [10, 100],
      [20, 300],
      [30, 200],
    ]);
    seedAgents(
      { id: 1, sessionIds: [10], sessions: [{ id: 10 }] },
      { id: 2, pinned: true, sessionIds: [20], sessions: [{ id: 20 }] },
      { id: 3, sessionIds: [30], sessions: [{ id: 30 }] },
    );

    const { result } = renderHook(() => useIndexGroups("agent", []));

    expect(result.current.map((g) => g.refID)).toEqual([2, 3, 1]);
  });

  it("Given an agent that never had activity, When ordering, Then it sinks instead of leading the list", () => {
    seedMeta([[10, 100]]);
    seedAgents(
      { id: 1, sessionIds: [], sessions: [] },
      { id: 2, sessionIds: [10], sessions: [{ id: 10 }] },
    );

    const { result } = renderHook(() => useIndexGroups("agent", []));

    expect(result.current.map((g) => g.refID)).toEqual([2, 1]);
  });

  it("Given an agent with attention sessions outside its top five, When its group is built, Then both sets appear once, newest first", () => {
    seedMeta([
      [10, 100],
      [20, 300],
    ]);
    seedAgents({
      id: 1,
      sessions: [{ id: 10 }, { id: 20 }],
      attentionSessions: [{ id: 20 }],
      totalSessions: 9,
    });

    const { result } = renderHook(() => useIndexGroups("agent", []));

    expect(result.current[0].sessionIDs).toEqual([20, 10]);
    expect(result.current[0].total).toBe(9);
  });

  it("Given the time axis, When it renders, Then there is one flat group fed by the global recent page", async () => {
    listIndex.mockResolvedValueOnce({
      sessions: [
        { id: 9, agentId: 1, projectId: 0, lastMessageAt: 90 },
        { id: 8, agentId: 2, projectId: 3, lastMessageAt: 80 },
      ],
      total: 42,
      hasMore: true,
    });

    const { result } = renderHook(() => useIndexGroups("time", []));

    await waitFor(() => expect(result.current[0].sessionIDs).toEqual([9, 8]));
    expect(result.current).toHaveLength(1);
    expect(result.current[0].kind).toBe("flat");
    expect(result.current[0].total).toBe(42);
  });
});
