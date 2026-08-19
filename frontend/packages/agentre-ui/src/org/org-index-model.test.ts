import { describe, expect, it } from "vitest";

import {
  buildOrgIndex,
  buildOrgReportsToOptions,
  EMPTY_ORG_FILTERS,
} from "./org-index-model";
import type { OrgAgentModel, OrgDepartmentModel } from "./types";

function dept(
  p: Partial<OrgDepartmentModel> & { id: number },
): OrgDepartmentModel {
  return {
    id: p.id,
    name: p.name ?? `d${p.id}`,
    description: "",
    icon: "hammer",
    accentColor: "agent-2",
    parentId: p.parentId ?? 0,
    leadAgentId: p.leadAgentId ?? 0,
    leadAgentName: p.leadAgentName ?? "",
    sortOrder: p.sortOrder ?? 0,
    directAgentCount: 0,
    subdepartmentCount: 0,
    memberCount: 0,
    createtime: 0,
    updatetime: 0,
  } as OrgDepartmentModel;
}

function agent(p: Partial<OrgAgentModel> & { id: number }): OrgAgentModel {
  return {
    id: p.id,
    name: p.name ?? `a${p.id}`,
    description: p.description ?? "",
    avatarColor: "agent-1",
    avatarIcon: "",
    avatarDataUrl: "",
    systemBadge: p.systemBadge ?? "",
    departmentId: p.departmentId ?? 0,
    departmentName: "",
    parentAgentId: p.parentAgentId ?? 0,
    parentAgentName: p.parentAgentName ?? "",
    agentBackendId: p.agentBackendId ?? 0,
    sortOrder: p.sortOrder ?? 0,
    prompt: [],
    skills: [],
  } as unknown as OrgAgentModel;
}

const ceo = agent({ id: 1, name: "CEO 助手", systemBadge: "DEFAULT" });

describe("buildOrgIndex 部门轴", () => {
  it("子部门跟在父部门后面并带 depth，空部门照常摆组头", () => {
    const model = buildOrgIndex({
      agents: [ceo],
      departments: [
        dept({ id: 1, name: "工程部", sortOrder: 1 }),
        dept({ id: 2, name: "平台组", parentId: 1 }),
        dept({ id: 3, name: "产品部", sortOrder: 2 }),
      ],
      filters: EMPTY_ORG_FILTERS,
    });

    expect(
      model.groups.map((g) => [g.department.name, g.depth, g.rows.length]),
    ).toEqual([
      ["工程部", 0, 0],
      ["平台组", 1, 0],
      ["产品部", 0, 0],
    ]);
  });

  it("下属 Agent 与主管平排落在主管所在的组里，并带上主管名", () => {
    const eva = agent({ id: 2, name: "Eva", departmentId: 1, sortOrder: 1 });
    const bob = agent({ id: 3, name: "Bob", parentAgentId: 2, sortOrder: 1 });
    const model = buildOrgIndex({
      agents: [ceo, eva, bob],
      departments: [dept({ id: 1, name: "工程部", leadAgentId: 2 })],
      filters: EMPTY_ORG_FILTERS,
    });

    const group = model.groups[0];
    expect(group.rows.map((r) => r.agent.name)).toEqual(["Eva", "Bob"]);
    // 下属只在行内说主管，不靠缩进
    expect(group.rows.map((r) => r.reportsToName)).toEqual(["", "Eva"]);
    // 桶键就是写操作的入参：Eva 在部门桶，Bob 在上级桶
    expect(group.rows.map((r) => [r.departmentId, r.parentAgentId])).toEqual([
      [1, 0],
      [0, 2],
    ]);
  });

  it("孙下属也平排在同一个组里，主管名是它自己的直属上级", () => {
    const eva = agent({ id: 2, name: "Eva", departmentId: 1 });
    const bob = agent({ id: 3, name: "Bob", parentAgentId: 2 });
    const cid = agent({ id: 4, name: "Cid", parentAgentId: 3 });
    const model = buildOrgIndex({
      agents: [ceo, eva, bob, cid],
      departments: [dept({ id: 1 })],
      filters: EMPTY_ORG_FILTERS,
    });

    expect(model.groups[0].rows.map((r) => r.agent.name)).toEqual([
      "Eva",
      "Bob",
      "Cid",
    ]);
    expect(model.groups[0].rows.map((r) => r.reportsToName)).toEqual([
      "",
      "Eva",
      "Bob",
    ]);
  });

  it("系统 Agent 置顶且不属于任何组，直接汇报给它的 Agent 与它平排", () => {
    const solo = agent({ id: 9, name: "Solo", parentAgentId: 1 });
    const model = buildOrgIndex({
      agents: [ceo, solo],
      departments: [dept({ id: 1 })],
      filters: EMPTY_ORG_FILTERS,
    });

    expect(model.topRows.map((r) => r.agent.name)).toEqual([
      "CEO 助手",
      "Solo",
    ]);
    expect(model.topRows.map((r) => r.reportsToName)).toEqual(["", "CEO 助手"]);
    expect(model.groups[0].rows).toEqual([]);
  });

  it("组内按 sortOrder 排，同序按 id 兜底，并给出该桶的完整现序", () => {
    const model = buildOrgIndex({
      agents: [
        ceo,
        agent({ id: 5, name: "E5", departmentId: 1, sortOrder: 2 }),
        agent({ id: 4, name: "E4", departmentId: 1, sortOrder: 1 }),
        agent({ id: 6, name: "E6", departmentId: 1, sortOrder: 2 }),
      ],
      departments: [dept({ id: 1 })],
      filters: EMPTY_ORG_FILTERS,
    });

    const rows = model.groups[0].rows;
    expect(rows.map((r) => r.agent.id)).toEqual([4, 5, 6]);
    expect(rows.map((r) => r.bucketIndex)).toEqual([0, 1, 2]);
    expect(rows[0].bucketOrderedIds).toEqual([4, 5, 6]);
  });

  it("父子互指的脏数据既不死循环也不丢行", () => {
    const model = buildOrgIndex({
      agents: [
        agent({ id: 7, name: "L", parentAgentId: 8 }),
        agent({ id: 8, name: "R", parentAgentId: 7 }),
      ],
      departments: [],
      filters: EMPTY_ORG_FILTERS,
    });

    expect(model.topRows.map((r) => r.agent.name).sort()).toEqual(["L", "R"]);
  });
});

