import { AlertTriangle } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

import {
  Alert,
  AlertDescription,
  AlertTitle,
  Button,
} from "@agentre-hub/agentre-ui";

import type {
  agent_backend_svc,
  department_svc,
} from "../../../../wailsjs/go/models";

import { ExecTargetList, type ExecTargetRow } from "./exec-target-list";
import type { useExecTargetAvailability } from "./use-exec-target-availability";

type Availability = ReturnType<typeof useExecTargetAvailability>["byBackendId"];

/** 三栏里的「执行」栏：不可对话提示 + 执行目标列表 + 技能说明。 */
export function OrgDetailAgentExecution({
  agentId,
  agentName,
  availability,
  targets,
  backends,
  builtinMissingProvider,
  hasExecTargets,
  loading,
  saveRejected,
  onChange,
  onReorder,
  onSkillsChange,
}: {
  agentId: number;
  agentName: string;
  availability: Availability;
  targets: ExecTargetRow[];
  backends: agent_backend_svc.BackendItem[];
  builtinMissingProvider: boolean;
  hasExecTargets: boolean;
  loading: boolean;
  saveRejected: boolean;
  onChange: (next: ExecTargetRow[]) => void;
  onReorder: (next: ExecTargetRow[]) => void;
  onSkillsChange: (
    agentBackendId: number,
    skills: department_svc.AgentSkillDTO[],
  ) => void;
}) {
  const { t } = useTranslation();
  return (
    <section
      aria-label={t("org.detail.columns.execution")}
      data-slot="org-detail-col-execution"
      className="flex min-w-0 flex-col gap-4"
    >
      <h3 className="font-mono text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
        {t("org.detail.columns.execution")}
      </h3>
      {builtinMissingProvider ? <ProviderGapAlert /> : null}
      {/* 执行目标区只有这一个列表：它恒等于这台电脑当前实际的派发顺序，
          每一档的技能折在它自己那一行里。 */}
      <ExecTargetList
        agentId={agentId}
        agentName={agentName}
        availability={availability}
        targets={targets}
        backends={backends}
        onChange={onChange}
        onReorder={onReorder}
        onSkillsChange={onSkillsChange}
        loading={loading}
        saveRejected={saveRejected}
      />
      {/* 技能三种色调的图例只出一次：芯片本身折在各行里，逐行重复一遍图例
          比不给还吵。 */}
      {hasExecTargets && (
        <p className="font-mono text-2xs text-muted-foreground">
          {t("org.agent.skills.inheritNote")}
        </p>
      )}
    </section>
  );
}

/**
 * 任务 8：不可对话状态内联提示（继续只覆盖单档场景——多档时"哪一档缺什么"已经在
 * 执行目标列表的逐行徽标 + 全部不可用横幅里说明，不需要在这里重复一份笼统提示）。
 * 「没有任何执行目标」不在这里说：那一条与列表空态是同一个条件，只由列表说一次。
 */
function ProviderGapAlert() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  return (
    <Alert
      className="border-status-waiting/40 bg-status-waiting-bg text-xs"
      data-testid="org-agent-provider-gap"
    >
      <AlertTriangle className="size-4" aria-hidden="true" />
      <AlertTitle className="text-xs">
        {t("org.agent.backend.providerGapTitle")}
      </AlertTitle>
      <AlertDescription className="text-2xs">
        {t("org.agent.backend.providerGapDescription")}
        <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1">
          <Button
            type="button"
            size="sm"
            className="h-7 px-2.5 text-2xs"
            onClick={() =>
              navigate("/settings", {
                state: { settingsPage: "llm-providers" },
              })
            }
          >
            {t("org.agent.backend.configureProvider")}
          </Button>
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
            {t("org.agent.backend.goAgentBackendSettings")}
          </Button>
        </div>
      </AlertDescription>
    </Alert>
  );
}
