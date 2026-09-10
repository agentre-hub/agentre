import { describe, expect, it } from "vitest";

import {
  isValidOrgDepartmentDrop,
  isValidOrgDrop,
  resolveOrgDrop,
  type OrgDropContext,
  type OrgDropTarget,
} from "./org-drop";
import type { OrgAgentModel, OrgDepartmentModel } from "./types";

function dept(
  p: Partial<OrgDepartmentModel> & { id: number },
): OrgDepartmentModel {
  return {
    id: p.id,
    name: `d${p.id}`,
    parentId: p.parentId ?? 0,
    leadAgentId: 0,
    sortOrder: 0,
  } as OrgDepartmentModel;
}

function agent(p: Partial<OrgAgentModel> & { id: number }): OrgAgentModel {
  return {
    id: p.id,
    name: `a${p.id}`,
    systemBadge: p.systemBadge ?? "",
    departmentId: p.departmentId ?? 0,
    parentAgentId: p.parentAgentId ?? 0,
    sortOrder: p.sortOrder ?? 0,
  } as OrgAgentModel;
}

// CEO(1) ─ Eva(2, 工程部) ─ Bob(3, 汇报给 Eva) ─ Cid(4, 汇报给 Bob)
//          Dan(5, 工程部)   Ann(6, 产品部)
const ctx: OrgDropContext = {
  agents: [
    agent({ id: 1, systemBadge: "DEFAULT" }),
    agent({ id: 2, departmentId: 1, sortOrder: 1 }),
    agent({ id: 3, parentAgentId: 2 }),
    agent({ id: 4, parentAgentId: 3 }),
    agent({ id: 5, departmentId: 1, sortOrder: 2 }),
    agent({ id: 6, departmentId: 2 }),
  ],
  departments: [dept({ id: 1 }), dept({ id: 2 }), dept({ id: 3, parentId: 1 })],
};

const reorderTarget: OrgDropTarget = {
  kind: "reorder",
  departmentId: 1,
  parentAgentId: 0,
  index: 0,
  orderedIds: [2, 5],
};

describe("isValidOrgDrop", () => {
  it("同桶的细线合法，跨桶的不合法", () => {
    expect(isValidOrgDrop(5, reorderTarget, ctx)).toBe(true);
    // Ann 在产品部，落到工程部那条细线上是跨桶
    expect(isValidOrgDrop(6, reorderTarget, ctx)).toBe(false);
    // 下属 Agent 的桶是 (0, 上级)，与部门桶不是同一个
    expect(isValidOrgDrop(3, reorderTarget, ctx)).toBe(false);
  });

  it("落到另一个 Agent 行上换汇报对象；落到自己或自己的后代上不合法", () => {
    expect(isValidOrgDrop(2, { kind: "agent", agentId: 5 }, ctx)).toBe(true);
    expect(isValidOrgDrop(2, { kind: "agent", agentId: 2 }, ctx)).toBe(false);
    // 成环：Bob 是 Eva 的下属，Cid 是 Bob 的下属
    expect(isValidOrgDrop(2, { kind: "agent", agentId: 3 }, ctx)).toBe(false);
    expect(isValidOrgDrop(2, { kind: "agent", agentId: 4 }, ctx)).toBe(false);
    expect(isValidOrgDrop(2, { kind: "agent", agentId: 99 }, ctx)).toBe(false);
  });

  it("落到部门组头上换部门；部门不存在则不合法", () => {
    expect(
      isValidOrgDrop(2, { kind: "department", departmentId: 2 }, ctx),
    ).toBe(true);
    expect(
      isValidOrgDrop(2, { kind: "department", departmentId: 99 }, ctx),
    ).toBe(false);
  });

  it("系统 Agent 也不接受任何落点：谁都挂不到它下面", () => {
    expect(isValidOrgDrop(2, { kind: "agent", agentId: 1 }, ctx)).toBe(false);
    expect(isValidOrgDrop(6, { kind: "agent", agentId: 1 }, ctx)).toBe(false);
  });

  it("系统 Agent 不可拖动，三种落点一律不合法", () => {
    expect(isValidOrgDrop(1, { kind: "agent", agentId: 2 }, ctx)).toBe(false);
    expect(
      isValidOrgDrop(1, { kind: "department", departmentId: 1 }, ctx),
    ).toBe(false);
    expect(
      isValidOrgDrop(
        1,
        { ...reorderTarget, orderedIds: [1, 2], departmentId: 0 },
        ctx,
      ),
    ).toBe(false);
  });
});

describe("isValidOrgDepartmentDrop", () => {
  it("部门只落在部门组头上，且不能落到自己或自己的后代上", () => {
    expect(
      isValidOrgDepartmentDrop(1, { kind: "department", departmentId: 2 }, ctx),
    ).toBe(true);
    expect(
      isValidOrgDepartmentDrop(1, { kind: "department", departmentId: 1 }, ctx),
    ).toBe(false);
    expect(
      isValidOrgDepartmentDrop(1, { kind: "department", departmentId: 3 }, ctx),
    ).toBe(false);
    expect(
      isValidOrgDepartmentDrop(2, { kind: "agent", agentId: 1 }, ctx),
    ).toBe(false);
    expect(isValidOrgDepartmentDrop(2, reorderTarget, ctx)).toBe(false);
  });
});

describe("resolveOrgDrop 三种落点各自发出的写操作", () => {
  it("细线 → reorderAgents(部门, 上级, 新次序)", () => {
    expect(
      resolveOrgDrop({ kind: "agent", id: 5 }, reorderTarget, ctx),
    ).toEqual({
      op: "reorderAgents",
      departmentId: 1,
      parentAgentId: 0,
      orderedIds: [5, 2],
    });
  });

  it("整行 → moveAgent{ newDepartmentId: 0, newParentAgentId }", () => {
    expect(
      resolveOrgDrop(
        { kind: "agent", id: 2 },
        { kind: "agent", agentId: 5 },
        ctx,
      ),
    ).toEqual({
      op: "moveAgent",
      id: 2,
      newDepartmentId: 0,
      newParentAgentId: 5,
    });
  });

  it("组头 → moveAgent{ newDepartmentId, newParentAgentId: 0 }", () => {
    expect(
      resolveOrgDrop(
        { kind: "agent", id: 2 },
        { kind: "department", departmentId: 2 },
        ctx,
      ),
    ).toEqual({
      op: "moveAgent",
      id: 2,
      newDepartmentId: 2,
      newParentAgentId: 0,
    });
  });

  it("部门落在部门组头上 → moveDepartment", () => {
    expect(
      resolveOrgDrop(
        { kind: "department", id: 2 },
        { kind: "department", departmentId: 1 },
        ctx,
      ),
    ).toEqual({ op: "moveDepartment", id: 2, newParentId: 1 });
  });

  it("非法落点一律不产出写操作", () => {
    expect(
      resolveOrgDrop(
        { kind: "agent", id: 2 },
        { kind: "agent", agentId: 4 },
        ctx,
      ),
    ).toBeNull();
    expect(
      resolveOrgDrop({ kind: "agent", id: 6 }, reorderTarget, ctx),
    ).toBeNull();
    expect(
      resolveOrgDrop(
        { kind: "department", id: 1 },
        { kind: "department", departmentId: 3 },
        ctx,
      ),
    ).toBeNull();
    expect(
      resolveOrgDrop(
        { kind: "department", id: 1 },
        { kind: "agent", agentId: 2 },
        ctx,
      ),
    ).toBeNull();
  });
});
