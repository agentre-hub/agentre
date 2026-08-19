import * as React from "react";
import type { TFunction } from "i18next";
import { FolderPlus, Plus } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useLocation, useNavigate } from "react-router-dom";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";
import {
  consumeNewAgentDialogIntent,
  subscribeNewAgentIntent,
} from "@/stores/new-agent-intent-store";

import { agent_svc, department_svc } from "../../../wailsjs/go/models";
import {
  agentColorClassNames,
  agentColorOrder,
  type AgentColor,
} from "./types";
import { AgentreDialog } from "./app-dialog";
import { AgentAvatarPicker, IconPicker } from "./icon-picker";
import {
  type OrgAgent,
  type OrgDepartment,
  type OrgSelection,
} from "./org/types";
import { OrgDetailAgent } from "./org/org-detail-agent";
import { OrgDetailDepartment } from "./org/org-detail-department";
import { OrgIndex } from "./org/org-index";
import { useOrgData } from "./org/use-org-data";
import { useOrgIndexView } from "./org/use-org-index-view";

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
  const location = useLocation();

  React.useEffect(() => {
    const selection = (location.state as { orgSelection?: OrgSelection } | null)
      ?.orgSelection;
    if (selection?.kind && selection.id > 0) {
      view.setSelected(selection);
    }
  }, [location.state, view.setSelected]);

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

