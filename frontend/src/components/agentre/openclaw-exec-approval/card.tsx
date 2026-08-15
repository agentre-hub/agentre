import * as React from "react";
import { ShieldAlertIcon } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
  useTranscriptPorts,
  TranscriptCard,
  TranscriptCardBody,
} from "@agentre-ai/agentre-ui";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import type { ExecApprovalData } from "@/stores/chat-streams-store";

const supportedDecisions = ["allow-once", "allow-always", "deny"] as const;
type ExecApprovalDecision = (typeof supportedDecisions)[number];

function allowedDecisions(approval: ExecApprovalData): ExecApprovalDecision[] {
  const values = approval.allowedDecisions ?? [];
  return supportedDecisions.filter((decision) => values.includes(decision));
}

export function OpenClawExecApprovalCard({
  approval,
  sessionId,
}: {
  approval: ExecApprovalData;
  sessionId: number;
}) {
  const { t } = useTranslation();
  const ports = useTranscriptPorts();
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [localTerminal, setLocalTerminal] = React.useState<{
    status: string;
    decision?: string;
  } | null>(null);
  const inFlightRef = React.useRef(false);

  React.useEffect(() => {
    if (approval.status !== "pending") {
      setLocalTerminal(null);
      inFlightRef.current = false;
      setSubmitting(false);
    }
  }, [approval.status, approval.decision]);

  const status = localTerminal?.status ?? approval.status;
  const decision = localTerminal?.decision ?? approval.decision;
  const pending = status === "pending";
  const decisions = allowedDecisions(approval);

  const resolve = async (nextDecision: ExecApprovalDecision) => {
    if (!pending || inFlightRef.current) return;
    inFlightRef.current = true;
    setSubmitting(true);
    setError(null);
    try {
      const response = await ports.resolveExecApproval({
        sessionId,
        approvalId: approval.id,
        decision: nextDecision,
      });
      setLocalTerminal({
        status: response.status || "resolved",
        decision: response.decision || nextDecision,
      });
    } catch {
      setError(t("openclawExecApproval.submitFailed"));
    } finally {
      inFlightRef.current = false;
      setSubmitting(false);
    }
  };

  const terminalLabel =
    status === "expired"
      ? t("openclawExecApproval.status.expired")
      : decision
        ? t(`openclawExecApproval.decisionResult.${decision}`)
        : t("openclawExecApproval.status.resolved");

  return (
    <TranscriptCard
      data-testid="openclaw-exec-approval-card"
      data-selectable-text="true"
      tone={pending ? "pending" : status === "expired" ? "error" : "done"}
    >
      <div className="flex items-center gap-2 px-3.5 py-2.5">
        <ShieldAlertIcon className="size-4 shrink-0 text-status-waiting" />
        <span className="font-medium">{t("openclawExecApproval.title")}</span>
        <Badge className="ml-auto" variant={pending ? "secondary" : "outline"}>
          {pending ? t("openclawExecApproval.status.pending") : terminalLabel}
        </Badge>
      </div>

      <TranscriptCardBody className="flex flex-col gap-3">
        <div className="flex flex-col gap-1">
          <span className="text-aux text-muted-foreground">
            {t("openclawExecApproval.command")}
          </span>
          <pre className="max-h-64 overflow-auto rounded-sm bg-muted/40 px-2.5 py-2 text-aux whitespace-pre-wrap">
            <code>{approval.commandText}</code>
          </pre>
        </div>

        {(approval.host || approval.nodeId || approval.agentId) && (
          <dl className="flex flex-wrap gap-x-4 gap-y-2 text-aux">
            {approval.host && (
              <div className="flex items-center gap-1.5">
                <dt className="text-muted-foreground">
                  {t("openclawExecApproval.host")}
                </dt>
                <dd>{approval.host}</dd>
              </div>
            )}
            {approval.nodeId && (
              <div className="flex items-center gap-1.5">
                <dt className="text-muted-foreground">
                  {t("openclawExecApproval.node")}
                </dt>
                <dd>{approval.nodeId}</dd>
              </div>
            )}
            {approval.agentId && (
              <div className="flex items-center gap-1.5">
                <dt className="text-muted-foreground">
                  {t("openclawExecApproval.agent")}
                </dt>
                <dd>{approval.agentId}</dd>
              </div>
            )}
          </dl>
        )}

        {!pending && approval.resolvedBy && (
          <div className="flex items-center gap-1.5 text-aux">
            <span className="text-muted-foreground">
              {t("openclawExecApproval.resolvedBy")}
            </span>
            <span>{approval.resolvedBy}</span>
          </div>
        )}
      </TranscriptCardBody>

      {pending && (
        <TranscriptCardBody className="flex flex-wrap items-center gap-2">
          {decisions.map((value) => (
            <Button
              key={value}
              size="sm"
              variant={value === "deny" ? "destructive" : "default"}
              disabled={submitting}
              onClick={() => void resolve(value)}
            >
              {submitting && <Spinner data-icon="inline-start" />}
              {t(`openclawExecApproval.decision.${value}`)}
            </Button>
          ))}
          {submitting && (
            <span className="text-aux text-muted-foreground">
              {t("openclawExecApproval.loading")}
            </span>
          )}
          {error && (
            <span role="alert" className="text-aux text-destructive">
              {error}
            </span>
          )}
        </TranscriptCardBody>
      )}
    </TranscriptCard>
  );
}
