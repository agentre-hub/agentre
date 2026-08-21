// frontend/src/components/agentre/session-index/index-projection.test.ts
//
// 适配层是新代码，不受「既有测试即守卫」保护（规格 2026-08-18「已知风险」②），
// 所以它自带这一份：桌面端的组骨架进去、共享投影分好的组出来。
import { describe, expect, it } from "vitest";

import { projectIndexGroups, type IndexGroup } from "./index-projection";

function metasOf(
  ...entries: [number, number][]
): ReadonlyMap<number, { title?: string; lastMessageAt?: number }> {
  return new Map(entries.map(([id, at]) => [id, { lastMessageAt: at }]));
}

function projectSlot(
  id: number,
  depth: number,
  sessionIDs: number[],
  total = sessionIDs.length,
): IndexGroup {
  return {
    key: `project:${id}`,
    kind: "project",
    refID: id,
    depth,
    sessionIDs,
    total,
  };
}

function freeSlot(sessionIDs: number[], total = sessionIDs.length): IndexGroup {
  return { key: "free", kind: "free", refID: 0, depth: 0, sessionIDs, total };
}

function machineSlot(
  deviceID: number,
  sessionIDs: number[],
  total = sessionIDs.length,
): IndexGroup {
  return {
    key: `machine:${deviceID}`,
    kind: "machine",
    refID: deviceID,
    depth: 0,
    sessionIDs,
    total,
  };
}

function agentSlot(
  id: number,
  sessionIDs: number[],
  total = sessionIDs.length,
): IndexGroup {
  return {
    key: `agent:${id}`,
    kind: "agent",
    refID: id,
    depth: 0,
    sessionIDs,
    total,
  };
}

function flatSlot(sessionIDs: number[], total = sessionIDs.length): IndexGroup {
  return { key: "flat", kind: "flat", refID: 0, depth: 0, sessionIDs, total };
}

describe("projectIndexGroups", () => {
  it("Given one page per project, When the projection runs, Then every group keeps its own sessions and the free page lands in 随手对话", () => {
    const groups = projectIndexGroups(
      "project",
      [projectSlot(1, 0, [10]), projectSlot(2, 1, [20]), freeSlot([30])],
      metasOf([10, 100], [20, 200], [30, 300]),
    );

    expect(
      groups.map((g) => [g.key, g.kind, g.refID, g.depth, g.sessionIDs]),
    ).toEqual([
      ["project:1", "project", 1, 0, [10]],
      ["project:2", "project", 2, 1, [20]],
      ["free", "free", 0, 0, [30]],
    ]);
  });

  it("Given a project with nothing loaded and no free session at all, When the projection runs, Then both groups are still there and empty (decision 6)", () => {
    const groups = projectIndexGroups(
      "project",
      [projectSlot(1, 0, []), projectSlot(2, 0, [20]), freeSlot([])],
      metasOf([20, 200]),
    );

    expect(groups.map((g) => [g.key, g.sessionIDs])).toEqual([
      ["project:1", []],
      ["project:2", [20]],
      ["free", []],
    ]);
  });

  it("Given a page whose order disagrees with the last activity, When the projection runs, Then the group lists the newest first", () => {
    const groups = projectIndexGroups(
      "project",
      [projectSlot(1, 0, [10, 20, 30])],
      metasOf([10, 100], [20, 300], [30, 200]),
    );

    expect(groups[0].sessionIDs).toEqual([20, 30, 10]);
  });

  it("Given a session the meta store has never seen, When the projection runs, Then it sinks instead of leading the group", () => {
    const groups = projectIndexGroups(
      "project",
      [projectSlot(1, 0, [99, 10])],
      metasOf([10, 100]),
    );

    expect(groups[0].sessionIDs).toEqual([10, 99]);
  });

  it("Given two sessions with the very same last activity, When the projection runs, Then the order the page came back in decides", () => {
    const groups = projectIndexGroups(
      "project",
      [projectSlot(1, 0, [9, 10, 8])],
      metasOf([9, 100], [10, 100], [8, 100]),
    );

    expect(groups[0].sessionIDs).toEqual([9, 10, 8]);
  });

  it("Given per-group totals, When the projection runs, Then each group carries its own 查看全部 count, empty ones included", () => {
    const groups = projectIndexGroups(
      "project",
      [projectSlot(1, 0, [10], 42), projectSlot(2, 0, [], 7), freeSlot([], 0)],
      metasOf([10, 100]),
    );

    expect(groups.map((g) => g.total)).toEqual([42, 7, 0]);
  });

  it("Given the agent axis, When the projection runs, Then the host's own group order survives and each agent keeps its own sessions", () => {
    const groups = projectIndexGroups(
      "agent",
      [agentSlot(2, [20]), agentSlot(3, [30]), agentSlot(1, [10, 11])],
      metasOf([10, 100], [11, 400], [20, 200], [30, 300]),
    );

    expect(groups.map((g) => [g.refID, g.sessionIDs])).toEqual([
      [2, [20]],
      [3, [30]],
      [1, [11, 10]],
    ]);
  });

  it("Given the time axis with nothing loaded, When the projection runs, Then the single flat group is still returned", () => {
    const groups = projectIndexGroups("time", [flatSlot([], 0)], metasOf());

    expect(groups).toEqual([
      {
        key: "flat",
        kind: "flat",
        refID: 0,
        depth: 0,
        sessionIDs: [],
        total: 0,
      },
    ]);
  });

  it("Given the time axis, When the projection runs, Then the flat group is newest first and carries the paged total", () => {
    const groups = projectIndexGroups(
      "time",
      [flatSlot([8, 9], 42)],
      metasOf([8, 80], [9, 90]),
    );

    expect(groups).toHaveLength(1);
    expect(groups[0].sessionIDs).toEqual([9, 8]);
    expect(groups[0].total).toBe(42);
  });
});

