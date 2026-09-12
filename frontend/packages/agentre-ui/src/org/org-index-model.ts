import { buildOrgReportToMap } from "./reporting";
import {
  ORG_SYSTEM_BADGE,
  type OrgAgentModel,
  type OrgDepartmentModel,
} from "./types";

// 部门轴索引的投影：把「部门 + Agent」两张平表投成一串组头与行。
//
// 与画布/列表两套旧实现的区别只有一条，但它决定了整个形状：**行一律同级**。
// 下属 Agent（departmentId == 0 且 parentAgentId > 0）不再靠缩进表达从属，而是与
// 主管平排、行内带一句 `↳ 主管名`；层级只剩「部门套部门」这一种，缩进因此只发生在
// 组头上。系统 Agent（唯一合法的 dept == 0 且 parent == 0）不属于任何组，置顶单列。
//
// 筛选只决定**哪些行看得见**，不决定组头在不在（决策 13：空部门照常摆组头），
// 也不决定桶的现序 —— 排序落点算的是完整现序，只看可见行会把隐藏的兄弟挤掉。

export type OrgIndexFilters = {
  search: string;
  backendId: number;
  reportsToId: number;
};

export const EMPTY_ORG_FILTERS: OrgIndexFilters = {
  search: "",
  backendId: 0,
  reportsToId: 0,
};

export type OrgIndexRow = {
  agent: OrgAgentModel;
  /** 该行现在挂在哪 —— 也就是 reorderAgents 的两个桶键。 */
  departmentId: number;
  parentAgentId: number;
  /** 行内「↳ 主管」；只有下属 Agent 有，部门成员的主管写在组头的 Lead 上。 */
  reportsToName: string;
  /** 该行在自己那个桶的完整现序里的下标（筛选不影响它）。 */
  bucketIndex: number;
  /** 该桶的完整现序，reorderAgents 的第三个入参就从它算出来。 */
  bucketOrderedIds: number[];
};

export type OrgIndexGroup = {
  department: OrgDepartmentModel;
  /** 子部门缩在父部门里，深度只由部门链决定。 */
  depth: number;
  rows: OrgIndexRow[];
};

export type OrgIndexModel = {
  /** 系统 Agent，以及挂不进任何部门的行（直接汇报给系统 Agent 的、脏数据）。 */
  topRows: OrgIndexRow[];
  groups: OrgIndexGroup[];
  matchedAgents: number;
  totalAgents: number;
};

export type OrgIndexInput = {
  agents: OrgAgentModel[];
  departments: OrgDepartmentModel[];
  filters: OrgIndexFilters;
};

function isSystem(agent: OrgAgentModel): boolean {
  return agent.systemBadge === ORG_SYSTEM_BADGE;
}

function bySortOrder(a: { sortOrder?: number; id: number }, b: typeof a) {
  return (a.sortOrder ?? 0) - (b.sortOrder ?? 0) || a.id - b.id;
}

function bucketKeyOf(agent: OrgAgentModel): string {
  return `${agent.departmentId ?? 0}:${agent.parentAgentId ?? 0}`;
}

