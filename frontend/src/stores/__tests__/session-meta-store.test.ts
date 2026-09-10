import { beforeEach, describe, expect, it } from "vitest";

import { useSessionMetaStore, type SessionMeta } from "../session-meta-store";

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
});
