import { describe, expect, it } from "vitest";

import {
  buildAxisGroups,
  type AgentInfo,
  type GroupKind,
  type IndexAxis,
  type IndexRow,
  type MachineInfo,
  type ProjectNode,
} from "./axis-groups";

/**
 * 四个轴的分组投影。这一层是纯函数：进去的是「已解析出来的会话 + 项目树 +
 * Agent 名单 + 机器名单」，出来的是有序的组。渲染、筛选、选中都不在这里。
 *
 * 这份守卫**随投影一起**从 agentre-server 迁进来（原
 * `frontend/src/__tests__/session-axes.test.ts`，规格 2026-08-17）：实现只有一份，
 * 守卫也只该有一份，否则两端各自改一半、谁也不知道对方的组是怎么排的。
 */

const projects: ProjectNode[] = [
  { syncId: "p-backend", name: "后端", color: "#3B82F6", sortOrder: 0 },
  {
    syncId: "p-server",
    name: "agentre-server",
    color: "#10B981",
    parentSyncId: "p-backend",
    sortOrder: 1,
  },
  { syncId: "p-lonely", name: "没人用的项目", sortOrder: 2 },
];

// 名字用 ASCII:这条用例验的是「组头按名字排」,不该顺带押注 localeCompare 对
// 中文的排序规则(不同 ICU 数据能给出不同答案)。
const agents: AgentInfo[] = [
  { syncId: "ag-fe", name: "Frontend Agent", color: "#F59E0B" },
  { syncId: "ag-be", name: "Backend Agent", color: "#EF4444" },
];

const machines: MachineInfo[] = [
  { deviceId: 20, name: "书房小主机", online: true },
  { deviceId: 21, name: "旧笔记本", online: false },
];

function row(over: Partial<IndexRow> & { key: string }): IndexRow {
  return {
    sessionId: 1,
    deviceId: 20,
    fingerprint: "fp-a",
    agentSyncId: "ag-fe",
    projectSyncId: "p-server",
    updatedAt: 1000,
    title: "重构登录页",
    lifecycleState: "running",
    ...over,
  };
}

