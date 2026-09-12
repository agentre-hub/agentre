import * as React from "react";
import { Crown } from "lucide-react";
import { useTranslation } from "react-i18next";

import { cn } from "@/lib/utils";

import {
  agentColorClassNames,
  agentTextColorClassNames,
  type AgentColor,
} from "../types";

import { iconForKey, safeAgentColor } from "./types";

/** 部门字形：结构栏的下拉项与成员栏的部门行共用同一枚。 */
export function DepartmentIconBadge({
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
