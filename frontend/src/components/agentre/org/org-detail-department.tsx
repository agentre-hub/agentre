import * as React from "react";
import { useTranslation } from "react-i18next";
import {
  AlertTriangle,
  ArrowUpRight,
  ChevronRight,
  CornerDownRight,
  Crown,
  FolderPlus,
  Plus,
  Trash2,
  UserPlus,
  X,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

import {
  agentColorClassNames,
  agentColorOrder,
  agentTextColorClassNames,
  type AgentColor,
} from "../types";
import type { department_svc } from "../../../../wailsjs/go/models";

import { IconPicker } from "../icon-picker";
import { AgentAvatar } from "../primitives";

import {
  iconForKey,
  safeAgentColor,
  type OrgAgent,
  type OrgDepartment,
} from "./types";
import { useAutoSave } from "./use-auto-save";
import { AutoSaveStatus } from "./auto-save-status";

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
  const [strategy, setStrategy] = React.useState<"reparent" | "cascade">(
    "reparent",
  );

  const handleConfirmDelete = React.useCallback(async () => {
    await props.onDelete({ id: props.department.id, strategy });
    props.onClose();
  }, [strategy, props]);

  const path = buildPath(props.department, props.allDepartments);
  const parentOptions = props.allDepartments.filter(
    (d) =>
      d.id !== props.department.id &&
      !isDescendant(d.id, props.department.id, props.allDepartments),
  );
  const directAgents = props.allAgents.filter(
    (a) => a.departmentId === props.department.id,
  );
  const directDepts = props.allDepartments.filter(
    (d) => d.parentId === props.department.id,
  );
  const selectedParent =
    parentId > 0
      ? (props.allDepartments.find((d) => d.id === parentId) ?? null)
      : null;
  const selectedLead =
    props.leadCandidates.find((a) => a.id === leadAgentId) ?? null;
  const iconNode = React.createElement(iconForKey(icon), {
    className: "size-5 text-agent-foreground",
    "aria-hidden": true,
  });

  return (
    <div
      data-slot="org-detail-department"
      className="flex h-full min-w-0 flex-col bg-card"
    >
      <header className="space-y-3 border-b border-border bg-card px-5 py-4">
        <div className="flex items-start gap-3">
          <span
            className={cn(
              "inline-flex size-10 shrink-0 items-center justify-center rounded-lg",
              agentColorClassNames[accentColor],
            )}
          >
            {iconNode}
          </span>
          <div className="flex-1 min-w-0">
            <div className="font-mono text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
              {t("org.department.editEyebrow")}
            </div>
            <div className="truncate text-base font-semibold">
              {props.department.name}
            </div>
          </div>
          <div className="flex shrink-0 gap-1">
            <Button
              variant="outline"
              size="icon"
              className="size-8"
              aria-label={t("org.department.deleteDepartment")}
              onClick={() => setDeletePromptOpen(true)}
            >
              <Trash2 className="size-4 text-destructive" />
            </Button>
            <Button
              variant="outline"
              size="icon"
              className="size-8"
              aria-label={t("common.close")}
              onClick={props.onClose}
            >
              <X className="size-4" />
            </Button>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-1.5 text-2xs text-muted-foreground">
          <CornerDownRight className="size-3" aria-hidden="true" />
          <span>{t("org.department.path")}</span>
          {path.map((node, i) => (
            <React.Fragment key={node.id}>
              {i > 0 && (
                <ChevronRight
                  className="size-3 text-muted-foreground"
                  aria-hidden="true"
                />
              )}
              <span
                className={cn(
                  "inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 font-mono text-2xs",
                  i === path.length - 1
                    ? "border border-primary bg-primary-soft text-primary-text"
                    : "bg-secondary text-foreground",
                )}
              >
                {node.name}
              </span>
            </React.Fragment>
          ))}
        </div>
      </header>

      {/* 与 Agent 详情同一套三栏：身份 / 结构 / 成员。栏宽按**容器**分档（详情是
          主区里的一块，视口宽不等于它宽），每一栏 min-w-0，长部门名/长描述才不会
          顶着栏宽不收缩、把溢出交给外层滚动容器藏起来。 */}
      <div className="@container min-h-0 min-w-0 flex-1 overflow-y-auto px-5 py-5">
        <div
          className="grid min-w-0 grid-cols-1 items-start gap-6 @xl:grid-cols-2 @3xl:grid-cols-3"
          data-slot="org-detail-columns"
        >
          <section
            aria-label={t("org.detail.columns.identity")}
            data-slot="org-detail-col-identity"
            className="min-w-0 space-y-4"
          >
            <section className="space-y-4" data-slot="dept-section-basic">
              <h3 className="font-mono text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
                {t("org.department.basicInfo")}
              </h3>
              <div className="space-y-1.5">
                <label className="block text-2xs text-muted-foreground">
                  {t("org.department.name")}
                </label>
                <Input
                  value={name}
                  onChange={(e) => patch({ name: e.target.value })}
                  onBlur={flush}
                  aria-label={t("org.department.name")}
                />
              </div>
              <div className="space-y-1.5">
                <div className="flex items-center justify-between gap-2">
                  <label className="text-2xs text-muted-foreground">
                    {t("org.department.description")}
                  </label>
                  <span className="font-mono text-2xs text-muted-foreground">
                    {t("common.optional")}
                  </span>
                </div>
                <Input
                  value={description}
                  onChange={(e) => patch({ description: e.target.value })}
                  onBlur={flush}
                  aria-label={t("org.department.description")}
                />
              </div>
              {/* 图标与主题色不再并排：三栏时栏宽实测 276px，150px 的图标块 + 5 列
                  色板要 152px，并排放不下（色板被顶出栏宽，外层滚动容器把它藏起来
                  而不报错）。竖着摞，两块各自吃满栏宽。 */}
              <div
                className="flex min-w-0 flex-col gap-2.5"
                data-slot="dept-icon-theme-row"
              >
                <div className="flex min-w-0 flex-col gap-1.5">
                  <label className="block text-2xs text-muted-foreground">
                    {t("org.department.icon")}
                  </label>
                  <IconPicker
                    value={icon}
                    onChange={(v) => patch({ icon: v }, { immediate: true })}
                    accentColor={accentColor}
                    ariaLabel={t("org.department.icon")}
                    className="h-[38px] px-2.5 py-1.5"
                  />
                </div>
                <div className="flex min-w-0 flex-1 flex-col gap-1.5">
                  <label className="block text-2xs text-muted-foreground">
                    {t("org.department.themeColor")}
                  </label>
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
                        aria-label={t("org.department.themeColorNamed", {
                          color: c,
                        })}
                        onClick={() =>
                          patch({ accentColor: c }, { immediate: true })
                        }
                        className={cn(
                          "size-6 rounded-full ring-offset-2 transition-all",
                          agentColorClassNames[c],
                          accentColor === c && "size-7 ring-2 ring-primary",
                        )}
                      />
                    ))}
                  </div>
                </div>
              </div>
            </section>
          </section>

          <section
            aria-label={t("org.detail.columns.structure")}
            data-slot="org-detail-col-structure"
            className="flex min-w-0 flex-col gap-6"
          >
            <section
              className="flex flex-col gap-2"
              data-slot="dept-section-parent"
            >
              <h3 className="font-mono text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
                {t("org.department.parent")}
              </h3>
              <Select
                value={String(parentId)}
                onValueChange={(v) => {
                  const p = Number(v);
                  setParentId(p);
                  void wrap(() =>
                    props.onMove({
                      id: props.department.id,
                      newParentId: p,
                      newSortOrder: 0,
                    }),
                  );
                }}
              >
                <SelectTrigger
                  aria-label={t("org.department.parent")}
                  className="h-auto py-2"
                >
                  {selectedParent ? (
                    <DepartmentSelectPreview department={selectedParent} />
                  ) : (
                    <div className="flex min-w-0 items-center gap-2">
                      <span
                        className="inline-flex size-[22px] shrink-0 items-center justify-center rounded bg-primary text-primary-foreground"
                        aria-hidden="true"
                      >
                        <Crown className="size-3" />
                      </span>
                      <span className="truncate text-sm font-medium">
                        {t("org.department.topLevel")}
                      </span>
                    </div>
                  )}
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="0">
                    <span
                      className="inline-flex size-[22px] shrink-0 items-center justify-center rounded bg-primary text-primary-foreground"
                      aria-hidden="true"
                    >
                      <Crown className="size-3" />
                    </span>
                    <span>{t("org.department.topLevel")}</span>
                  </SelectItem>
                  {parentOptions.map((d) => (
                    <SelectItem key={d.id} value={String(d.id)}>
                      <DepartmentIconBadge
                        icon={d.icon}
                        accentColor={d.accentColor}
                        className="size-[22px] rounded"
                        iconClassName="size-3"
                      />
                      <span>{d.name}</span>
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </section>

            <section
              className="flex flex-col gap-2"
              data-slot="dept-section-lead"
            >
              <div className="flex items-center justify-between">
                <h3 className="font-mono text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
                  {t("org.department.leader")}
                </h3>
                <span className="font-mono text-2xs text-muted-foreground">
                  {t("org.department.leadHint")}
                </span>
              </div>
              <Select
                value={String(leadAgentId)}
                onValueChange={(v) =>
                  patch({ leadAgentId: Number(v) }, { immediate: true })
                }
              >
                <SelectTrigger
                  aria-label={t("org.department.leader")}
                  className="h-auto py-2"
                >
                  {selectedLead ? (
                    <LeaderSelectPreview agent={selectedLead} />
                  ) : (
                    <span className="text-xs text-muted-foreground">
                      {t("common.unassigned")}
                    </span>
                  )}
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="0">{t("common.unassigned")}</SelectItem>
                  {props.leadCandidates.map((a) => (
                    <SelectItem key={a.id} value={String(a.id)}>
                      {a.name}
                      {a.description && ` · ${a.description}`}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </section>
          </section>

          <section
            aria-label={t("org.department.members")}
            data-slot="org-detail-col-members"
            className="flex min-w-0 flex-col gap-2"
          >
            <section
              className="flex flex-col gap-2"
              data-slot="dept-section-members"
            >
              <div className="flex items-center justify-between">
                <h3 className="font-mono text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
                  {t("org.department.members")}
                </h3>
                <span className="font-mono text-2xs text-muted-foreground">
                  {t("org.department.memberSummary", {
                    agents: directAgents.length,
                    departments: directDepts.length,
                  })}
                </span>
              </div>
              <div className="flex flex-col gap-1.5">
                {directAgents.map((a) => {
                  const agentColor = safeAgentColor(a.avatarColor);
                  const isLead = a.id === leadAgentId;
                  return (
                    <button
                      key={`a-${a.id}`}
                      type="button"
                      onClick={() =>
                        props.onSelect({ kind: "agent", id: a.id })
                      }
                      className="flex w-full items-center gap-2.5 rounded-md border border-border bg-card px-3 py-2 text-left text-sm hover:bg-accent"
                      aria-label={t("org.department.viewAgent", {
                        name: a.name,
                      })}
                    >
                      <AgentAvatar
                        name={a.name}
                        color={agentColor}
                        avatarDataUrl={a.avatarDataUrl}
                        avatarIcon={a.avatarIcon}
                        className="size-7 rounded-md text-xs"
                      />
                      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                        <span className="flex min-w-0 items-center gap-1.5">
                          <span className="truncate text-xs font-semibold text-foreground">
                            {a.name}
                          </span>
                          {isLead && <LeadBadge color={agentColor} compact />}
                        </span>
                        <span className="truncate font-mono text-2xs text-muted-foreground">
                          {agentMemberDescription(a)}
                        </span>
                      </span>
                      <ArrowUpRight
                        className="size-3 shrink-0 text-muted-foreground"
                        aria-hidden="true"
                      />
                    </button>
                  );
                })}
                {directDepts.map((d) => (
                  <button
                    key={`d-${d.id}`}
                    type="button"
                    onClick={() =>
                      props.onSelect({ kind: "department", id: d.id })
                    }
                    className="flex w-full items-center gap-2.5 rounded-md border border-border bg-card px-3 py-2 text-left text-sm hover:bg-accent"
                    aria-label={t("org.department.viewDepartment", {
                      name: d.name,
                    })}
                  >
                    <DepartmentIconBadge
                      icon={d.icon}
                      accentColor={d.accentColor}
                      className="size-7 rounded-md"
                      iconClassName="size-3.5"
                    />
                    <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                      <span className="truncate text-xs font-semibold text-foreground">
                        {d.name}
                      </span>
                      <span className="truncate font-mono text-2xs text-muted-foreground">
                        {departmentMemberDescription(d, t)}
                      </span>
                    </span>
                    <ArrowUpRight
                      className="size-3 shrink-0 text-muted-foreground"
                      aria-hidden="true"
                    />
                  </button>
                ))}
                {directAgents.length === 0 && directDepts.length === 0 && (
                  <div className="rounded-md border border-dashed border-border px-3 py-2 text-center text-2xs text-muted-foreground">
                    {t("org.department.noDirectMembers")}
                  </div>
                )}
                {(props.onAddAgent || props.onAddSubDepartment) && (
                  <div
                    role="group"
                    aria-label={t("org.department.addGroup")}
                    className="flex min-w-0 flex-col gap-1.5 rounded-md border border-dashed border-border bg-background/30 px-3 py-2"
                  >
                    {/* 说明与两个按钮分两行：栏宽 276px 时挤在一行里，说明只剩
                        22px，任何写法都要么截成两个字母要么顶破栏宽。 */}
                    <span className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
                      <Plus className="size-3 shrink-0" aria-hidden="true" />
                      <span className="min-w-0">
                        {t("org.department.addGroup")}
                      </span>
                    </span>
                    <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                      {props.onAddAgent && (
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          className="h-7 gap-1 px-2 text-2xs"
                          onClick={props.onAddAgent}
                        >
                          <UserPlus className="size-3" aria-hidden="true" />
                          {t("org.department.addAgent")}
                        </Button>
                      )}
                      {props.onAddSubDepartment && (
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          className="h-7 gap-1 px-2 text-2xs"
                          onClick={props.onAddSubDepartment}
                        >
                          <FolderPlus className="size-3" aria-hidden="true" />
                          {t("org.department.subDepartment")}
                        </Button>
                      )}
                    </div>
                  </div>
                )}
              </div>
            </section>
          </section>
        </div>
      </div>

      <AutoSaveStatus
        status={status}
        pendingInvalid={pendingInvalid}
        onRetry={retry}
      />

      <Dialog
        open={deletePromptOpen}
        onOpenChange={(o) => !o && setDeletePromptOpen(false)}
      >
        {deletePromptOpen && (
          <DialogContent className="max-w-md">
            <DialogHeader>
              <DialogTitle className="flex items-center gap-2">
                <AlertTriangle
                  className="size-[18px] text-destructive"
                  aria-hidden="true"
                />
                <span>
                  {t("org.department.deleteDialog.title", {
                    name: props.department.name,
                  })}
                </span>
              </DialogTitle>
              <DialogDescription>
                {t("org.department.deleteDialog.description")}
              </DialogDescription>
            </DialogHeader>
            <DialogBody className="space-y-2.5">
              <h4 className="font-mono text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
                {t("org.department.deleteDialog.strategyTitle")}
              </h4>
              <label
                className={cn(
                  "flex cursor-pointer items-start gap-2.5 rounded-md border bg-card px-3 py-2.5 transition-colors",
                  strategy === "reparent"
                    ? "border-primary ring-1 ring-primary"
                    : "border-border",
                )}
              >
                <input
                  type="radio"
                  name="strategy"
                  value="reparent"
                  checked={strategy === "reparent"}
                  onChange={() => setStrategy("reparent")}
                  className="mt-0.5"
                />
                <div className="flex-1">
                  <div className="text-sm font-semibold text-foreground">
                    {t("org.department.deleteDialog.reparentTitle")}
                  </div>
                  <div className="text-2xs text-muted-foreground">
                    {t("org.department.deleteDialog.reparentDescription")}
                  </div>
                </div>
              </label>
              <label
                className={cn(
                  "flex cursor-pointer items-start gap-2.5 rounded-md border bg-card px-3 py-2.5 transition-colors",
                  strategy === "cascade"
                    ? "border-primary ring-1 ring-primary"
                    : "border-border",
                )}
              >
                <input
                  type="radio"
                  name="strategy"
                  value="cascade"
                  checked={strategy === "cascade"}
                  onChange={() => setStrategy("cascade")}
                  aria-label={t("org.department.deleteDialog.cascadeTitle")}
                  className="mt-0.5"
                />
                <div className="flex-1">
                  <div className="text-sm font-semibold text-foreground">
                    {t("org.department.deleteDialog.cascadeTitle")}
                  </div>
                  <div className="text-2xs text-muted-foreground">
                    {t("org.department.deleteDialog.cascadeDescription")}
                  </div>
                </div>
              </label>
            </DialogBody>
            <DialogFooter>
              <span className="mr-auto font-mono text-2xs text-muted-foreground">
                {t("org.department.deleteDialog.irreversible")}
              </span>
              <Button
                variant="outline"
                size="sm"
                onClick={() => setDeletePromptOpen(false)}
              >
                {t("common.cancel")}
              </Button>
              <Button
                variant="destructive"
                size="sm"
                onClick={handleConfirmDelete}
              >
                <Trash2 className="size-3.5" />
                {t("org.department.deleteDialog.confirm")}
              </Button>
            </DialogFooter>
          </DialogContent>
        )}
      </Dialog>
    </div>
  );
}

function DepartmentSelectPreview({
  department,
}: {
  department: OrgDepartment;
}) {
  return (
    <div className="flex min-w-0 items-center gap-2">
      <DepartmentIconBadge
        icon={department.icon}
        accentColor={department.accentColor}
        className="size-[22px] rounded"
        iconClassName="size-3"
      />
      <span className="truncate text-sm font-medium">{department.name}</span>
    </div>
  );
}

function DepartmentIconBadge({
  accentColor,
  className,
  icon,
  iconClassName,
}: {
  accentColor: string;
  className?: string;
  icon: string;
  iconClassName?: string;
}) {
  const Icon = iconForKey(icon);
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center justify-center text-agent-foreground",
        agentColorClassNames[safeAgentColor(accentColor)],
        className,
      )}
      aria-hidden="true"
    >
      {React.createElement(Icon, {
        className: cn("size-3.5", iconClassName),
      })}
    </span>
  );
}

function LeaderSelectPreview({ agent }: { agent: OrgAgent }) {
  const color = safeAgentColor(agent.avatarColor);
  return (
    <div className="flex w-full min-w-0 items-center gap-2.5">
      <AgentAvatar
        name={agent.name}
        color={color}
        avatarDataUrl={agent.avatarDataUrl}
        avatarIcon={agent.avatarIcon}
        className="size-6 rounded text-2xs"
      />
      {/* 列向 flex + items-start 时子项按内容定宽，truncate 一个人管不住它：
          一句长简介照样把触发器撑破，所以两行都要 max-w-full。 */}
      <span className="flex min-w-0 flex-1 flex-col items-start gap-0 text-left">
        <span className="max-w-full truncate text-sm font-semibold text-foreground">
          {agent.name}
        </span>
        {agent.description && (
          <span className="max-w-full truncate font-mono text-2xs text-muted-foreground">
            {agent.description}
          </span>
        )}
      </span>
      <LeadBadge color={color} />
    </div>
  );
}

function LeadBadge({
  color,
  compact = false,
}: {
  color: AgentColor;
  compact?: boolean;
}) {
  const { t } = useTranslation();

  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center rounded-sm bg-secondary font-mono font-semibold",
        agentTextColorClassNames[color],
        compact ? "gap-1 px-1 py-0.5 text-2xs" : "gap-1 px-1.5 py-0.5 text-2xs",
      )}
    >
      <Crown className={compact ? "size-2" : "size-2.5"} aria-hidden="true" />
      {t("org.department.leadBadge")}
    </span>
  );
}

function agentMemberDescription(agent: OrgAgent): string {
  return agent.description || "";
}

function departmentMemberDescription(
  department: OrgDepartment,
  t: (key: string, options?: Record<string, unknown>) => string,
): string {
  return t("org.department.departmentMemberCount", {
    count: department.memberCount,
  });
}

function buildPath(dept: OrgDepartment, all: OrgDepartment[]): OrgDepartment[] {
  const byId = new Map(all.map((d) => [d.id, d]));
  const out: OrgDepartment[] = [dept];
  let cur: OrgDepartment | undefined = byId.get(dept.parentId);
  while (cur) {
    out.unshift(cur);
    cur = byId.get(cur.parentId);
  }
  return out;
}

function isDescendant(
  candidateId: number,
  ancestorId: number,
  all: OrgDepartment[],
): boolean {
  const byId = new Map(all.map((d) => [d.id, d]));
  let cur: number | undefined = candidateId;
  while (cur && cur > 0) {
    if (cur === ancestorId) return true;
    cur = byId.get(cur)?.parentId;
  }
  return false;
}