describe("会话索引的轴投影", () => {
  it("项目轴：项目递归成树，父组在子组之前且带深度；判不出项目的落「未归项目」并排在最后", () => {
    const groups = buildAxisGroups("project", {
      rows: [row({ key: "a" }), row({ key: "b", projectSyncId: "" })],
      projects,
      agents,
      machines,
    });

    expect(groups.map((g) => [g.key, g.depth])).toEqual([
      ["p-backend", 0],
      ["p-server", 1],
      ["__unassigned_project__", 0],
    ]);
    // 父项目自己没有会话，但它是子项目的组头，不能因为「本组为空」就消失。
    expect(groups[0].rows).toEqual([]);
    expect(groups[1].rows.map((r) => r.key)).toEqual(["a"]);
    expect(groups[2].rows.map((r) => r.key)).toEqual(["b"]);
    // 一条会话都没有的项目不摆出来。
    expect(groups.map((g) => g.key)).not.toContain("p-lonely");
  });

  it("项目轴：会话判进了一个名单里没有的项目时，那一行照样看得见（如实自成一组，与 Agent 轴同一条规则）", () => {
    // 服务端判出了项目同步标识，浏览器这边却没有这个项目：项目树那一次请求失败，
    // 或项目已被删而机器上的路径记录还在。
    const groups = buildAxisGroups("project", {
      rows: [row({ key: "a", projectSyncId: "p-ghost" })],
      projects,
      agents,
      machines,
    });

    // 「未归项目」不收它：那个组的含义是「cwd 配不上任何项目路径」（决策 7），
    // 而这一条是判出来了、只是叫不出名字，两种可操作性不同。
    expect(groups.map((g) => g.key)).toEqual(["p-ghost"]);
    expect(groups[0].label).toBe("p-ghost");
    expect(groups[0].rows.map((r) => r.key)).toEqual(["a"]);
  });

  it("项目轴：父项目不在名单里时子树当根挂，会话不跟着一起消失", () => {
    const groups = buildAxisGroups("project", {
      rows: [row({ key: "a", projectSyncId: "p-child" })],
      projects: [
        ...projects,
        {
          syncId: "p-child",
          name: "父被删了的子项目",
          parentSyncId: "p-gone",
          sortOrder: 0,
        },
      ],
      agents,
      machines,
    });

    expect(groups.map((g) => [g.key, g.depth])).toEqual([["p-child", 0]]);
    expect(groups[0].rows.map((r) => r.key)).toEqual(["a"]);
  });

  it("Agent 轴：只摆有会话的 Agent（决策 10），没有 agentSyncId 的老会话落「未命名」并排在最后", () => {
    const groups = buildAxisGroups("agent", {
      rows: [row({ key: "a" }), row({ key: "b", agentSyncId: "" })],
      projects,
      agents,
      machines,
    });

    // Backend Agent 一条会话都没有，因此不出现——今天 /chat 的骨架行为到此为止。
    expect(groups.map((g) => g.key)).toEqual(["ag-fe", "__unnamed_agent__"]);
    expect(groups[0].label).toBe("Frontend Agent");
    expect(groups[0].color).toBe("#F59E0B");
    expect(groups[0].rows.map((r) => r.key)).toEqual(["a"]);
    expect(groups[1].rows.map((r) => r.key)).toEqual(["b"]);
  });

  it("一条会话都没有时四个轴都交白卷，由宿主的空态承接（决策 10）", () => {
    for (const axis of ["project", "agent", "machine"] as const) {
      const groups = buildAxisGroups(axis, {
        rows: [],
        projects,
        agents,
        machines,
      });
      expect(groups, axis).toEqual([]);
    }
  });

  it("时间轴：不分组，单一平铺，按最后活动时间倒序", () => {
    const groups = buildAxisGroups("time", {
      rows: [
        row({ key: "old", updatedAt: 10 }),
        row({ key: "new", updatedAt: 900 }),
        row({ key: "mid", updatedAt: 500 }),
      ],
      projects,
      agents,
      machines,
    });

    expect(groups).toHaveLength(1);
    expect(groups[0].kind).toBe("all");
    expect(groups[0].label).toBe("");
    expect(groups[0].rows.map((r) => r.key)).toEqual(["new", "mid", "old"]);
  });

  it("机器轴：一台机器一组，离线的机器自己成组并标离线", () => {
    const groups = buildAxisGroups("machine", {
      rows: [row({ key: "a", deviceId: 20 }), row({ key: "b", deviceId: 21 })],
      projects,
      agents,
      machines,
    });

    expect(groups.map((g) => [g.key, g.offline])).toEqual([
      ["device-20", false],
      ["device-21", true],
    ]);
    expect(groups[0].label).toBe("书房小主机");
  });

  it("三个兜底组各自独立，不合并成一个「其他」", () => {
    const groups = buildAxisGroups("project", {
      rows: [row({ key: "a", projectSyncId: "", agentSyncId: "" })],
      projects,
      agents,
      machines,
    });

    // 同一条会话既判不出项目也没有 Agent 标识：项目轴上它只属于「未归项目」，
    // 「未命名」是 Agent 轴的兜底，两者不在同一个轴上出现。
    expect(groups.map((g) => g.key)).toEqual(["__unassigned_project__"]);
  });

  /**
   * 镜像里的一条对话，它的发起端指纹可能对不上账号里的任何一台设备（浏览器发起
   * 的那些，或者那台设备已经不在名单里）。这样的行不能从索引里消失——本体在
   * server 上，读它跟机器在不在没关系。机器轴上它们自成一组。
   */
  it("机器轴：认不出机器的行自成一组、排在最后，不被丢掉", () => {
    const groups = buildAxisGroups("machine", {
      rows: [
        row({ key: "a", deviceId: 20 }),
        row({ key: "b", deviceId: undefined }),
      ],
      projects,
      agents,
      machines,
      labels: { unknownMachine: "未知机器" },
    });

    expect(groups.map((g) => g.key)).toEqual([
      "device-20",
      "__unknown_machine__",
    ]);
    expect(groups[1].label).toBe("未知机器");
    expect(groups[1].offline).toBe(false);
    expect(groups[1].rows.map((r) => r.key)).toEqual(["b"]);
  });

  it("别的轴上，认不出机器的行照常按项目 / Agent / 时间归组", () => {
    const groups = buildAxisGroups("project", {
      rows: [row({ key: "b", deviceId: undefined })],
      projects,
      agents,
      machines,
    });

    const group = groups.find((g) => g.key === "p-server")!;
    expect(group.rows.map((r) => r.key)).toEqual(["b"]);
    expect(group.rows[0].machine).toBeUndefined();
  });

  it("行上带着当前轴没说的那两维（决策 8：行首字形与第二行靠它们渲染）", () => {
    const groups = buildAxisGroups("project", {
      rows: [row({ key: "a" })],
      projects,
      agents,
      machines,
    });
    // p-backend 只是这棵树的组头,会话挂在子项目 p-server 上。
    const group = groups.find((g) => g.key === "p-server")!;

    expect(group.rows[0].agent).toEqual({
      syncId: "ag-fe",
      name: "Frontend Agent",
      color: "#F59E0B",
    });
    expect(group.rows[0].machine).toEqual({
      deviceId: 20,
      name: "书房小主机",
      online: true,
    });
    expect(group.rows[0].project?.name).toBe("agentre-server");
  });
});

