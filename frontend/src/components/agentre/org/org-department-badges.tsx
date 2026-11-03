import { Crown } from "lucide-react";
import { useTranslation } from "react-i18next";

import { cn, DepartmentMark } from "@agentre-hub/agentre-ui";

import { agentIconColorClassNames, type AgentColor } from "../types";

import { safeAgentColor } from "./types";

/** 部门记号：结构栏的下拉项与成员栏的部门行共用同一枚（预设 ▸ 图标 ▸ 首字）。 */
export function DepartmentIconBadge({
  accentColor,
  className,
  icon,
  name,
}: {
  accentColor: string;
  className?: string;
  icon: string;
  /** 部门名：既是首字兜底的来源，也是这一格的可及名。 */
  name: string;
}) {
  return (
    <DepartmentMark
      name={name}
      icon={icon}
      accentColor={safeAgentColor(accentColor)}
      className={className}
    />
  );
}

/** 主管徽标：负责人下拉的预览与成员行里各出现一次，后者用紧凑档。 */
export function LeadBadge({
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
        "inline-flex shrink-0 items-center rounded-sm bg-secondary font-mono font-semibold text-secondary-foreground",
        compact ? "gap-1 px-1 py-0.5 text-2xs" : "gap-1 px-1.5 py-0.5 text-2xs",
      )}
    >
      <Crown
        className={cn(
          compact ? "size-2" : "size-2.5",
          agentIconColorClassNames[color],
        )}
        aria-hidden="true"
      />
      {t("org.department.leadBadge")}
    </span>
  );
}
