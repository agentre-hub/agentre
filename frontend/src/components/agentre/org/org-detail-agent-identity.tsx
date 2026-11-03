import { useTranslation } from "react-i18next";

import {
  DepartmentMark,
  OrgPlacementField,
  type OrgPlacement,
} from "@agentre-hub/agentre-ui";

import { AgentAvatar } from "../primitives";
import { safeAgentColor, type OrgAgent, type OrgDepartment } from "./types";

/** 三栏里的身份栏保留归属；可编辑的身份区统一放在顶栏。 */
export function OrgDetailAgentIdentity({
  agent,
  agents,
  departments,
  placement,
  reportTarget,
  onPlacementPick,
}: {
  agent: OrgAgent;
  agents: OrgAgent[];
  departments: OrgDepartment[];
  placement: OrgPlacement;
  reportTarget: OrgAgent | null;
  onPlacementPick: (next: OrgPlacement) => void;
}) {
  const { t } = useTranslation();
  return (
    <section
      aria-label={t("org.detail.columns.identity")}
      data-slot="org-detail-col-identity"
      className="flex min-w-0 flex-col gap-4"
    >
      <h3 className="font-mono text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
        {t("org.detail.columns.identity")}
      </h3>
      <OrgPlacementField
        agent={agent}
        agents={agents}
        departments={departments}
        placement={placement}
        reportTarget={reportTarget}
        onPick={onPlacementPick}
        renderAgentAvatar={(a, className) => (
          <AgentAvatar
            name={a.name}
            color={safeAgentColor(a.avatarColor ?? "")}
            size="xs"
            avatarDataUrl={a.avatarDataUrl}
            avatarIcon={a.avatarIcon}
            className={className}
          />
        )}
        renderDepartmentGlyph={(department) =>
          department ? (
            <DepartmentMark
              name={department.name}
              icon={department.icon}
              accentColor={department.accentColor}
              size="xs"
              className="size-5 shrink-0 rounded-sm"
            />
          ) : null
        }
      />
    </section>
  );
}
