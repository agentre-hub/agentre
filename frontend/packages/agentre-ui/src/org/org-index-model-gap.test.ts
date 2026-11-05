import { describe, expect, it } from "vitest";

import { buildOrgIndex } from "./org-index-model";
import type { OrgAgentModel, OrgDepartmentModel } from "./types";

const EMPTY_FILTERS = { search: "", backendId: 0, reportsToId: 0 };

function agent(p: Partial<OrgAgentModel> & { id: number }): OrgAgentModel {
  return {
    id: p.id,
    name: p.name ?? `a${p.id}`,
    description: "",
    avatarColor: "agent-1",
    avatarIcon: "",
    avatarDataUrl: "",
    systemBadge: p.systemBadge ?? "",
    departmentId: p.departmentId ?? 0,
    departmentName: "",
    parentAgentId: p.parentAgentId ?? 0,
    parentAgentName: "",
    agentBackendId: p.agentBackendId ?? 0,
    sortOrder: p.sortOrder ?? 0,
    prompt: [],
    skills: [],
  } as unknown as OrgAgentModel;
}

const departments = [
  { id: 1, name: "工程部", parentId: 0, sortOrder: 1 },
] as unknown as OrgDepartmentModel[];

const agents = [
  agent({ id: 1, name: "CEO 助手", systemBadge: "DEFAULT" }),
  agent({ id: 2, name: "Eva", departmentId: 1, sortOrder: 1 }),
  agent({ id: 3, name: "Dan", departmentId: 1, sortOrder: 2 }),
  agent({ id: 4, name: "Bob", departmentId: 1, sortOrder: 3 }),
];

function visibleIds(model: ReturnType<typeof buildOrgIndex>): number[] {
  return [...model.topRows, ...model.groups.flatMap((group) => group.rows)].map(
    (row) => row.agent.id,
  );
}

describe("buildOrgIndex 「待配置」筛选", () => {
  it("给了缺口 Agent 名单时只看得见名单里的行，桶的完整现序不受影响", () => {
    const model = buildOrgIndex({
      agents,
      departments,
      filters: { ...EMPTY_FILTERS, needsSetupAgentIds: [2, 4] },
    });

    expect(visibleIds(model)).toEqual([2, 4]);
    expect(model.matchedAgents).toBe(2);
    const bob = model.groups[0].rows.find((row) => row.agent.id === 4)!;
    expect(bob.bucketOrderedIds).toEqual([2, 3, 4]);
    expect(bob.bucketIndex).toBe(2);
  });

  it("名单为空时一行都不命中（筛选开着、没有缺口）", () => {
    const model = buildOrgIndex({
      agents,
      departments,
      filters: { ...EMPTY_FILTERS, needsSetupAgentIds: [] },
    });

    expect(visibleIds(model)).toEqual([]);
    expect(model.matchedAgents).toBe(0);
  });

  it("不传这一维时行为与今天一致：全部可见（agentre-server 不传）", () => {
    const model = buildOrgIndex({
      agents,
      departments,
      filters: EMPTY_FILTERS,
    });

    expect(visibleIds(model)).toEqual([1, 2, 3, 4]);
  });

  it("与其他维度取交集", () => {
    const model = buildOrgIndex({
      agents,
      departments,
      filters: { ...EMPTY_FILTERS, search: "bob", needsSetupAgentIds: [2, 4] },
    });

    expect(visibleIds(model)).toEqual([4]);
  });
});
