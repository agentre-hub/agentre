import * as React from "react";
import { useTranslation } from "react-i18next";
import { Button, useTranscriptPorts } from "@agentre-ai/agentre-ui";

// ToolPermissionOverlay 是 RawToolCard 的"等待审批"小条。ExitPlanMode 这种特例
// 走 plan-approve-request/card.tsx,不走这里;这里只负责通用工具的 Allow / Deny。
export type ToolPermissionPayload = {
  requestId: string;
  toolName?: string;
};

export const ToolPermissionOverlay: React.FC<{
  payload: ToolPermissionPayload;
  sessionId?: number;
}> = ({ payload, sessionId }) => {
  const { t } = useTranslation();
  const ports = useTranscriptPorts();
  const [submitting, setSubmitting] = React.useState(false);

  const handle = async (allow: boolean) => {
    if (!sessionId || submitting) return;
    setSubmitting(true);
    try {
      await ports.answerToolPermission({
        sessionId,
        requestId: payload.requestId,
        allow,
      });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div
      className="mt-2 flex items-center gap-2 rounded border border-status-waiting/40 bg-status-waiting-bg px-2 py-1 text-xs"
      data-testid="tool-permission-overlay"
    >
      <span className="text-status-waiting">
        {t("canonical.raw.permission.waiting")}
      </span>
      <Button
        size="sm"
        variant="default"
        disabled={submitting || !sessionId}
        onClick={() => void handle(true)}
      >
        {t("canonical.raw.permission.allow")}
      </Button>
      <Button
        size="sm"
        variant="destructive"
        disabled={submitting || !sessionId}
        onClick={() => void handle(false)}
      >
        {t("common.reject")}
      </Button>
    </div>
  );
};
