import { Crown } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
} from "@agentre-hub/agentre-ui";

import { AgentAvatar } from "../primitives";

import { DepartmentIconBadge, LeadBadge } from "./org-department-badges";
import { safeAgentColor, type OrgAgent, type OrgDepartment } from "./types";

/** 三栏里的「结构」栏：上级部门与负责人两个下拉。 */
export function OrgDetailDepartmentStructure({
  department,
  allDepartments,
  leadCandidates,
  parentId,
  leadAgentId,
  onPickParent,
  onPickLead,
}: {
  department: OrgDepartment;
  allDepartments: OrgDepartment[];
  leadCandidates: OrgAgent[];
  parentId: number;
  leadAgentId: number;
  onPickParent: (parentId: number) => void;
  onPickLead: (leadAgentId: number) => void;
}) {
  const { t } = useTranslation();
  const parentOptions = allDepartments.filter(
    (d) =>
      d.id !== department.id &&
      !isDescendant(d.id, department.id, allDepartments),
  );
  const selectedParent =
    parentId > 0
      ? (allDepartments.find((d) => d.id === parentId) ?? null)
      : null;
  const selectedLead = leadCandidates.find((a) => a.id === leadAgentId) ?? null;

  return (
    <section
      aria-label={t("org.detail.columns.structure")}
      data-slot="org-detail-col-structure"
      className="flex min-w-0 flex-col gap-6"
    >
      <section className="flex flex-col gap-2" data-slot="dept-section-parent">
        <h3 className="font-mono text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
          {t("org.department.parent")}
        </h3>
        <Select
          value={String(parentId)}
          onValueChange={(v) => onPickParent(Number(v))}
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

      <section className="flex flex-col gap-2" data-slot="dept-section-lead">
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
          onValueChange={(v) => onPickLead(Number(v))}
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
            {leadCandidates.map((a) => (
              <SelectItem key={a.id} value={String(a.id)}>
                {a.name}
                {a.description && ` · ${a.description}`}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </section>
    </section>
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
