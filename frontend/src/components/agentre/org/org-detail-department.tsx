import * as React from "react";
import { useTranslation } from "react-i18next";

import type { department_svc } from "../../../../wailsjs/go/models";

import { safeAgentColor, type OrgAgent, type OrgDepartment } from "./types";
import { useAutoSave } from "./use-auto-save";
import { AutoSaveStatus } from "./auto-save-status";
import {
  DeleteDepartmentDialog,
  type DeleteDepartmentStrategy,
} from "./delete-department-dialog";
import { OrgDetailDepartmentHeader } from "./org-detail-department-header";
import { OrgDetailDepartmentMembers } from "./org-detail-department-members";
import { OrgDetailDepartmentStructure } from "./org-detail-department-structure";
import { deleteFailureOf, type DeleteFailure } from "./delete-failure";

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
  const { t } = useTranslation();
  const { values, patch, flush, wrap, status, pendingInvalid, retry } =
    useAutoSave({
      initial: {
        name: props.department.name,
        description: props.department.description,
        // 没有图标就是没有图标：旧代码在这里兜底成 "puzzle"，随后每次自动保存都把
        // 它写回库里，用户再也得不到「没设图标」这个状态。
        icon: props.department.icon || "",
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

  const [deleteFailure, setDeleteFailure] =
    React.useState<DeleteFailure | null>(null);
  const setDeleteDialogOpen = (open: boolean) => {
    setDeleteFailure(null);
    setDeletePromptOpen(open);
  };

  const handleConfirmDelete = React.useCallback(async () => {
    setDeleteFailure(null);
    try {
      await props.onDelete({ id: props.department.id, strategy });
    } catch (err) {
      // 没删掉：确认框与详情都留着、话说在确认框的页脚，用户可以再点一次或取消。
      setDeleteFailure(
        deleteFailureOf(err, t("org.chart.errors.deleteFailed")),
      );
      return;
    }
    props.onClose();
  }, [strategy, props, t]);

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
        identity={{
          name,
          description,
          onNameChange: (name) => patch({ name }),
          onNameBlur: flush,
          onDescriptionChange: (description) => patch({ description }),
          onDescriptionBlur: flush,
          mark: {
            iconKey: icon,
            color: accentColor,
            onPickIcon: (icon) => patch({ icon }, { immediate: true }),
            onPickColor: (color) =>
              patch(
                { accentColor: safeAgentColor(color) },
                { immediate: true },
              ),
          },
        }}
        onDelete={() => setDeletePromptOpen(true)}
        onClose={props.onClose}
      />

      {/* 身份整块搬进 header 后，往下只剩 结构 / 成员 两栏；栏宽同样按**容器**
          分档，每一栏 min-w-0，长部门名/长描述才不会顶着栏宽不收缩。 */}
      <div className="@container min-h-0 min-w-0 flex-1 overflow-y-auto px-5 py-5">
        <div
          className="grid min-w-0 grid-cols-1 items-start gap-6 @xl:grid-cols-2"
          data-slot="org-detail-columns"
        >
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
        failure={deleteFailure}
        onStrategyChange={setStrategy}
        onOpenChange={(o) => !o && setDeleteDialogOpen(false)}
        onCancel={() => setDeleteDialogOpen(false)}
        onConfirm={handleConfirmDelete}
      />
    </div>
  );
}
