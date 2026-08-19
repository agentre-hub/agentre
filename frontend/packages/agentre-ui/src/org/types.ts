/**
 * 组织面（部门 + Agent）的**结构化契约**。
 *
 * 桌面端的这两张表来自 Wails 生成的 `department_svc.DepartmentItem` /
 * `AgentItem`，agentre-server 那侧来自 REST —— 两边字段名同源（同一套同步对象），
 * 但类型来源完全不同。所以包里只声明**呈现件真正读到的那些字段**，并且一律可选：
 * 宿主把自己那份更宽的类型直接传进来即可，不需要在调用处映射一层。
 *
 * 反过来说，这里多一个字段就是多一条对宿主的要求。加字段前先问：这一维是**画出来
 * 的**，还是宿主自己算的？后者应该留在宿主。
 */

/** 行尾那枚后端徽标读的就这一个字段（桌面端是 `AgentItem.backend` 的摘要）。 */
export type OrgAgentBackendSummary = {
  name: string;
};

export type OrgAgentModel = {
  id: number;
  name: string;
  description?: string;
  avatarColor?: string;
  avatarIcon?: string;
  avatarDataUrl?: string;
  /** `"DEFAULT"` = 系统 Agent（唯一合法的 dept == 0 且 parent == 0）。 */
  systemBadge?: string;
  departmentId?: number;
  parentAgentId?: number;
  parentAgentName?: string;
  agentBackendId?: number;
  sortOrder?: number;
  backend?: OrgAgentBackendSummary;
};

export type OrgDepartmentModel = {
  id: number;
  name: string;
  description?: string;
  icon?: string;
  accentColor?: string;
  parentId?: number;
  leadAgentId?: number;
  leadAgentName?: string;
  sortOrder?: number;
  memberCount?: number;
};

/** 执行目标那一档指向的后端；行上要画机器名与类型。 */
export type OrgBackendModel = {
  id: number;
  type: string;
  name: string;
  /** 空 = 本机；非空 = 远端设备（未解析出名字时不把这串指纹当机器名显示）。 */
  deviceId?: string;
  deviceName?: string;
};

/** 索引里选中的是谁；`null` = 没选。 */
export type OrgSelection =
  | { kind: "agent"; id: number }
  | { kind: "department"; id: number }
  | null;

/** 系统 Agent 的徽标值。判据只有这一处，两端同源。 */
export const ORG_SYSTEM_BADGE = "DEFAULT";

export function isOrgSystemAgent(agent: OrgAgentModel): boolean {
  return agent.systemBadge === ORG_SYSTEM_BADGE;
}