describe("projectIndexGroups —— 机器轴", () => {
  it("组骨架的顺序原样保留：本机在最前，投影不重排组", () => {
    const out = projectIndexGroups(
      "machine",
      [machineSlot(0, [1]), machineSlot(7, [2]), machineSlot(3, [])],
      metasOf([1, 100], [2, 200]),
    );

    expect(out.map((g) => g.refID)).toEqual([0, 7, 3]);
  });

  it("行按最近活动倒序落回它自己那一台机器，不串组", () => {
    const out = projectIndexGroups(
      "machine",
      [machineSlot(0, [1, 3]), machineSlot(7, [2])],
      metasOf([1, 100], [3, 300], [2, 200]),
    );

    expect(out[0].sessionIDs).toEqual([3, 1]);
    expect(out[1].sessionIDs).toEqual([2]);
  });

  it("一条会话都没有的机器照样摆出来，空着（决策 10）", () => {
    // 刚配好的一台 daemon 上没有会话，它也得在索引里看得见 —— 否则用户无从确认
    // 配对生效了。
    const out = projectIndexGroups(
      "machine",
      [machineSlot(0, [1]), machineSlot(9, [], 0)],
      metasOf([1, 100]),
    );

    expect(out).toHaveLength(2);
    expect(out[1].refID).toBe(9);
    expect(out[1].sessionIDs).toEqual([]);
  });

  it("每组的总数从投影回来，「查看全部 N」不失真", () => {
    const out = projectIndexGroups(
      "machine",
      [machineSlot(0, [1], 42)],
      metasOf([1, 100]),
    );

    expect(out[0].total).toBe(42);
  });

  it("本机那一组的键是 device-0，不与「认不出机器」的兜底组撞上", () => {
    // 0 是一台机器；共享投影的 UNKNOWN_MACHINE_KEY 是「这一行没有 deviceId」，
    // 桌面端每条会话都有 exec_device_id，因此那个兜底组永远不该出现。
    const out = projectIndexGroups(
      "machine",
      [machineSlot(0, [1])],
      metasOf([1, 100]),
    );

    expect(out).toHaveLength(1);
    expect(out[0].key).toBe("machine:0");
  });
});
