import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../../../wailsjs/go/app/App", () => ({
  ListChatIndexSessions: vi.fn(),
}));

import { ListChatIndexSessions } from "../../../wailsjs/go/app/App";
import {
  freeScope,
  projectScope,
  recentScope,
  scopeKey,
  useSessionIndexStore,
} from "../session-index-store";
import { useSessionMetaStore } from "../session-meta-store";
import { useSessionStatusStore } from "../session-status-store";
import { useChatStreamsStore } from "../chat-streams-store";

const listIndex = ListChatIndexSessions as ReturnType<typeof vi.fn>;

function lite(id: number, extra: Record<string, unknown> = {}) {
  return {
    id,
    agentId: 1,
    projectId: 0,
    title: `s-${id}`,
    status: "idle",
    needsAttention: false,
    bgRunning: false,
    lastMessageAt: id * 10,
    lastReadAt: 0,
    ...extra,
  };
}

describe("session-index-store", () => {
  beforeEach(() => {
    listIndex.mockReset();
    useSessionIndexStore.getState().__reset();
    useSessionMetaStore.getState().__reset();
    useSessionStatusStore.getState().__reset();
    useChatStreamsStore.setState({ streams: new Map() });
  });

  it("Given three scopes, When they are keyed, Then projects stay distinct from each other and from recent/free", () => {
    expect(scopeKey(recentScope())).toBe("recent");
    expect(scopeKey(freeScope())).toBe("free");
    expect(scopeKey(projectScope(7))).toBe("project:7");
    expect(scopeKey(projectScope(8))).not.toBe(scopeKey(projectScope(7)));
  });

  it("Given a page of sessions, When it loads, Then every row lands in the meta store carrying BOTH grouping dimensions", async () => {
    // 决策 4/5：索引按一维分组时行首要放另一维，所以每行必须同时知道 agent 与项目。
    // 这正是旧的 ProjectListSessions 通路给不出的东西。
    listIndex.mockResolvedValueOnce({
      sessions: [
        lite(9, { agentId: 3, projectId: 7, title: "in-project" }),
        lite(8, { agentId: 4, projectId: 0, title: "free one" }),
      ],
      total: 2,
      hasMore: false,
    });

    await useSessionIndexStore.getState().loadFirstPage(recentScope());

    const metas = useSessionMetaStore.getState().metas;
    expect(metas.get(9)?.agentId).toBe(3);
    expect(metas.get(9)?.projectId).toBe(7);
    expect(metas.get(9)?.title).toBe("in-project");
    expect(metas.get(8)?.projectId).toBe(0);
  });

  it("Given a page of sessions, When it loads, Then run state lands in the status store including bgRunning", async () => {
    // 项目页旧通路从不传 bgRunning，同一条会话在两个页面因此显示不同（问题 3）。
    listIndex.mockResolvedValueOnce({
      sessions: [
        lite(5, { status: "running", needsAttention: true, bgRunning: true }),
      ],
      total: 1,
      hasMore: false,
    });

    await useSessionIndexStore.getState().loadFirstPage(projectScope(7));

    const status = useSessionStatusStore.getState().statuses.get(5);
    expect(status?.agentStatus).toBe("running");
    expect(status?.needsAttention).toBe(true);
    expect(status?.bgRunning).toBe(true);
  });

  it("Given an active live stream, When a stale idle snapshot arrives, Then running is not clobbered", async () => {
    useSessionStatusStore.getState().upsert(9, {
      agentStatus: "running",
      needsAttention: false,
    });
    useChatStreamsStore.getState().openStream({
      name: "chat:event:9:42",
      sessionId: 9,
      assistantMessageId: 42,
      streamStartedAt: 123,
    });
    listIndex.mockResolvedValueOnce({
      sessions: [lite(9, { status: "idle" })],
      total: 1,
      hasMore: false,
    });

    await useSessionIndexStore.getState().loadFirstPage(recentScope());

    expect(useSessionStatusStore.getState().statuses.get(9)?.agentStatus).toBe(
      "running",
    );
  });

  it("Given a first page, When it loads, Then the store keeps only the id order plus paging facts", async () => {
    listIndex.mockResolvedValueOnce({
      sessions: [lite(9), lite(8), lite(7)],
      total: 30,
      hasMore: true,
    });

    await useSessionIndexStore.getState().loadFirstPage(recentScope(), 3);

    const page = useSessionIndexStore.getState().pages.get("recent")!;
    expect(page.ids).toEqual([9, 8, 7]);
    expect(page.total).toBe(30);
    expect(page.hasMore).toBe(true);
    expect(page.loading).toBe(false);
    expect(listIndex).toHaveBeenCalledWith({
      scope: "recent",
      projectId: 0,
      offset: 0,
      limit: 3,
    });
  });

  it("Given a loaded page, When more is requested, Then it pages from the loaded count and appends", async () => {
    listIndex.mockResolvedValueOnce({
      sessions: [lite(9), lite(8)],
      total: 4,
      hasMore: true,
    });
    await useSessionIndexStore.getState().loadFirstPage(recentScope(), 2);

    listIndex.mockResolvedValueOnce({
      sessions: [lite(7), lite(6)],
      total: 4,
      hasMore: false,
    });
    await useSessionIndexStore.getState().loadMore(recentScope(), 2);

    expect(listIndex).toHaveBeenLastCalledWith({
      scope: "recent",
      projectId: 0,
      offset: 2,
      limit: 2,
    });
    const page = useSessionIndexStore.getState().pages.get("recent")!;
    expect(page.ids).toEqual([9, 8, 7, 6]);
    expect(page.hasMore).toBe(false);
  });

  it("Given a row repeated across pages, When appended, Then it is not listed twice", async () => {
    // 翻页期间有新会话插到最前面会把整个窗口后移一格，第二页第一条就是第一页的最后一条。
    listIndex.mockResolvedValueOnce({
      sessions: [lite(9), lite(8)],
      total: 3,
      hasMore: true,
    });
    await useSessionIndexStore.getState().loadFirstPage(recentScope(), 2);
    listIndex.mockResolvedValueOnce({
      sessions: [lite(8), lite(7)],
      total: 3,
      hasMore: false,
    });
    await useSessionIndexStore.getState().loadMore(recentScope(), 2);

    expect(useSessionIndexStore.getState().pages.get("recent")!.ids).toEqual([
      9, 8, 7,
    ]);
  });

  it("Given a project scope, When it loads, Then the project id travels with the request", async () => {
    listIndex.mockResolvedValueOnce({ sessions: [], total: 0, hasMore: false });

    await useSessionIndexStore.getState().loadFirstPage(projectScope(7), 5);

    expect(listIndex).toHaveBeenCalledWith({
      scope: "project",
      projectId: 7,
      offset: 0,
      limit: 5,
    });
  });

  it("Given the RPC rejects, When loading, Then the error is captured on that page instead of thrown", async () => {
    listIndex.mockRejectedValueOnce(new Error("boom"));

    await expect(
      useSessionIndexStore.getState().loadFirstPage(recentScope()),
    ).resolves.toBeUndefined();

    const page = useSessionIndexStore.getState().pages.get("recent")!;
    expect(page.error).toContain("boom");
    expect(page.loading).toBe(false);
  });

  it("Given one scope is in flight, When the same scope is requested again, Then the call is not duplicated", async () => {
    let resolve!: (v: unknown) => void;
    listIndex.mockReturnValueOnce(
      new Promise((r) => {
        resolve = r;
      }),
    );

    const a = useSessionIndexStore.getState().loadFirstPage(recentScope());
    const b = useSessionIndexStore.getState().loadFirstPage(recentScope());
    resolve({ sessions: [], total: 0, hasMore: false });
    await Promise.all([a, b]);

    expect(listIndex).toHaveBeenCalledTimes(1);
  });

  it("Given two different scopes, When both load, Then they do not dedupe against each other", async () => {
    listIndex.mockResolvedValue({ sessions: [], total: 0, hasMore: false });

    await Promise.all([
      useSessionIndexStore.getState().loadFirstPage(projectScope(7)),
      useSessionIndexStore.getState().loadFirstPage(projectScope(8)),
    ]);

    expect(listIndex).toHaveBeenCalledTimes(2);
  });

  it("Given scopes that were loaded, When the sidebar refreshes, Then only those are refetched and untouched scopes stay silent", async () => {
    listIndex.mockResolvedValue({ sessions: [], total: 0, hasMore: false });
    await useSessionIndexStore.getState().loadFirstPage(projectScope(7));
    listIndex.mockClear();

    await useSessionIndexStore.getState().reloadLoaded();

    expect(listIndex).toHaveBeenCalledTimes(1);
    expect(listIndex).toHaveBeenCalledWith(
      expect.objectContaining({ scope: "project", projectId: 7, offset: 0 }),
    );
  });

  it("Given nothing was ever loaded, When the sidebar refreshes, Then no RPC is sent at all", async () => {
    await useSessionIndexStore.getState().reloadLoaded();

    expect(listIndex).not.toHaveBeenCalled();
  });
});
