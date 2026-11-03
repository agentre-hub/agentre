import * as React from "react";
import { ChevronRight, CornerDownRight, Trash2, X } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  Button,
  IdentityFields,
  cn,
  type IdentityFieldsProps,
} from "@agentre-hub/agentre-ui";

import type { OrgDepartment } from "./types";

/** 部门详情的唯一身份区：可编辑身份 + 原有的路径 + 删除、关闭。 */
export function OrgDetailDepartmentHeader({
  department,
  allDepartments,
  identity,
  onDelete,
  onClose,
}: {
  department: OrgDepartment;
  allDepartments: OrgDepartment[];
  // 名字 / 描述 / 记号 / 主题色都归这一行，header 不再另画一枚只读的字形与名字。
  identity: Omit<IdentityFieldsProps, "kind" | "testIdPrefix" | "meta">;
  onDelete: () => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const path = buildPath(department, allDepartments);

  return (
    <header className="flex shrink-0 items-start gap-3 px-5 py-4">
      <div className="min-w-0 flex-1">
        <IdentityFields
          {...identity}
          kind="department"
          testIdPrefix="department-detail"
          meta={
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
                        ? "bg-primary-soft text-primary-text"
                        : "bg-secondary text-foreground",
                    )}
                  >
                    {node.name}
                  </span>
                </React.Fragment>
              ))}
            </div>
          }
        />
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
