// 右侧详情面板按选中节点渲染的那一块。

import * as React from "react";
import type { TFunction } from "i18next";

import {
  type OrgAgent,
  type OrgDepartment,
  type OrgSelection,
} from "../org/types";
import { OrgDetailAgent } from "../org/org-detail-agent";
import { OrgDetailDepartment } from "../org/org-detail-department";
import { useOrgData } from "../org/use-org-data";

export type RenderDetailArgs = {
  selected: OrgSelection;
  agentById: Record<number, OrgAgent>;
  departmentById: Record<number, OrgDepartment>;
  departments: OrgDepartment[];
  agents: OrgAgent[];
  backends: ReturnType<typeof useOrgData>["backends"];
  availableTools: ReturnType<typeof useOrgData>["availableTools"];
  onSelect: (sel: OrgSelection) => void;
  onClose: () => void;
  updateDepartment: ReturnType<typeof useOrgData>["updateDepartment"];
  moveDepartment: ReturnType<typeof useOrgData>["moveDepartment"];
  deleteDepartment: ReturnType<typeof useOrgData>["deleteDepartment"];
  updateAgent: ReturnType<typeof useOrgData>["updateAgent"];
  moveAgent: ReturnType<typeof useOrgData>["moveAgent"];
  deleteAgent: ReturnType<typeof useOrgData>["deleteAgent"];
  uploadAgentAvatar: ReturnType<typeof useOrgData>["uploadAgentAvatar"];
  deleteAgentAvatar: ReturnType<typeof useOrgData>["deleteAgentAvatar"];
  t: TFunction;
  onAddAgent: (deptId: number) => void;
  onAddSubDepartment: (deptId: number) => void;
};

export function renderDetail(args: RenderDetailArgs): React.ReactNode {
  const { selected } = args;
  if (selected?.kind === "department" && args.departmentById[selected.id]) {
    return (
      <OrgDetailDepartment
        key={`dept-${selected.id}`}
        department={args.departmentById[selected.id]}
        allDepartments={args.departments}
        allAgents={args.agents}
        leadCandidates={args.agents.filter(
          (a) => a.departmentId === selected.id && (a.parentAgentId ?? 0) === 0,
        )}
        onUpdate={(req) => args.updateDepartment(req)}
        onMove={(req) => args.moveDepartment(req)}
        onDelete={(req) => args.deleteDepartment(req)}
        onSelect={(sel) => args.onSelect(sel)}
        onClose={args.onClose}
        onAddAgent={() => args.onAddAgent(selected.id)}
        onAddSubDepartment={() => args.onAddSubDepartment(selected.id)}
      />
    );
  }
  if (selected?.kind === "agent" && args.agentById[selected.id]) {
    return (
      <OrgDetailAgent
        key={`agent-${selected.id}`}
        agent={args.agentById[selected.id]}
        departments={args.departments}
        agents={args.agents}
        backends={args.backends}
        availableTools={args.availableTools}
        isLeadOf={
          args.departments.find((d) => d.leadAgentId === selected.id) ?? null
        }
        onUpdate={(req) => args.updateAgent(req)}
        onMove={(req) => args.moveAgent(req)}
        onDelete={(req) => args.deleteAgent(req)}
        onUploadAvatar={(req) => args.uploadAgentAvatar(req)}
        onDeleteAvatar={(req) => args.deleteAgentAvatar(req)}
        onClose={args.onClose}
      />
    );
  }
  return (
    <div
      className="flex h-full min-w-0 items-center justify-center p-8 text-center text-sm text-muted-foreground"
      data-slot="org-detail-empty"
    >
      {args.t("org.chart.detail.empty")}
    </div>
  );
}
