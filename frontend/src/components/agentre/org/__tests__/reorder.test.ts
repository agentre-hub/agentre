import { describe, expect, it } from "vitest";

import { applyAgentOrder, applyDepartmentOrder } from "../reorder";
import type { OrgAgent, OrgDepartment } from "../types";

const mkAgent = (
  id: number,
  sortOrder: number,
  departmentId = 0,
  parentAgentId = 0,
): OrgAgent =>
  ({
    id,
    name: `Agent ${id}`,
    description: "",
    avatarColor: "agent-1",
    avatarDataUrl: "",
    avatarIcon: "",
    systemBadge: "",
    departmentId,
    departmentName: "",
    parentAgentId,
    parentAgentName: "",
    agentBackendId: 0,
    sortOrder,
    prompt: [],
    skills: [],
    createtime: 0,
    updatetime: 0,
  }) as unknown as OrgAgent;

const mkDept = (id: number, sortOrder: number, parentId = 0): OrgDepartment =>
  ({
    id,
    name: `Dept ${id}`,
    description: "",
    icon: "star",
    accentColor: "agent-1",
    parentId,
    sortOrder,
    leadAgentId: 0,
    leadAgentName: "",
    memberCount: 0,
  }) as unknown as OrgDepartment;

describe("applyAgentOrder", () => {
  it("updates sortOrder for agents in matching departmentId group", () => {
    const agents = [
      mkAgent(1, 10, 5, 0),
      mkAgent(2, 20, 5, 0),
      mkAgent(3, 30, 5, 0),
      mkAgent(4, 5, 0, 0), // different group
    ];
    // reorder: [2, 1, 3]
    const result = applyAgentOrder(agents, 5, 0, [2, 1, 3]);

    const a1 = result.find((a) => a.id === 1)!;
    const a2 = result.find((a) => a.id === 2)!;
    const a3 = result.find((a) => a.id === 3)!;
    const a4 = result.find((a) => a.id === 4)!;

    // a2 is first → sortOrder=1, a1 is second → sortOrder=2, a3 is third → sortOrder=3
    expect(a2.sortOrder).toBe(1);
    expect(a1.sortOrder).toBe(2);
    expect(a3.sortOrder).toBe(3);
    // agent 4 in different group is unchanged
    expect(a4.sortOrder).toBe(5);
  });

  it("updates sortOrder for agents in matching parentAgentId group", () => {
    const agents = [mkAgent(10, 1, 0, 99), mkAgent(11, 2, 0, 99)];
    const result = applyAgentOrder(agents, 0, 99, [11, 10]);
    const a10 = result.find((a) => a.id === 10)!;
    const a11 = result.find((a) => a.id === 11)!;
    expect(a11.sortOrder).toBe(1);
    expect(a10.sortOrder).toBe(2);
  });
});

describe("applyDepartmentOrder", () => {
  it("updates sortOrder for departments under the given parentId", () => {
    const departments = [
      mkDept(1, 10, 0),
      mkDept(2, 20, 0),
      mkDept(3, 30, 5), // different parent
    ];
    const result = applyDepartmentOrder(departments, 0, [2, 1]);
    const d1 = result.find((d) => d.id === 1)!;
    const d2 = result.find((d) => d.id === 2)!;
    const d3 = result.find((d) => d.id === 3)!;

    expect(d2.sortOrder).toBe(1);
    expect(d1.sortOrder).toBe(2);
    expect(d3.sortOrder).toBe(30); // unchanged
  });
});