export function buildOrgIndex(input: OrgIndexInput): OrgIndexModel {
  const { agents, departments, filters } = input;
  const byId = new Map(agents.map((a) => [a.id, a]));
  const ordered = [...agents].sort(bySortOrder);

  // ── 桶（不受筛选影响）与下属索引 ──
  const buckets = new Map<string, number[]>();
  const childrenOf = new Map<number, OrgAgentModel[]>();
  for (const agent of ordered) {
    if (isSystem(agent)) continue;
    const key = bucketKeyOf(agent);
    if (!buckets.has(key)) buckets.set(key, []);
    buckets.get(key)!.push(agent.id);
    const parentId = agent.parentAgentId ?? 0;
    if ((agent.departmentId ?? 0) === 0 && parentId > 0) {
      if (!childrenOf.has(parentId)) childrenOf.set(parentId, []);
      childrenOf.get(parentId)!.push(agent);
    }
  }

  const matched = matchedAgentIds(agents, departments, filters);

  const makeRow = (agent: OrgAgentModel): OrgIndexRow => {
    const departmentId = agent.departmentId ?? 0;
    const parentAgentId = agent.parentAgentId ?? 0;
    const bucketOrderedIds = buckets.get(bucketKeyOf(agent)) ?? [];
    const parent = parentAgentId > 0 ? byId.get(parentAgentId) : undefined;
    return {
      agent,
      departmentId,
      parentAgentId,
      reportsToName:
        departmentId === 0 && parentAgentId > 0
          ? (parent?.name ?? agent.parentAgentName ?? "")
          : "",
      bucketIndex: bucketOrderedIds.indexOf(agent.id),
      bucketOrderedIds,
    };
  };

  const visited = new Set<number>();
  const emit = (agent: OrgAgentModel, out: OrgIndexRow[]): void => {
    if (visited.has(agent.id)) return;
    visited.add(agent.id);
    if (matched.has(agent.id)) out.push(makeRow(agent));
    for (const child of childrenOf.get(agent.id) ?? []) emit(child, out);
  };

  // ── 组头：部门按 parent 递归，子部门缩在父部门里 ──
  const childDepartments = new Map<number, OrgDepartmentModel[]>();
  for (const department of [...departments].sort(bySortOrder)) {
    const parentId = department.parentId ?? 0;
    if (!childDepartments.has(parentId)) childDepartments.set(parentId, []);
    childDepartments.get(parentId)!.push(department);
  }
  const groups: OrgIndexGroup[] = [];
  const seenDepartments = new Set<number>();
  const walkDepartments = (parentId: number, depth: number): void => {
    for (const department of childDepartments.get(parentId) ?? []) {
      if (seenDepartments.has(department.id)) continue;
      seenDepartments.add(department.id);
      const rows: OrgIndexRow[] = [];
      for (const agent of ordered) {
        if ((agent.departmentId ?? 0) === department.id) emit(agent, rows);
      }
      groups.push({ department, depth, rows });
      walkDepartments(department.id, depth + 1);
    }
  };
  walkDepartments(0, 0);
  // 部门链自己成环时上面走不到，兜底摆在末尾 —— 组头丢失比缩进错更糟。
  for (const department of [...departments].sort(bySortOrder)) {
    if (seenDepartments.has(department.id)) continue;
    seenDepartments.add(department.id);
    const rows: OrgIndexRow[] = [];
    for (const agent of ordered) {
      if ((agent.departmentId ?? 0) === department.id) emit(agent, rows);
    }
    groups.push({ department, depth: 0, rows });
  }

  // ── 顶部：系统 Agent，然后是它的下属与无处可挂的行 ──
  const topRows: OrgIndexRow[] = [];
  for (const agent of ordered) if (isSystem(agent)) emit(agent, topRows);
  for (const agent of ordered) {
    if ((agent.departmentId ?? 0) === 0 && (agent.parentAgentId ?? 0) === 0) {
      emit(agent, topRows);
    }
  }
  // 环里的、或挂在不存在的上级下的，单独放出来：宁可位置不理想，也不能整行消失。
  for (const agent of ordered) emit(agent, topRows);

  return {
    topRows,
    groups,
    matchedAgents: matched.size,
    totalAgents: agents.length,
  };
}

function matchedAgentIds(
  agents: OrgAgentModel[],
  departments: OrgDepartmentModel[],
  filters: OrgIndexFilters,
): Set<number> {
  const keyword = filters.search.trim().toLowerCase();
  // 「汇报给」按有效汇报关系判定（显式 parent ▸ 部门链 leader ▸ CEO 兜底），
  // 单一事实源在 ./reporting.ts。
  const reportTo =
    filters.reportsToId > 0
      ? buildOrgReportToMap(agents, departments)
      : new Map<number, number>();
  const matched = new Set<number>();
  for (const agent of agents) {
    if (
      filters.backendId > 0 &&
      (agent.agentBackendId ?? 0) !== filters.backendId
    ) {
      continue;
    }
    if (
      filters.reportsToId > 0 &&
      (reportTo.get(agent.id) ?? 0) !== filters.reportsToId
    ) {
      continue;
    }
    if (keyword) {
      const haystack = `${agent.name} ${agent.description ?? ""}`.toLowerCase();
      if (!haystack.includes(keyword)) continue;
    }
    matched.add(agent.id);
  }
  return matched;
}

/** 「汇报给」筛选的候选：真的当过某人有效上级的那些 Agent。 */
export function buildOrgReportsToOptions(
  agents: OrgAgentModel[],
  departments: OrgDepartmentModel[],
): OrgAgentModel[] {
  const byId = new Map(agents.map((a) => [a.id, a]));
  const ids = new Set<number>();
  for (const parentId of buildOrgReportToMap(agents, departments).values()) {
    if (parentId !== 0) ids.add(parentId);
  }
  return [...ids]
    .map((id) => byId.get(id))
    .filter((a): a is OrgAgentModel => Boolean(a))
    .sort((a, b) => a.name.localeCompare(b.name, "zh-Hans"));
}
