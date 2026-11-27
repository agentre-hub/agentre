import { beforeEach, describe, expect, it, vi } from "vitest";

import { useSessionMetaStore, type SessionMeta } from "../session-meta-store";

const appMocks = vi.hoisted(() => ({
  ListChatSessionsByIDs: vi.fn(),
}));
vi.mock("../../../wailsjs/go/app/App", () => appMocks);

describe("session-meta-store", () => {
  beforeEach(() => {
    useSessionMetaStore.getState().__reset();
  });

  it("setMeta 后能读出", () => {
    useSessionMetaStore.getState().setMeta(12, {
      agentId: 3,
      agentName: "CEO",
      agentColor: "agent-2",
      projectId: 7,
      title: "arch-review · jwt",
    });
    const meta = useSessionMetaStore.getState().metas.get(12);
    expect(meta).toEqual({
      agentId: 3,
      agentName: "CEO",
      agentColor: "agent-2",
      projectId: 7,
      title: "arch-review · jwt",
    });
  });

  it("同值短路, 不重建 Map", () => {
    const m: SessionMeta = {
      agentId: 1,
      agentName: "A",
      agentColor: "agent-1",
      projectId: 0,
      title: "t",
    };
    useSessionMetaStore.getState().setMeta(1, m);
    const first = useSessionMetaStore.getState().metas;
    useSessionMetaStore.getState().setMeta(1, { ...m });
    expect(useSessionMetaStore.getState().metas).toBe(first);
  });

  it("不同 sessionId 互不影响", () => {
    useSessionMetaStore.getState().setMeta(1, {
      agentId: 1,
      agentName: "A",
      agentColor: "agent-1",
      projectId: 0,
      title: "a",
    });
    useSessionMetaStore.getState().setMeta(2, {
      agentId: 2,
      agentName: "B",
      agentColor: "agent-2",
      projectId: 5,
      title: "b",
    });
    expect(useSessionMetaStore.getState().metas.size).toBe(2);
  });

  it("bulkUpsert 整批写入", () => {
    useSessionMetaStore.getState().bulkUpsert([
      [
        1,
        {
          agentId: 1,
          agentName: "A",
          agentColor: "agent-1",
          projectId: 0,
          title: "a",
          lastMessageAt: 100,
        },
      ],
      [
        2,
        {
          agentId: 2,
          agentName: "B",
          agentColor: "agent-2",
          projectId: 5,
          title: "b",
          lastMessageAt: 200,
        },
      ],
    ]);
    expect(useSessionMetaStore.getState().metas.size).toBe(2);
    expect(useSessionMetaStore.getState().metas.get(1)?.title).toBe("a");
    expect(useSessionMetaStore.getState().metas.get(2)?.lastMessageAt).toBe(
      200,
    );
  });

  it("bulkUpsert 整批全等时不换 Map 引用", () => {
    const m: SessionMeta = {
      agentId: 1,
      agentName: "A",
      agentColor: "agent-1",
      projectId: 0,
      title: "t",
    };
    useSessionMetaStore.getState().setMeta(1, m);
    const first = useSessionMetaStore.getState().metas;
    useSessionMetaStore.getState().bulkUpsert([[1, { ...m }]]);
    expect(useSessionMetaStore.getState().metas).toBe(first);
  });

  describe("loadMissing: 给缓存里还没有的会话补元数据（恢复的会话标签）", () => {
    const row = (id: number, title: string) => ({
      id,
      agentId: 3,
      projectId: 7,
      title,
      status: "idle",
      needsAttention: false,
      bgRunning: false,
      lastMessageAt: 50,
      lastReadAt: 40,
    });

    beforeEach(() => {
      appMocks.ListChatSessionsByIDs.mockReset();
    });

    it("Given 两个缺元数据的会话, When loadMissing, Then 一次批量取回并写进缓存", async () => {
      appMocks.ListChatSessionsByIDs.mockResolvedValue({
        sessions: [row(6, "六"), row(8, "八")],
      });

      await useSessionMetaStore.getState().loadMissing([6, 8]);

      expect(appMocks.ListChatSessionsByIDs).toHaveBeenCalledTimes(1);
      expect(appMocks.ListChatSessionsByIDs).toHaveBeenCalledWith({
        ids: [6, 8],
      });
      expect(useSessionMetaStore.getState().metas.get(6)).toMatchObject({
        agentId: 3,
        projectId: 7,
        title: "六",
        lastMessageAt: 50,
        lastReadAt: 40,
      });
      expect(useSessionMetaStore.getState().metas.get(8)?.title).toBe("八");
    });

    it("Given 一个已缓存、一个非法 id, When loadMissing, Then 只取缺的那个", async () => {
      useSessionMetaStore.getState().setMeta(6, {
        agentId: 1,
        agentName: "A",
        agentColor: "agent-1",
        title: "已缓存",
      });
      appMocks.ListChatSessionsByIDs.mockResolvedValue({ sessions: [] });

      await useSessionMetaStore.getState().loadMissing([6, 0, 9, 9]);

      expect(appMocks.ListChatSessionsByIDs).toHaveBeenCalledWith({ ids: [9] });
    });

    it("Given 全部已缓存, When loadMissing, Then 不发请求", async () => {
      useSessionMetaStore.getState().setMeta(6, {
        agentId: 1,
        agentName: "A",
        agentColor: "agent-1",
        title: "已缓存",
      });

      await useSessionMetaStore.getState().loadMissing([6]);

      expect(appMocks.ListChatSessionsByIDs).not.toHaveBeenCalled();
    });

    it("Given 会话已不存在, When 再次 loadMissing 同一个 id, Then 不重复去取", async () => {
      appMocks.ListChatSessionsByIDs.mockResolvedValue({ sessions: [] });

      await useSessionMetaStore.getState().loadMissing([6]);
      await useSessionMetaStore.getState().loadMissing([6]);

      expect(appMocks.ListChatSessionsByIDs).toHaveBeenCalledTimes(1);
      expect(useSessionMetaStore.getState().metas.has(6)).toBe(false);
    });

    it("Given 读取失败, When loadMissing, Then 安静吞掉、缓存不变", async () => {
      appMocks.ListChatSessionsByIDs.mockRejectedValue(new Error("db gone"));

      await expect(
        useSessionMetaStore.getState().loadMissing([6]),
      ).resolves.toBeUndefined();

      expect(useSessionMetaStore.getState().metas.has(6)).toBe(false);
    });

    it("Given 取的途中面板已写入详情, When 响应到达, Then 不用较旧的索引行盖掉它", async () => {
      let resolve!: (v: { sessions: unknown[] }) => void;
      appMocks.ListChatSessionsByIDs.mockReturnValue(
        new Promise((r) => {
          resolve = r;
        }),
      );

      const pending = useSessionMetaStore.getState().loadMissing([6]);
      useSessionMetaStore.getState().setMeta(6, {
        agentId: 3,
        agentName: "设计师",
        agentColor: "agent-9",
        projectId: 7,
        title: "面板写的新标题",
      });
      resolve({ sessions: [row(6, "旧标题")] });
      await pending;

      expect(useSessionMetaStore.getState().metas.get(6)?.title).toBe(
        "面板写的新标题",
      );
    });
  });
});