/**
 * 契约本身（规格 2026-08-18「共享包承载什么」）：组的形状归这一层，两端共用。
 */
describe("组的形状契约", () => {
  const spread: IndexRow[] = [
    row({ key: "a" }),
    row({
      key: "b",
      projectSyncId: "",
      agentSyncId: "",
      deviceId: undefined,
    }),
  ];

  it("六种组别都由这一层给出，不多也不少", () => {
    const kinds = new Set<GroupKind>();
    for (const axis of ["project", "agent", "time", "machine"] as const) {
      for (const group of buildAxisGroups(axis, {
        rows: spread,
        projects,
        agents,
        machines,
      })) {
        kinds.add(group.kind);
      }
    }

    expect([...kinds].sort()).toEqual([
      "agent",
      "all",
      "machine",
      "project",
      "unassignedProject",
      "unnamedAgent",
    ]);
  });

  /**
   * 「查看全部 N」是桌面端的分页呈现：每个轴各有一条分页查询，组里先来前几条，
   * 总数另说。总数**由宿主逐组传入**，投影不去数——它只看得见已经拿到的那些行。
   */
  it("宿主传了每组总数时，组上带着它", () => {
    const groups = buildAxisGroups("agent", {
      rows: spread,
      projects,
      agents,
      machines,
      totals: { "ag-fe": 42 },
    });

    expect(groups.map((g) => [g.key, g.total])).toEqual([
      ["ag-fe", 42],
      ["__unnamed_agent__", undefined],
    ]);
  });

  it("不传总数即没有分页：组上连这个字段都不长出来，server 侧行为不变", () => {
    const input = { rows: spread, projects, agents, machines };
    const bare = buildAxisGroups("agent", input);
    const paged = buildAxisGroups("agent", {
      ...input,
      totals: { "ag-fe": 42 },
    });

    // 用 hasOwnProperty 而不是 ES2022 的 Object.hasOwn：宿主 app 的 tsconfig
    // 是 lib ES2020 且**不排除**包里的用例文件，用新内置会把宿主的 tsc -b 弄红。
    const carriesTotal = (group: object) =>
      Object.prototype.hasOwnProperty.call(group, "total");

    expect(bare.map(carriesTotal)).toEqual([false, false]);
    expect(paged.map(carriesTotal)).toEqual([true, false]);
    // 总数是**加上去**的一维，别的什么都不动。
    expect(paged.map(({ total: _total, ...rest }) => rest)).toEqual(bare);
  });

  it("轴清单由宿主给，投影只认它给的那一个轴", () => {
    // 桌面端只offer 三个轴（无机器），server offer 四个 —— 同一份投影都吃得下。
    const desktopAxes: IndexAxis[] = ["project", "agent", "time"];
    const built = desktopAxes.map((axis) =>
      buildAxisGroups(axis, { rows: spread, projects, agents, machines }),
    );

    expect(built.map((groups) => groups.map((g) => g.kind))).toEqual([
      ["project", "project", "unassignedProject"],
      ["agent", "unnamedAgent"],
      ["all"],
    ]);
  });
});
