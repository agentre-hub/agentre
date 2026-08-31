import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../../../wailsjs/go/app/App", () => ({
  ListChatIndexSessions: vi.fn(),
}));

import { ListChatIndexSessions } from "../../../wailsjs/go/app/App";
import {
  agentScope,
  freeScope,
  machineScope,
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

  it("Given the machine axis, When a machine scope is keyed, Then the local machine is a machine like any other", () => {
    // deviceID 0 是本机（chat_entity.Session 的约定），不是「不限机器」——
    // 它必须有自己的一格页缓存，否则本机那一组会和别的组共用一份 id 列表。
    expect(scopeKey(machineScope(0))).toBe("machine:0");
    expect(scopeKey(machineScope(7))).toBe("machine:7");
    expect(scopeKey(machineScope(0))).not.toBe(scopeKey(machineScope(7)));
    expect(scopeKey(machineScope(0))).not.toBe(scopeKey(recentScope()));
  });

  it("Given a machine scope, When its first page loads, Then the RPC carries the device id", async () => {
    listIndex.mockResolvedValueOnce({
      sessions: [lite(3, { agentId: 1, projectId: 0 })],
      total: 1,
      hasMore: false,
    });

    await useSessionIndexStore.getState().loadFirstPage(machineScope(7));

    expect(listIndex).toHaveBeenCalledWith(
      expect.objectContaining({ scope: "machine", deviceId: 7 }),
    );
  });

  it("Given the local machine, When its first page loads, Then deviceId 0 goes out as 0 rather than being dropped", async () => {
    // 0 是合法值。若这里因为 falsy 被吞成 undefined，服务端会当成漏传。
    listIndex.mockResolvedValueOnce({ sessions: [], total: 0, hasMore: false });

    await useSessionIndexStore.getState().loadFirstPage(machineScope(0));

    expect(listIndex).toHaveBeenCalledWith(
      expect.objectContaining({ scope: "machine", deviceId: 0 }),
    );
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
      // deviceId / agentId / keyword 与 projectId 同理：恒发，只在对应的 scope 下有意义。
      deviceId: 0,
      agentId: 0,
      keyword: "",
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
      // deviceId / agentId / keyword 与 projectId 同理：恒发，只在对应的 scope 下有意义。
      deviceId: 0,
      agentId: 0,
      keyword: "",
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
      // deviceId / agentId / keyword 与 projectId 同理：恒发，只在对应的 scope 下有意义。
      deviceId: 0,
      agentId: 0,
      keyword: "",
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

  // ── 刷新不churn ────────────────────────────────────────────────────────────
  // reloadLoaded 每轮对话被调两次(起手 / 落定)。pages 换新 Map 就等于让订阅它的
  // useIndexGroups 重算所有组、所有行 —— 而绝大多数轮次这些 scope 一条都没变。

  it("Given a loaded scope, When it is refetched with the same rows, Then the pages map is left untouched so the sidebar does not re-render", async () => {
    listIndex.mockResolvedValue({
      sessions: [lite(2), lite(1)],
      total: 2,
      hasMore: false,
    });
    await useSessionIndexStore.getState().loadFirstPage(recentScope());
    const pages = useSessionIndexStore.getState().pages;

    await useSessionIndexStore.getState().reloadLoaded();

    expect(useSessionIndexStore.getState().pages).toBe(pages);
  });

  it("Given a loaded scope, When the refetched rows actually differ, Then the new order lands", async () => {
    listIndex.mockResolvedValueOnce({
      sessions: [lite(2), lite(1)],
      total: 2,
      hasMore: false,
    });
    await useSessionIndexStore.getState().loadFirstPage(recentScope());

    listIndex.mockResolvedValueOnce({
      sessions: [lite(1), lite(2)],
      total: 2,
      hasMore: false,
    });
    await useSessionIndexStore.getState().reloadLoaded();

    expect(useSessionIndexStore.getState().pages.get("recent")?.ids).toEqual([
      1, 2,
    ]);
  });

  it("Given a loaded scope, When it is being refetched, Then loading is not flipped back to true", async () => {
    listIndex.mockResolvedValueOnce({
      sessions: [lite(1)],
      total: 1,
      hasMore: false,
    });
    await useSessionIndexStore.getState().loadFirstPage(recentScope());

    let resolveFn: ((v: unknown) => void) | null = null;
    listIndex.mockReturnValueOnce(
      new Promise((res) => {
        resolveFn = res;
      }),
    );
    const p = useSessionIndexStore.getState().reloadLoaded();

    expect(useSessionIndexStore.getState().pages.get("recent")?.loading).toBe(
      false,
    );

    resolveFn!({ sessions: [lite(1)], total: 1, hasMore: false });
    await p;
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

  // ── 搜索：关键词是 scope 的一部分 ──────────────────────────────────────────
  //
  // 搜索此前只在前端已加载的那一页上做子串匹配，命中范围等于首屏窗口。把关键词并进
  // scope 之后，列表 / 总数 / 翻页全都由服务端按过滤后的口径给出，前端不再有第二套
  // 过滤逻辑 —— 也因此关键词必须进 key，否则搜索结果会盖掉未搜索的那份页缓存。

  it("Given the same scope with and without a keyword, When both are keyed, Then the search page does not clobber the unfiltered one", () => {
    expect(scopeKey(projectScope(7, "happy"))).not.toBe(
      scopeKey(projectScope(7)),
    );
    expect(scopeKey(projectScope(7, "happy"))).not.toBe(
      scopeKey(projectScope(7, "sad")),
    );
    expect(scopeKey(recentScope(""))).toBe(scopeKey(recentScope()));
  });

  it("Given the agent axis is searched, When an agent scope loads, Then the agent id and keyword travel with the request", async () => {
    // Agent 轴平时的会话来自 ListChatAgents 的每 agent 前 5 条；一开搜那个窗口就
    // 不够用了，得按 agent 各查一遍全量。
    listIndex.mockResolvedValueOnce({ sessions: [], total: 0, hasMore: false });

    await useSessionIndexStore.getState().loadFirstPage(agentScope(2, "happy"));

    expect(listIndex).toHaveBeenCalledWith(
      expect.objectContaining({ scope: "agent", agentId: 2, keyword: "happy" }),
    );
  });

  it("Given each axis is searched, When a page loads, Then the keyword rides along with that axis's own scope", async () => {
    listIndex.mockResolvedValue({ sessions: [], total: 0, hasMore: false });

    await useSessionIndexStore
      .getState()
      .loadFirstPage(machineScope(0, "happy"));
    expect(listIndex).toHaveBeenCalledWith(
      expect.objectContaining({
        scope: "machine",
        deviceId: 0,
        keyword: "happy",
      }),
    );

    await useSessionIndexStore.getState().loadFirstPage(freeScope("happy"));
    expect(listIndex).toHaveBeenCalledWith(
      expect.objectContaining({ scope: "free", keyword: "happy" }),
    );
  });

  it("Given a keyword is being typed, When the next keystroke loads, Then the previous keyword's pages are dropped instead of piling up", async () => {
    listIndex.mockResolvedValue({ sessions: [], total: 0, hasMore: false });

    await useSessionIndexStore.getState().loadFirstPage(projectScope(7, "hap"));
    await useSessionIndexStore
      .getState()
      .loadFirstPage(projectScope(7, "happy"));

    const keys = [...useSessionIndexStore.getState().pages.keys()];
    expect(keys).toContain(scopeKey(projectScope(7, "happy")));
    expect(keys).not.toContain(scopeKey(projectScope(7, "hap")));
  });

  it("Given an unfiltered page is cached, When a search runs, Then the unfiltered page survives so clearing the box needs no refetch", async () => {
    listIndex.mockResolvedValue({ sessions: [], total: 0, hasMore: false });

    await useSessionIndexStore.getState().loadFirstPage(projectScope(7));
    await useSessionIndexStore
      .getState()
      .loadFirstPage(projectScope(7, "happy"));

    expect([...useSessionIndexStore.getState().pages.keys()]).toContain(
      scopeKey(projectScope(7)),
    );
  });

  // 机器轴此前根本刷不动：reloadLoaded 把页 key 反解成 scope 时不认 `machine:`，
  // 于是本机那一组每次刷新都去重拉 recent，自己永远停在首屏那一份。
  it("Given a loaded machine scope, When the sidebar refreshes, Then that machine is refetched rather than recent", async () => {
    listIndex.mockResolvedValue({ sessions: [], total: 0, hasMore: false });
    await useSessionIndexStore.getState().loadFirstPage(machineScope(7));
    listIndex.mockClear();

    await useSessionIndexStore.getState().reloadLoaded();

    expect(listIndex).toHaveBeenCalledTimes(1);
    expect(listIndex).toHaveBeenCalledWith(
      expect.objectContaining({ scope: "machine", deviceId: 7, offset: 0 }),
    );
  });

  it("Given a searched scope was loaded, When the sidebar refreshes, Then the keyword survives the refetch", async () => {
    listIndex.mockResolvedValue({ sessions: [], total: 0, hasMore: false });
    await useSessionIndexStore
      .getState()
      .loadFirstPage(projectScope(7, "happy"));
    listIndex.mockClear();

    await useSessionIndexStore.getState().reloadLoaded();

    expect(listIndex).toHaveBeenCalledWith(
      expect.objectContaining({
        scope: "project",
        projectId: 7,
        keyword: "happy",
      }),
    );
  });

  it("Given nothing was ever loaded, When the sidebar refreshes, Then no RPC is sent at all", async () => {
    await useSessionIndexStore.getState().reloadLoaded();

    expect(listIndex).not.toHaveBeenCalled();
  });
});
