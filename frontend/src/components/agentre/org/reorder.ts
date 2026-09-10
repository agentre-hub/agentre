import type { OrgAgent, OrgDepartment } from "./types";

// 排序落点的算法在共享包里（落点 → 写操作那一步要用它），宿主这一层留的是
// 「把新次序写回本地状态」的两个投影 —— 它们吃的是 Wails 的实体对象。
export { computeOrgReorder as computeReorder } from "@agentre-hub/agentre-ui";

/**
 * Applies a new sortOrder to agents that belong to the given
 * (departmentId, parentAgentId) group, based on orderedIds position.
 * Agents outside the group are returned unchanged.
 */
export function applyAgentOrder(
  agents: OrgAgent[],
  departmentId: number,
  parentAgentId: number,
  orderedIds: number[],
): OrgAgent[] {
  const positionMap = new Map<number, number>(
    orderedIds.map((id, index) => [id, index + 1]),
  );
  return agents.map((agent) => {
    const newPos = positionMap.get(agent.id);
    if (newPos === undefined) return agent;
    // Only touch agents in the matching group
    if (
      agent.departmentId !== departmentId ||
      (agent.parentAgentId ?? 0) !== parentAgentId
    ) {
      return agent;
    }
    return Object.assign(
      Object.create(Object.getPrototypeOf(agent) as object) as OrgAgent,
      agent,
      { sortOrder: newPos },
    ) as OrgAgent;
  });
}

/**
 * Applies a new sortOrder to departments under the given parentId,
 * based on orderedIds position. Departments in other groups are unchanged.
 */
export function applyDepartmentOrder(
  departments: OrgDepartment[],
  parentId: number,
  orderedIds: number[],
): OrgDepartment[] {
  const positionMap = new Map<number, number>(
    orderedIds.map((id, index) => [id, index + 1]),
  );
  return departments.map((dept) => {
    const newPos = positionMap.get(dept.id);
    if (newPos === undefined) return dept;
    if (dept.parentId !== parentId) return dept;
    return Object.assign(
      Object.create(Object.getPrototypeOf(dept) as object) as OrgDepartment,
      dept,
      { sortOrder: newPos },
    ) as OrgDepartment;
  });
}

/**
 * Groups a reordered agent id list by each agent's original
 * (departmentId, parentAgentId) placement, preserving relative order
 * within each bucket. Used by the list-view drag handler to call
 * onReorderAgent once per distinct placement group.
 */
export function bucketByPlacement(
  agentById: Map<number, OrgAgent>,
  orderedIds: number[],
): Array<{
  departmentId: number;
  parentAgentId: number;
  orderedIds: number[];
}> {
  const buckets = new Map<
    string,
    { departmentId: number; parentAgentId: number; orderedIds: number[] }
  >();
  for (const id of orderedIds) {
    const a = agentById.get(id);
    if (!a) continue;
    const departmentId = a.departmentId ?? 0;
    const parentAgentId = a.parentAgentId ?? 0;
    const key = `${departmentId}:${parentAgentId}`;
    if (!buckets.has(key)) {
      buckets.set(key, { departmentId, parentAgentId, orderedIds: [] });
    }
    buckets.get(key)!.orderedIds.push(id);
  }
  return [...buckets.values()];
}
