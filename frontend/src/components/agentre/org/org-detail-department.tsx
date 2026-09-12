import * as React from "react";

import type { department_svc } from "../../../../wailsjs/go/models";

import { safeAgentColor, type OrgAgent, type OrgDepartment } from "./types";
import { useAutoSave } from "./use-auto-save";
import { AutoSaveStatus } from "./auto-save-status";
import {
  DeleteDepartmentDialog,
  type DeleteDepartmentStrategy,
} from "./delete-department-dialog";
import { OrgDetailDepartmentHeader } from "./org-detail-department-header";
import { OrgDetailDepartmentIdentity } from "./org-detail-department-identity";
import { OrgDetailDepartmentMembers } from "./org-detail-department-members";
import { OrgDetailDepartmentStructure } from "./org-detail-department-structure";

type Props = {
  department: OrgDepartment;
  allDepartments: OrgDepartment[];
  allAgents: OrgAgent[];
  leadCandidates: OrgAgent[];
  onUpdate: (req: department_svc.UpdateDepartmentRequest) => Promise<unknown>;
  onMove: (req: department_svc.MoveDepartmentRequest) => Promise<unknown>;
  onDelete: (req: department_svc.DeleteDepartmentRequest) => Promise<unknown>;
  onSelect: (
    sel: { kind: "agent"; id: number } | { kind: "department"; id: number },
  ) => void;
  onClose: () => void;
  onAddAgent?: () => void;
  onAddSubDepartment?: () => void;
};

export function OrgDetailDepartment(props: Props) {
  const { values, patch, flush, wrap, status, pendingInvalid, retry } =
    useAutoSave({
      initial: {
        name: props.department.name,
        description: props.department.description,
        icon: props.department.icon || "puzzle",
        accentColor: safeAgentColor(props.department.accentColor),
        leadAgentId: props.department.leadAgentId,
      },
      isValid: (v) => v.name.trim() !== "",
      save: (v) =>
        props.onUpdate({
          id: props.department.id,
          name: v.name,
          description: v.description,
          icon: v.icon,
          accentColor: v.accentColor,
          leadAgentId: v.leadAgentId,
        }),
    });
  const { name, description, icon, accentColor, leadAgentId } = values;
  const [parentId, setParentId] = React.useState<number>(
    props.department.parentId,
  );
  const [deletePromptOpen, setDeletePromptOpen] = React.useState(false);
  const [strategy, setStrategy] =
    React.useState<DeleteDepartmentStrategy>("reparent");

  const handleConfirmDelete = React.useCallback(async () => {
    await props.onDelete({ id: props.department.id, strategy });
    props.onClose();
  }, [strategy, props]);

  const handlePickParent = (p: number) => {
    setParentId(p);
    void wrap(() =>
      props.onMove({
        id: props.department.id,
        newParentId: p,
        newSortOrder: 0,
      }),
    );
  };

  return (
    <div
      data-slot="org-detail-department"
      className="flex h-full min-w-0 flex-col bg-card"
    >
      <OrgDetailDepartmentHeader
        department={props.department}
        allDepartments={props.allDepartments}
        icon={icon}
        accentColor={accentColor}
        onDelete={() => setDeletePromptOpen(true)}
        onClose={props.onClose}
      />

      {/* 与 Agent 详情同一套三栏：身份 / 结构 / 成员。栏宽按**容器**分档（详情是
          主区里的一块，视口宽不等于它宽），每一栏 min-w-0，长部门名/长描述才不会
          顶着栏宽不收缩、把溢出交给外层滚动容器藏起来。 */}
      <div className="@container min-h-0 min-w-0 flex-1 overflow-y-auto px-5 py-5">
        <div
          className="grid min-w-0 grid-cols-1 items-start gap-6 @xl:grid-cols-2 @3xl:grid-cols-3"
          data-slot="org-detail-columns"
        >
          <OrgDetailDepartmentIdentity
            name={name}
            description={description}
            icon={icon}
            accentColor={accentColor}
            patch={patch}
            flush={flush}
          />

          <OrgDetailDepartmentStructure
            department={props.department}
            allDepartments={props.allDepartments}
            leadCandidates={props.leadCandidates}
            parentId={parentId}
            leadAgentId={leadAgentId}
            onPickParent={handlePickParent}
            onPickLead={(id) => patch({ leadAgentId: id }, { immediate: true })}
          />

          <OrgDetailDepartmentMembers
            department={props.department}
            allDepartments={props.allDepartments}
            allAgents={props.allAgents}
            leadAgentId={leadAgentId}
            onSelect={props.onSelect}
            onAddAgent={props.onAddAgent}
            onAddSubDepartment={props.onAddSubDepartment}
          />
        </div>
      </div>

      <AutoSaveStatus
        status={status}
        pendingInvalid={pendingInvalid}
        onRetry={retry}
      />

      <DeleteDepartmentDialog
        open={deletePromptOpen}
        departmentName={props.department.name}
        strategy={strategy}
        onStrategyChange={setStrategy}
        onOpenChange={(o) => !o && setDeletePromptOpen(false)}
        onCancel={() => setDeletePromptOpen(false)}
        onConfirm={handleConfirmDelete}
      />
    </div>
  );
}
