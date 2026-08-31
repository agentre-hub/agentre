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

  it("Given the machine axis, When scopes are resolved, Then every machine in the roster gets its own query", () => {
    // 每台机器一条分页查询，与项目轴同形 —— 「查看全部 N」的 N 只有取数方数得出来。
    const scopes = scopesForAxis("machine", [1, 2], [0, 7]);
    expect(scopes.map((s) => s.kind)).toEqual(["machine", "machine"]);
    expect(scopes).toEqual([
      { kind: "machine", deviceID: 0, keyword: "" },
      { kind: "machine", deviceID: 7, keyword: "" },
    ]);
  });

  it("Given the machine axis with no paired daemon, When scopes are resolved, Then the local machine alone is still queried", () => {
    expect(scopesForAxis("machine", [1, 2], [0])).toEqual([
      { kind: "machine", deviceID: 0, keyword: "" },
    ]);
  });

  it("Given a non-machine axis, When scopes are resolved, Then no machine query goes out", () => {
    expect(
      scopesForAxis("project", [1], [0, 7]).map((s) => s.kind),
    ).not.toContain("machine");
  });

  it("Given the project axis, When scopes are resolved, Then every project plus the free bucket is needed", () => {
    expect(scopesForAxis("project", [1, 2]).map((s) => s.kind)).toEqual([
      "project",
      "project",
      "free",
    ]);
  });

  // ── 搜索 ────────────────────────────────────────────────────────────────
  //
  // 关键词不换一条查询，它只是把每条轴本来就在发的那条查询收窄。所以每根轴的每个
  // scope 都得带上它 —— 漏掉哪一个，那一组在搜索时给出的就是未过滤的列表，混在
  // 过滤后的兄弟组之间。

  it("Given a keyword, When scopes are resolved, Then every scope of the axis carries it", () => {
    expect(
      scopesForAxis("project", [1, 2], [], [], "happy").map((s) => s.keyword),
    ).toEqual(["happy", "happy", "happy"]);
    expect(
      scopesForAxis("time", [1], [], [], "happy").map((s) => s.keyword),
    ).toEqual(["happy"]);
    // 机器轴此前没有任何搜索可言：它那一组的会话只有首屏那一页。
    expect(scopesForAxis("machine", [], [0, 7], [], "happy")).toEqual([
      { kind: "machine", deviceID: 0, keyword: "happy" },
      { kind: "machine", deviceID: 7, keyword: "happy" },
    ]);
  });

  it("Given the agent axis, When a keyword is active, Then each agent gets its own query — the top-five window is not enough to search", () => {
    expect(scopesForAxis("agent", [1], [], [7, 8], "happy")).toEqual([
      { kind: "agent", agentID: 7, keyword: "happy" },
      { kind: "agent", agentID: 8, keyword: "happy" },
    ]);
  });

  it("Given the agent axis with no keyword, When scopes are resolved, Then it still sends nothing — ListChatAgents already carries the sessions", () => {
    expect(scopesForAxis("agent", [1], [], [7, 8])).toEqual([]);
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

  // ── 搜索 ──────────────────────────────────────────────────────────────────

  it("Given the project axis is searched, When it mounts, Then the keyword goes out with每个项目组的查询", async () => {
    renderHook(() => useIndexGroups("project", [node(1)], [], "happy"));

    await waitFor(() => expect(listIndex).toHaveBeenCalled());
    for (const call of listIndex.mock.calls) {
      expect(call[0].keyword).toBe("happy");
    }
  });

  it("Given the agent axis is searched, When it renders, Then each agent group is filled from its own filtered query instead of the top-five window", async () => {
    seedAgents({ id: 7, sessions: [{ id: 10 }], totalSessions: 44 });
    listIndex.mockResolvedValue({
      sessions: [{ id: 3495, agentId: 7, projectId: 1, title: "happy 那条" }],
      total: 1,
      hasMore: false,
    });

    const { result } = renderHook(() =>
      useIndexGroups("agent", [], [], "happy"),
    );

    await waitFor(() => expect(result.current[0]?.sessionIDs).toEqual([3495]));
    // 组头的计数也必须是过滤后的口径，否则「会话 44」下面挂着一行搜索结果。
    expect(result.current[0]?.total).toBe(1);
    expect(listIndex).toHaveBeenCalledWith(
      expect.objectContaining({ scope: "agent", agentId: 7, keyword: "happy" }),
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