type RenderDetailArgs = {
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

function renderDetail(args: RenderDetailArgs): React.ReactNode {
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

type NewDeptProps = {
  open: boolean;
  departments: OrgDepartment[];
  agents: OrgAgent[];
  defaultParentId?: number;
  onSubmit: (req: {
    name: string;
    description: string;
    icon: string;
    accentColor: string;
    parentId: number;
  }) => Promise<void>;
  onClose: () => void;
};

function NewDepartmentDialog(props: NewDeptProps) {
  if (!props.open) return null;
  return <NewDepartmentDialogBody {...props} />;
}

function NewDepartmentDialogBody(props: NewDeptProps) {
  const { t } = useTranslation();
  const [name, setName] = React.useState("");
  const [icon, setIcon] = React.useState("hammer");
  const [accentColor, setAccentColor] = React.useState<AgentColor>("agent-2");
  const [parentId, setParentId] = React.useState<number>(
    props.defaultParentId ?? 0,
  );

  const canSubmit = name.trim().length > 0;

  async function handleSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!canSubmit) return;
    await props.onSubmit({
      name: name.trim(),
      description: "",
      icon,
      accentColor,
      parentId,
    });
  }

  return (
    <AgentreDialog
      open={props.open}
      onOpenChange={(o) => !o && props.onClose()}
      title={t("org.chart.actions.newDepartment")}
      description={t("org.chart.newDepartment.description")}
      bodyClassName="flex flex-col gap-4"
      onSubmit={handleSubmit}
      footer={
        <>
          <Button type="button" variant="outline" onClick={props.onClose}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" disabled={!canSubmit}>
            {t("common.create")}
          </Button>
        </>
      }
    >
      <label className="flex flex-col gap-1 text-xs">
        <span className="text-2xs text-muted-foreground">
          {t("org.department.name")}
        </span>
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          autoFocus
          aria-label={t("org.department.name")}
        />
      </label>
      <label className="flex flex-col gap-1 text-xs">
        <span className="text-2xs text-muted-foreground">
          {t("org.department.parent")}
        </span>
        <Select
          value={String(parentId)}
          onValueChange={(v) => setParentId(Number(v))}
        >
          <SelectTrigger aria-label={t("org.department.parent")}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="0">{t("org.department.topLevel")}</SelectItem>
            {props.departments.map((d) => (
              <SelectItem key={d.id} value={String(d.id)}>
                {d.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </label>
      <div className="flex flex-col gap-2">
        <span className="text-2xs text-muted-foreground">
          {t("org.department.icon")}
        </span>
        <IconPicker
          value={icon}
          onChange={setIcon}
          accentColor={accentColor}
          ariaLabel={t("org.chart.newDepartment.iconAria")}
        />
      </div>
      <div className="flex flex-col gap-2">
        <span className="text-2xs text-muted-foreground">
          {t("org.department.themeColor")}
        </span>
        <div
          className="grid grid-cols-5 gap-2"
          role="radiogroup"
          aria-label={t("org.department.themeColor")}
        >
          {agentColorOrder.map((c) => (
            <button
              key={c}
              type="button"
              role="radio"
              aria-checked={accentColor === c}
              aria-label={t("org.department.themeColorNamed", { color: c })}
              onClick={() => setAccentColor(c)}
              className={cn(
                "size-6 rounded-full ring-offset-2 transition-all",
                agentColorClassNames[c],
                accentColor === c && "size-7 ring-2 ring-primary",
              )}
            />
          ))}
        </div>
      </div>
    </AgentreDialog>
  );
}

type NewAgentProps = {
  open: boolean;
  fromIntent?: boolean;
  departments: OrgDepartment[];
  agents: OrgAgent[];
  backends: ReturnType<typeof useOrgData>["backends"];
  defaultDepartmentId?: number;
  onSubmit: (req: {
    name: string;
    description: string;
    avatarColor: string;
    avatarIcon: string;
    departmentId: number;
    parentAgentId: number;
    agentBackendId: number;
    prompt: string[];
    skills: { label: string; enabled: boolean }[];
  }) => Promise<void>;
  onClose: () => void;
};

function NewAgentDialog(props: NewAgentProps) {
  if (!props.open) return null;
  return <NewAgentDialogBody {...props} />;
}

function NewAgentDialogBody(props: NewAgentProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [name, setName] = React.useState("");
  const [description, setDescription] = React.useState("");
  const [avatarColor, setAvatarColor] = React.useState<AgentColor>("agent-1");
  const [avatarIcon, setAvatarIcon] = React.useState<string>("");
  const placementOptions = React.useMemo(
    () => [
      ...props.departments.map((d) => ({
        value: `department:${d.id}`,
        label: t("org.chart.newAgent.departmentOption", { name: d.name }),
      })),
      ...props.agents.map((a) => ({
        value: `agent:${a.id}`,
        label: t("org.chart.newAgent.agentOption", { name: a.name }),
      })),
    ],
    [props.departments, props.agents, t],
  );
  const initialPlacement = React.useMemo(() => {
    if (props.fromIntent) {
      const ceo = props.agents.find((a) => a.systemBadge === "DEFAULT");
      if (props.departments.length === 0 && ceo) {
        return `agent:${ceo.id}`;
      }
      return placementOptions[0]?.value ?? "";
    }
    const preset = props.defaultDepartmentId
      ? `department:${props.defaultDepartmentId}`
      : null;
    if (preset && placementOptions.some((opt) => opt.value === preset)) {
      return preset;
    }
    return placementOptions[0]?.value ?? "";
  }, [
    props.agents,
    props.defaultDepartmentId,
    props.departments.length,
    placementOptions,
    props.fromIntent,
  ]);
  const [placement, setPlacement] = React.useState<string>(initialPlacement);
  const [backendId, setBackendId] = React.useState<number>(
    props.fromIntent ? 0 : (props.backends[0]?.id ?? 0),
  );
  const parsedPlacement = parsePlacement(placement);

  const canSubmit =
    name.trim().length > 0 &&
    (parsedPlacement.departmentId > 0 || parsedPlacement.parentAgentId > 0);

  async function handleSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!canSubmit) return;
    await props.onSubmit({
      name: name.trim(),
      description: description.trim(),
      avatarColor,
      avatarIcon,
      departmentId: parsedPlacement.departmentId,
      parentAgentId: parsedPlacement.parentAgentId,
      agentBackendId: backendId,
      prompt: [],
      skills: [],
    });
  }

  return (
    <AgentreDialog
      open={props.open}
      onOpenChange={(o) => !o && props.onClose()}
      title={t("org.chart.actions.newAgent")}
      description={t("org.chart.newAgent.description")}
      bodyClassName="flex flex-col gap-4"
      onSubmit={handleSubmit}
      footer={
        <>
          <Button type="button" variant="outline" onClick={props.onClose}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" disabled={!canSubmit}>
            {t("common.create")}
          </Button>
        </>
      }
    >
      {props.fromIntent ? (
        <div className="flex items-center justify-between gap-3 rounded-md border border-primary/20 bg-primary-soft px-3 py-2 text-2xs text-muted-foreground">
          <span>{t("org.chart.newAgent.intentHint")}</span>
          <Button
            type="button"
            variant="link"
            size="sm"
            className="h-auto px-0 text-2xs"
            onClick={() =>
              navigate("/settings", {
                state: { settingsPage: "agent-backend" },
              })
            }
          >
            {t("org.chart.newAgent.intentSettings")}
          </Button>
        </div>
      ) : null}
      <label className="flex flex-col gap-1 text-xs">
        <span className="text-2xs text-muted-foreground">
          {t("org.department.name")}
        </span>
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          autoFocus
          aria-label={t("org.department.name")}
        />
      </label>
      <label className="flex flex-col gap-1.5 text-xs">
        <span className="text-2xs text-muted-foreground">
          {t("org.department.description")}{" "}
          <span className="opacity-60">
            {t("org.chart.newAgent.optionalSuffix")}
          </span>
        </span>
        <Input
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          aria-label={t("org.department.description")}
        />
      </label>
      <div className="flex flex-col gap-1.5">
        <span className="text-2xs text-muted-foreground">
          {t("org.chart.newAgent.avatar")}
        </span>
        <div className="flex items-center gap-3">
          <AgentAvatarPicker
            name={name || t("org.agent.fallbackName")}
            avatarColor={avatarColor}
            avatarIcon={avatarIcon}
            avatarDataUrl=""
            onChangeIcon={setAvatarIcon}
            allowUpload={false}
            triggerSize="lg"
          />
          <span className="font-mono text-2xs text-muted-foreground">
            {t("org.chart.newAgent.avatarHint")}
          </span>
        </div>
      </div>
      <div className="flex flex-col gap-1.5">
        <span className="text-2xs text-muted-foreground">
          {t("org.chart.newAgent.avatarColor")}
        </span>
        <div
          className="grid grid-cols-5 gap-2"
          role="radiogroup"
          aria-label={t("org.chart.newAgent.avatarColor")}
        >
          {agentColorOrder.map((c) => (
            <button
              key={c}
              type="button"
              role="radio"
              aria-checked={avatarColor === c}
              aria-label={t("org.chart.newAgent.avatarColorNamed", {
                color: c,
              })}
              onClick={() => setAvatarColor(c)}
              className={cn(
                "size-6 rounded-full ring-offset-2 transition-all",
                agentColorClassNames[c],
                avatarColor === c && "size-7 ring-2 ring-primary",
              )}
            />
          ))}
        </div>
      </div>
      <label className="flex flex-col gap-1 text-xs">
        <span className="text-2xs text-muted-foreground">
          {t("org.chart.newAgent.placement")}
        </span>
        <Select value={placement} onValueChange={setPlacement}>
          <SelectTrigger aria-label={t("org.chart.newAgent.placement")}>
            <SelectValue placeholder={t("org.chart.newAgent.placement")} />
          </SelectTrigger>
          <SelectContent>
            {placementOptions.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {option.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </label>
      <label className="flex flex-col gap-1 text-xs">
        <span className="text-2xs text-muted-foreground">
          {t("org.chart.newAgent.backend")}
        </span>
        <Select
          value={backendId > 0 ? String(backendId) : ""}
          onValueChange={(v) => setBackendId(Number(v))}
        >
          <SelectTrigger aria-label={t("org.chart.newAgent.backend")}>
            <SelectValue placeholder={t("org.chart.newAgent.backend")} />
          </SelectTrigger>
          <SelectContent>
            {props.backends.map((b) => (
              <SelectItem key={b.id} value={String(b.id)}>
                {b.name} · {b.type}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </label>
    </AgentreDialog>
  );
}

function parsePlacement(value: string): {
  departmentId: number;
  parentAgentId: number;
} {
  const [kind, rawId] = value.split(":");
  const id = Number(rawId);
  if (!Number.isFinite(id) || id <= 0) {
    return { departmentId: 0, parentAgentId: 0 };
  }
  if (kind === "department") {
    return { departmentId: id, parentAgentId: 0 };
  }
  if (kind === "agent") {
    return { departmentId: 0, parentAgentId: id };
  }
  return { departmentId: 0, parentAgentId: 0 };
}
