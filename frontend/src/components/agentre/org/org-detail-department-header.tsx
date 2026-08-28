import * as React from "react";
import { ChevronRight, CornerDownRight, Trash2, X } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

import { agentColorClassNames, type AgentColor } from "../types";

import { iconForKey, type OrgDepartment } from "./types";

/** 部门详情的顶栏：字形 / 名字 / 删除与关闭，外加一条从根部门数下来的路径。 */
export function OrgDetailDepartmentHeader({
  department,
  allDepartments,
  icon,
  accentColor,
  onDelete,
  onClose,
}: {
  department: OrgDepartment;
  allDepartments: OrgDepartment[];
  icon: string;
  accentColor: AgentColor;
  onDelete: () => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const path = buildPath(department, allDepartments);
  const iconNode = React.createElement(iconForKey(icon), {
    className: "size-5 text-agent-foreground",
    "aria-hidden": true,
  });

  return (
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
            {department.name}
          </div>
        </div>
        <div className="flex shrink-0 gap-1">
          <Button
            variant="outline"
            size="icon"
            className="size-8"
            aria-label={t("org.department.deleteDepartment")}
            onClick={onDelete}
          >
            <Trash2 className="size-4 text-destructive" />
          </Button>
          <Button
            variant="outline"
            size="icon"
            className="size-8"
            aria-label={t("common.close")}
            onClick={onClose}
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
  );
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