describe("buildOrgIndex 筛选", () => {
  const eva = agent({
    id: 2,
    name: "Eva",
    description: "工程总监",
    departmentId: 1,
    agentBackendId: 5,
  });
  const bob = agent({
    id: 3,
    name: "Bob",
    parentAgentId: 2,
    agentBackendId: 6,
  });
  const departments = [dept({ id: 1, name: "工程部", leadAgentId: 2 })];
  const agents = [ceo, eva, bob];

  it("搜索命中名字或描述，组头照旧在场", () => {
    const model = buildOrgIndex({
      agents,
      departments,
      filters: { ...EMPTY_ORG_FILTERS, search: "工程总监" },
    });

    expect(model.groups).toHaveLength(1);
    expect(model.groups[0].rows.map((r) => r.agent.name)).toEqual(["Eva"]);
    expect(model.topRows).toEqual([]);
    expect(model.matchedAgents).toBe(1);
    expect(model.totalAgents).toBe(3);
  });

  it("按后端筛选", () => {
    const model = buildOrgIndex({
      agents,
      departments,
      filters: { ...EMPTY_ORG_FILTERS, backendId: 6 },
    });

    expect(model.groups[0].rows.map((r) => r.agent.name)).toEqual(["Bob"]);
    expect(model.matchedAgents).toBe(1);
  });

  it("按汇报对象筛选走部门链 leader，而不是只看显式 parent", () => {
    const carol = agent({ id: 4, name: "Carol", departmentId: 1 });
    const model = buildOrgIndex({
      agents: [...agents, carol],
      departments,
      filters: { ...EMPTY_ORG_FILTERS, reportsToId: 2 },
    });

    // Bob 显式挂在 Eva 下；Carol 挂在 Eva 当 leader 的部门里 —— 两条都算汇报给 Eva
    expect(model.groups[0].rows.map((r) => r.agent.name)).toEqual([
      "Bob",
      "Carol",
    ]);
  });

  it("筛选过后桶的现序仍是完整现序（排序落点不能只看可见行）", () => {
    const model = buildOrgIndex({
      agents: [
        ceo,
        agent({ id: 4, name: "E4", departmentId: 1, sortOrder: 1 }),
        agent({ id: 5, name: "E5", departmentId: 1, sortOrder: 2 }),
      ],
      departments: [dept({ id: 1 })],
      filters: { ...EMPTY_ORG_FILTERS, search: "E5" },
    });

    const rows = model.groups[0].rows;
    expect(rows.map((r) => r.agent.id)).toEqual([5]);
    expect(rows[0].bucketIndex).toBe(1);
    expect(rows[0].bucketOrderedIds).toEqual([4, 5]);
  });
});

describe("buildOrgReportsToOptions", () => {
  it("候选只列真的当过上级的 Agent，按名字排", () => {
    const eva = agent({ id: 2, name: "Eva", departmentId: 1 });
    const bob = agent({ id: 3, name: "Bob", parentAgentId: 2 });
    const options = buildOrgReportsToOptions(
      [ceo, eva, bob],
      [dept({ id: 1, leadAgentId: 2 })],
    );

    expect(options.map((a) => a.name)).toEqual(["CEO 助手", "Eva"]);
  });
});
