import type { department_svc } from "../../../../wailsjs/go/models";

import { iconForKey as iconForKeyShared } from "../icon-registry";

export type OrgAgentColor =
  | "agent-1"
  | "agent-2"
  | "agent-3"
  | "agent-4"
  | "agent-5"
  | "agent-6"
  | "agent-7"
  | "agent-8"
  | "agent-9"
  | "agent-10"
  | "agent-11"
  | "agent-12"
  | "agent-13"
  | "agent-14"
  | "agent-15"
  | "agent-16"
  | "neutral";

// 选中态的形状与共享包同源：索引与详情两端共用的呈现件都按它收 onSelect。
export type { OrgSelection } from "@agentre-ai/agentre-ui";

export const iconForKey = iconForKeyShared;

export type OrgDepartment = department_svc.DepartmentItem;
export type OrgAgent = department_svc.AgentItem;

export function isOrgAgentColor(value: string): value is OrgAgentColor {
  return (
    value === "agent-1" ||
    value === "agent-2" ||
    value === "agent-3" ||
    value === "agent-4" ||
    value === "agent-5" ||
    value === "agent-6" ||
    value === "agent-7" ||
    value === "agent-8" ||
    value === "agent-9" ||
    value === "agent-10" ||
    value === "agent-11" ||
    value === "agent-12" ||
    value === "agent-13" ||
    value === "agent-14" ||
    value === "agent-15" ||
    value === "agent-16" ||
    value === "neutral"
  );
}

export function safeAgentColor(value: string): OrgAgentColor {
  return isOrgAgentColor(value) ? value : "neutral";
}
