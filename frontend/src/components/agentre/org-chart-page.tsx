// 组织架构页:部门/agent 树 + 右侧详情 + 两个新建对话框。
//
// 两个对话框本体(org-chart/new-*-dialog.tsx)与详情渲染(org-chart/detail.tsx)
// 已从这个文件里拆出去 —— 它们原先与页面挤在同一个 783 行的文件里。

import * as React from "react";
import { FolderPlus, Plus } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useLocation } from "react-router-dom";

import { Button } from "@agentre-hub/agentre-ui";
import {
  consumeNewAgentDialogIntent,
  subscribeNewAgentIntent,
} from "@/stores/new-agent-intent-store";

import { agent_svc, department_svc } from "../../../wailsjs/go/models";
import {
  type OrgAgent,
  type OrgDepartment,
  type OrgSelection,
} from "./org/types";
import { OrgIndex } from "./org/org-index";
import { useOrgData } from "./org/use-org-data";
import { useOrgIndexView } from "./org/use-org-index-view";

import { renderDetail } from "./org-chart/detail";
import { NewAgentDialog } from "./org-chart/new-agent-dialog";
import { NewDepartmentDialog } from "./org-chart/new-department-dialog";

export function OrgChartPage() {
  const { t } = useTranslation();
  const {
    loading,
    error,
    departments,
    agents,
    backends,
    availableTools,
    moveAgent,
    moveDepartment,
    reorderAgents,
    updateDepartment,
    deleteDepartment,
    updateAgent,
    deleteAgent,
    uploadAgentAvatar,
    deleteAgentAvatar,
    createDepartment,
    createAgent,
  } = useOrgData();
  const view = useOrgIndexView();
  // setSelected 单独解出来再进依赖：写成 `view.setSelected` 时 exhaustive-deps 看到的
  // 是成员访问，只能要求整个 `view` 进依赖（而 view.selected 每次选中都变，那会让这个
  // effect 在每次选中后重跑一遍，把 location.state 里的旧选择又写回去）。
  const { setSelected } = view;
  const location = useLocation();

  React.useEffect(() => {
    const selection = (location.state as { orgSelection?: OrgSelection } | null)
      ?.orgSelection;
    if (selection?.kind && selection.id > 0) {
      setSelected(selection);
    }
  }, [location.state, setSelected]);

  const [newDeptOpen, setNewDeptOpen] = React.useState(false);
  const [newAgentOpen, setNewAgentOpen] = React.useState(false);
  const [newAgentFromIntent, setNewAgentFromIntent] = React.useState(false);
  // 当从 DeptEditDrawer 的 “+ 添加 Agent 或子部门” 触发时，预选目标部门。
  const [newAgentParentDeptId, setNewAgentParentDeptId] =
    React.useState<number>(0);
  const [newSubDeptParentId, setNewSubDeptParentId] = React.useState<number>(0);
  React.useEffect(() => {
    const openFromIntent = () => {
      if (!consumeNewAgentDialogIntent()) return;
      setNewAgentParentDeptId(0);
      setNewAgentFromIntent(true);
      setNewAgentOpen(true);
    };
    openFromIntent();
    return subscribeNewAgentIntent(openFromIntent);
  }, []);

  const agentById = React.useMemo(
    () =>
      Object.fromEntries(agents.map((a) => [a.id, a])) as Record<
        number,
        OrgAgent
      >,
    [agents],
  );
  const departmentById = React.useMemo(
    () =>
      Object.fromEntries(departments.map((d) => [d.id, d])) as Record<
        number,
        OrgDepartment
      >,
    [departments],
  );

  const summaryText = React.useMemo(() => {
    const top = departments.filter((d) => d.parentId === 0).length;
    return t("org.chart.summary", {
      agents: agents.length,
      departments: top,
      subDepartments: departments.length - top,
    });
  }, [departments, agents, t]);

  if (loading) {
    return (
      <div
        className="flex min-h-0 min-w-0 flex-1 items-center justify-center text-muted-foreground"
        data-slot="org-chart-loading"
      >
        {t("org.chart.loading")}
      </div>
    );
  }

  if (error) {
    return (
      <div
        className="flex min-h-0 min-w-0 flex-1 items-center justify-center text-destructive"
        data-slot="org-chart-error"
      >
        {error}
      </div>
    );
  }

  const detailContent = renderDetail({
    selected: view.selected,
    agentById,
    departmentById,
    departments,
    agents,
    backends,
    availableTools,
    onSelect: view.setSelected,
    onClose: () => view.setSelected(null),
    updateDepartment,
    moveDepartment,
    deleteDepartment,
    updateAgent,
    moveAgent,
    deleteAgent,
    uploadAgentAvatar,
    deleteAgentAvatar,
    t,
    onAddAgent: (deptId) => {
      setNewAgentFromIntent(false);
      setNewAgentParentDeptId(deptId);
      setNewAgentOpen(true);
    },
    onAddSubDepartment: (deptId) => {
      setNewSubDeptParentId(deptId);
      setNewDeptOpen(true);
    },
  });

  return (
    <main
      className="flex min-h-0 min-w-0 flex-1 flex-col"
      data-slot="org-chart-page"
    >
      <header
        className="flex h-[60px] shrink-0 items-center gap-3 border-b bg-background px-5"
        data-slot="org-header"
      >
        <div className="flex flex-col">
          <span className="text-base font-semibold">
            {t("org.chart.title")}
          </span>
          <span className="font-mono text-2xs text-muted-foreground">
            {summaryText}
          </span>
        </div>
        <div className="flex-1" />
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            setNewSubDeptParentId(0);
            setNewDeptOpen(true);
          }}
        >
          <FolderPlus className="size-3.5 mr-1" />
          {t("org.chart.actions.newDepartment")}
        </Button>
        <Button
          size="sm"
          disabled={departments.length === 0 && agents.length === 0}
          title={
            departments.length === 0 && agents.length === 0
              ? t("org.chart.empty.noMountNodes")
              : undefined
          }
          onClick={() => {
            setNewAgentFromIntent(false);
            setNewAgentParentDeptId(0);
            setNewAgentOpen(true);
          }}
        >
          <Plus className="size-3.5 mr-1" />
          {t("org.chart.actions.newAgent")}
        </Button>
      </header>

      {/* 索引收成左边固定宽的一列，详情吃掉主区：三栏详情要的是主区那么宽的容器，
          380px 的右抽屉里放不下（规格「详情出现在主区，分三栏」）。 */}
      <div className="relative flex min-h-0 min-w-0 flex-1 overflow-hidden">
        <div
          className="flex w-[300px] shrink-0 overflow-hidden border-r"
          data-slot="org-index-pane"
          data-testid="org-index-pane"
        >
          <OrgIndex
            departments={departments}
            agents={agents}
            backends={backends}
            selected={view.selected}
            onSelect={view.setSelected}
            onMoveAgent={(id, placement) => {
              void moveAgent({
                id,
                newDepartmentId: placement.departmentId,
                newParentAgentId: placement.parentAgentId,
                newSortOrder: 0,
              });
            }}
            onMoveDepartment={(id, parentId) => {
              void moveDepartment({
                id,
                newParentId: parentId,
                newSortOrder: 0,
              });
            }}
            onReorderAgent={(departmentId, parentAgentId, orderedIds) => {
              void reorderAgents(departmentId, parentAgentId, orderedIds);
            }}
            onCreateDepartment={() => {
              setNewSubDeptParentId(0);
              setNewDeptOpen(true);
            }}
            onCreateAgent={
              departments.length > 0 || agents.length > 0
                ? () => {
                    setNewAgentFromIntent(false);
                    setNewAgentParentDeptId(0);
                    setNewAgentOpen(true);
                  }
                : undefined
            }
          />
        </div>

        <div
          className="relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden"
          data-slot="org-detail-panel"
          data-testid="org-detail-main"
        >
          {detailContent}
        </div>
      </div>

      <NewDepartmentDialog
        open={newDeptOpen}
        departments={departments}
        agents={agents}
        defaultParentId={newSubDeptParentId}
        onSubmit={async (req) => {
          await createDepartment(
            department_svc.CreateDepartmentRequest.createFrom(req),
          );
          setNewDeptOpen(false);
        }}
        onClose={() => setNewDeptOpen(false)}
      />
      <NewAgentDialog
        open={newAgentOpen}
        departments={departments}
        agents={agents}
        backends={backends}
        defaultDepartmentId={newAgentParentDeptId}
        onSubmit={async (req) => {
          await createAgent(agent_svc.CreateAgentRequest.createFrom(req));
          setNewAgentOpen(false);
          setNewAgentFromIntent(false);
        }}
        fromIntent={newAgentFromIntent}
        onClose={() => {
          setNewAgentOpen(false);
          setNewAgentFromIntent(false);
        }}
      />
    </main>
  );
}
