import { ArrowRight } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { Badge, Button } from "@agentre-hub/agentre-ui";

import { blockReasonToCta, navigateToTarget } from "../not-chattable";

import type { chat_svc } from "../../../../wailsjs/go/models";

type ChatAgentItem = chat_svc.ChatAgentItem;

/** 不可对话 Agent 新会话 tab 的内联引导块（组 4B）：复用任务 2 的徽标 / 原因 / CTA
 *  文案与跳转语义，渲染在输入框上方；按钮走 navigateToTarget 与引导弹窗一致。 */
function NewSessionChatGuard({ agent }: { agent: ChatAgentItem }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const cta = blockReasonToCta(agent.blockReason ?? "");
  const reasonKey = cta.copyKey;
  return (
    <div
      className="mb-2 flex flex-col items-start gap-2 rounded-lg border border-status-waiting/40 bg-status-waiting-bg px-4 py-3"
      data-testid="new-session-guard"
      role="alert"
      aria-live="polite"
    >
      <div className="flex flex-wrap items-center gap-2 text-sm font-semibold">
        <Badge
          variant="outline"
          className="border-status-waiting/40 bg-background px-1.5 py-0 text-2xs text-foreground"
        >
          {agent.blockReason === "no-backend"
            ? t("agentList.backendNotConfigured")
            : t("agentList.notConfigured")}
        </Badge>
        {t("chatPanel.newSession.guard.title", { name: agent.name })}
      </div>
      <div className="text-xs leading-relaxed text-muted-foreground">
        {t(`${reasonKey}.description`, { name: agent.name })}
      </div>
      <div className="flex items-center gap-4">
        <Button
          type="button"
          size="sm"
          onClick={() =>
            navigateToTarget(navigate, cta.primaryTarget, agent.id)
          }
        >
          {t(cta.primaryLabel)}
        </Button>
        <button
          type="button"
          className="inline-flex items-center gap-1 text-xs font-medium text-primary-text hover:underline"
          onClick={() =>
            navigateToTarget(navigate, "settings:agent-backend", agent.id)
          }
        >
          {t(cta.secondaryLabel)}
          <ArrowRight className="size-3" aria-hidden="true" />
        </button>
      </div>
    </div>
  );
}

export { NewSessionChatGuard };
