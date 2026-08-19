import {
  ORG_SYSTEM_BADGE,
  type OrgAgentModel,
  type OrgDepartmentModel,
} from "./types";

// "汇报给" 单一事实源。
//
// 规则：
//   1. 显式 parent_agent_id 优先（必须指向真实存在、且不是自己）；
//   2. 沿 department.parentId 向上找第一个 leadAgentId 不是自己的部门，用该部门的 leader；
//   3. 都没有就兜底到 CEO 助手（systemBadge === "DEFAULT"）；
//   4. CEO 自身、或既没有 CEO 也没有任何 leader 时返回 0（无上级）。
//
// 这一层只产出"agent id → agent id"的有向边，不做任何 UI 决策；索引投影、详情等
// 上层组件只读这张表，避免散落多份等价但不同步的解析逻辑。

type Ctx = {
  agentIds: Set<number>;
  deptById: Map<number, OrgDepartmentModel>;
  ceoId: number;
};

function buildCtx(
  agents: OrgAgentModel[],
  departments: OrgDepartmentModel[],
): Ctx {
  const ceo = agents.find((a) => a.systemBadge === ORG_SYSTEM_BADGE);
  return {
    agentIds: new Set(agents.map((a) => a.id)),
    deptById: new Map(departments.map((d) => [d.id, d])),
    ceoId: ceo ? ceo.id : 0,
  };
}

function resolveOne(agent: OrgAgentModel, ctx: Ctx): number {
  if (
    agent.parentAgentId &&
    agent.parentAgentId !== agent.id &&
    ctx.agentIds.has(agent.parentAgentId)
  ) {
    return agent.parentAgentId;
  }
  if (agent.systemBadge === ORG_SYSTEM_BADGE) return 0;

  let dept = agent.departmentId
    ? ctx.deptById.get(agent.departmentId)
    : undefined;
  const seen = new Set<number>();
  while (dept && !seen.has(dept.id)) {
    seen.add(dept.id);
    if (
      dept.leadAgentId &&
      dept.leadAgentId !== agent.id &&
      ctx.agentIds.has(dept.leadAgentId)
    ) {
      return dept.leadAgentId;
    }
    dept = dept.parentId ? ctx.deptById.get(dept.parentId) : undefined;
  }

  return ctx.ceoId && ctx.ceoId !== agent.id ? ctx.ceoId : 0;
}

export function resolveOrgReportTo(
  agent: OrgAgentModel,
  agents: OrgAgentModel[],
  departments: OrgDepartmentModel[],
): number {
  return resolveOne(agent, buildCtx(agents, departments));
}

export function buildOrgReportToMap(
  agents: OrgAgentModel[],
  departments: OrgDepartmentModel[],
): Map<number, number> {
  const ctx = buildCtx(agents, departments);
  const map = new Map<number, number>();
  for (const a of agents) map.set(a.id, resolveOne(a, ctx));
  return map;
}
