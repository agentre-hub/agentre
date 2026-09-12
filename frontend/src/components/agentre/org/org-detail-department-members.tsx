import { ArrowUpRight, FolderPlus, Plus, UserPlus } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@agentre-hub/agentre-ui";

import { AgentAvatar } from "../primitives";

import { DepartmentIconBadge, LeadBadge } from "./org-department-badges";
import { safeAgentColor, type OrgAgent, type OrgDepartment } from "./types";

/** 三栏里的「成员」栏：直属 Agent 与直属子部门，外加两个新建入口。 */
export function OrgDetailDepartmentMembers({
  department,
  allDepartments,
  allAgents,
  leadAgentId,
  onSelect,
  onAddAgent,
  onAddSubDepartment,
}: {
  department: OrgDepartment;
  allDepartments: OrgDepartment[];
  allAgents: OrgAgent[];
  leadAgentId: number;
  onSelect: (
    sel: { kind: "agent"; id: number } | { kind: "department"; id: number },
  ) => void;
  onAddAgent?: () => void;
  onAddSubDepartment?: () => void;
}) {
  const { t } = useTranslation();
  const directAgents = allAgents.filter(
    (a) => a.departmentId === department.id,
  );
  const directDepts = allDepartments.filter(
    (d) => d.parentId === department.id,
  );

  return (
    <section
      aria-label={t("org.department.members")}
      data-slot="org-detail-col-members"
      className="flex min-w-0 flex-col gap-2"
    >
      <section className="flex flex-col gap-2" data-slot="dept-section-members">
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
                onClick={() => onSelect({ kind: "agent", id: a.id })}
                className="flex w-full items-center gap-2.5 rounded-md border border-border bg-card px-3 py-2 text-left text-sm hover:bg-accent"
                aria-label={t("org.department.viewAgent", { name: a.name })}
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
              onClick={() => onSelect({ kind: "department", id: d.id })}
              className="flex w-full items-center gap-2.5 rounded-md border border-border bg-card px-3 py-2 text-left text-sm hover:bg-accent"
              aria-label={t("org.department.viewDepartment", { name: d.name })}
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
          {(onAddAgent || onAddSubDepartment) && (
            <div
              role="group"
              aria-label={t("org.department.addGroup")}
              className="flex min-w-0 flex-col gap-1.5 rounded-md border border-dashed border-border bg-background/30 px-3 py-2"
            >
              {/* 说明与两个按钮分两行：栏宽 276px 时挤在一行里，说明只剩
                  22px，任何写法都要么截成两个字母要么顶破栏宽。 */}
              <span className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
                <Plus className="size-3 shrink-0" aria-hidden="true" />
                <span className="min-w-0">{t("org.department.addGroup")}</span>
              </span>
              <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                {onAddAgent && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="h-7 gap-1 px-2 text-2xs"
                    onClick={onAddAgent}
                  >
                    <UserPlus className="size-3" aria-hidden="true" />
                    {t("org.department.addAgent")}
                  </Button>
                )}
                {onAddSubDepartment && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="h-7 gap-1 px-2 text-2xs"
                    onClick={onAddSubDepartment}
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
