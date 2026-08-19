import { computeReorder } from "./reorder";
import type { OrgAgent, OrgDepartment } from "./types";

// 索引里的三种落点，以及它们各自对应的那个**入参不变**的写操作。
//
// 三种落点一一对应实体层的不变式（agent_entity.Check）：系统 Agent 只能
// dept == 0 && parent == 0，普通 Agent 恰好挂在部门或上级之一。所以「细线 = 排序、
// 整行 = 换汇报对象、组头 = 换部门」已经把合法状态穷举完了，不需要任何修饰键。
//
// 成环判据与服务端逐字同规则（agent_svc.requireActivePlacement → hasAgentCycle）：
// 沿**显式 parentAgentId 链**从新上级往上走，走回自己就是环。这里先拒，是为了让
// 落点当场显示为拒绝态，而不是让用户放手后才吃一个后端错误。

const SYSTEM_BADGE = "DEFAULT";

export type OrgDropTarget =
  | {
      kind: "reorder";
      departmentId: number;
      parentAgentId: number;
      /** 插入位：0..n，"放在该桶现序第 index 位之前"。 */
      index: number;
      orderedIds: number[];
    }
  | { kind: "agent"; agentId: number }
  | { kind: "department"; departmentId: number };

export type OrgDragSubject = { kind: "agent" | "department"; id: number };

export type OrgDropContext = {
  agents: OrgAgent[];
  departments: OrgDepartment[];
};

export type OrgWriteOp =
  | {
      op: "reorderAgents";
      departmentId: number;
      parentAgentId: number;
      orderedIds: number[];
    }
  | {
      op: "moveAgent";
      id: number;
      newDepartmentId: number;
      newParentAgentId: number;
    }
  | { op: "moveDepartment"; id: number; newParentId: number };

function findAgent(ctx: OrgDropContext, id: number): OrgAgent | undefined {
  return ctx.agents.find((a) => a.id === id);
}

function findDepartment(
  ctx: OrgDropContext,
  id: number,
): OrgDepartment | undefined {
  return ctx.departments.find((d) => d.id === id);
}

// 从 from 沿显式上级链往上走，看看会不会走回 selfId。
function agentChainReaches(
  ctx: OrgDropContext,
  from: OrgAgent,
  selfId: number,
): boolean {
  let current: OrgAgent | undefined = from;
  const seen = new Set<number>();
  while (current && !seen.has(current.id)) {
    if (current.id === selfId) return true;
    seen.add(current.id);
    const parentId: number = current.parentAgentId ?? 0;
    current = parentId > 0 ? findAgent(ctx, parentId) : undefined;
  }
  return false;
}

function departmentChainReaches(
  ctx: OrgDropContext,
  from: OrgDepartment,
  selfId: number,
): boolean {
  let current: OrgDepartment | undefined = from;
  const seen = new Set<number>();
  while (current && !seen.has(current.id)) {
    if (current.id === selfId) return true;
    seen.add(current.id);
    const parentId: number = current.parentId ?? 0;
    current = parentId > 0 ? findDepartment(ctx, parentId) : undefined;
  }
  return false;
}

/** 把 Agent 落在这个落点上合不合法。非法落点照样画出来，只是画成拒绝态。 */
export function isValidOrgDrop(
  agentId: number,
  target: OrgDropTarget,
  ctx: OrgDropContext,
): boolean {
  const agent = findAgent(ctx, agentId);
  // 系统 Agent 不可拖动，也不接受任何落点（服务端同样拒绝）。
  if (!agent || agent.systemBadge === SYSTEM_BADGE) return false;

  switch (target.kind) {
    case "reorder":
      return (
        (agent.departmentId ?? 0) === target.departmentId &&
        (agent.parentAgentId ?? 0) === target.parentAgentId &&
        target.orderedIds.includes(agentId)
      );
    case "agent": {
      const parent = findAgent(ctx, target.agentId);
      if (!parent || parent.id === agent.id) return false;
      // 系统 Agent 不接受落点：它是那个「唯一合法的 dept == 0 且 parent == 0」，
      // 挂到它下面在索引里读起来像顶层，实际却是一条普通的上下级边。回到顶层走
      // 详情的「归属」下拉，不走拖拽。
      if (parent.systemBadge === SYSTEM_BADGE) return false;
      return !agentChainReaches(ctx, parent, agent.id);
    }
    case "department":
      return Boolean(findDepartment(ctx, target.departmentId));
  }
}

/** 部门只落在部门组头上；落到自己或自己的后代上会把子树摘出树外。 */
export function isValidOrgDepartmentDrop(
  departmentId: number,
  target: OrgDropTarget,
  ctx: OrgDropContext,
): boolean {
  if (target.kind !== "department") return false;
  const moving = findDepartment(ctx, departmentId);
  const destination = findDepartment(ctx, target.departmentId);
  if (!moving || !destination || moving.id === destination.id) return false;
  return !departmentChainReaches(ctx, destination, moving.id);
}

/** 落点 → 写操作。非法落点不产出任何写操作。 */
export function resolveOrgDrop(
  subject: OrgDragSubject,
  target: OrgDropTarget,
  ctx: OrgDropContext,
): OrgWriteOp | null {
  if (subject.kind === "department") {
    if (!isValidOrgDepartmentDrop(subject.id, target, ctx)) return null;
    if (target.kind !== "department") return null;
    return {
      op: "moveDepartment",
      id: subject.id,
      newParentId: target.departmentId,
    };
  }

  if (!isValidOrgDrop(subject.id, target, ctx)) return null;
  switch (target.kind) {
    case "reorder":
      return {
        op: "reorderAgents",
        departmentId: target.departmentId,
        parentAgentId: target.parentAgentId,
        orderedIds: computeReorder(target.orderedIds, subject.id, target.index),
      };
    case "agent":
      return {
        op: "moveAgent",
        id: subject.id,
        newDepartmentId: 0,
        newParentAgentId: target.agentId,
      };
    case "department":
      return {
        op: "moveAgent",
        id: subject.id,
        newDepartmentId: target.departmentId,
        newParentAgentId: 0,
      };
  }
}
