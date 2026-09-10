import type { department_svc } from "../../../../wailsjs/go/models";

import { agentColorOrder, type AgentColor } from "@agentre-hub/agentre-ui";

import { iconForKey as iconForKeyShared } from "../icon-registry";

// 颜色 token 词汇表只有一份：包的 `AgentColor` 与 tokens.css 同源（`agentColorOrder`
// 之外还有中性色 `neutral`）。这里只留一个本地别名给组织面的既有调用点。
export type OrgAgentColor = AgentColor;

// 选中态的形状与共享包同源：索引与详情两端共用的呈现件都按它收 onSelect。
export type { OrgSelection } from "@agentre-hub/agentre-ui";

export const iconForKey = iconForKeyShared;

export type OrgDepartment = department_svc.DepartmentItem;
export type OrgAgent = department_svc.AgentItem;

// 合法 token 的全集 = 调色板顺序 + 中性色（后者不进调色板，但是合法的落位值）。
const ORG_AGENT_COLORS = new Set<string>([...agentColorOrder, "neutral"]);

export function isOrgAgentColor(value: string): value is OrgAgentColor {
  return ORG_AGENT_COLORS.has(value);
}

export function safeAgentColor(value: string): OrgAgentColor {
  return isOrgAgentColor(value) ? value : "neutral";
}
