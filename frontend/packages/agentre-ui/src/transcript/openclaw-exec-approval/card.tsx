import * as React from "react";
import { ShieldAlertIcon } from "lucide-react";

import { Badge } from "../../ui/badge";
import { Button } from "../../ui/button";
import { Spinner } from "../../ui/spinner";
import { useUiTranslation } from "../../i18n";
import type { TranscriptBlockExecApproval } from "../dto";
import { useTranscriptPorts } from "../ports-context";
import { TranscriptCard, TranscriptCardBody } from "../transcript-card";

// 后端无关的决定词表，按按钮顺序排列；卡片只渲染后端在 allowedDecisions 里给出的那几项。
const supportedDecisions = [
  "allow-once",
  "allow-session",
  "allow-always",
  "deny",
] as const;
type ExecApprovalDecision = (typeof supportedDecisions)[number];

type Translate = ReturnType<typeof useUiTranslation>["t"];

function approvalTitle(t: Translate, kind: string | undefined): string {
  switch (kind) {
    case "hermes":
      return t("openclawExecApproval.titleHermes");
    case "plugin":
      return t("openclawExecApproval.titlePlugin");
    case "system-agent":
      return t("openclawExecApproval.titleSystemAgent");
    default:
      return t("openclawExecApproval.title");
  }
}

function actionCategoryLabel(t: Translate, category: string): string {
  switch (category) {
    case "message":
      return t("openclawExecApproval.actionCategory.message");
    case "payment":
      return t("openclawExecApproval.actionCategory.payment");
    case "publish":
      return t("openclawExecApproval.actionCategory.publish");
    case "automation":
      return t("openclawExecApproval.actionCategory.automation");
    default:
      return category;
  }
}

/** 后端给出的已脱敏内容里，按「标签 · 值」一行呈现的那几格（空的不出现）。 */
function detailRows(
  t: Translate,
  approval: TranscriptBlockExecApproval,
): { label: string; value: string }[] {
  const rows: { label: string; value: string | undefined }[] = [
    {
      label: t("openclawExecApproval.action"),
      value: approval.actionCategory
        ? actionCategoryLabel(t, approval.actionCategory)
        : undefined,
    },
    { label: t("openclawExecApproval.plugin"), value: approval.pluginName },
    { label: t("openclawExecApproval.tool"), value: approval.toolName },
    {
      label: t("openclawExecApproval.targets"),
      value: approval.messageTargets?.join(", "),
    },
    {
      label: t("openclawExecApproval.recipientCount"),
      value: approval.recipientCount
        ? String(approval.recipientCount)
        : undefined,
    },
    { label: t("openclawExecApproval.amount"), value: approval.paymentAmount },
    { label: t("openclawExecApproval.payee"), value: approval.paymentPayee },
    {
      label: t("openclawExecApproval.publishTarget"),
      value: approval.publishTarget,
    },
    {
      label: t("openclawExecApproval.visibility"),
      value: approval.publishVisibility,
    },
    {
      label: t("openclawExecApproval.automation"),
      value: approval.automationName,
    },
    { label: t("openclawExecApproval.host"), value: approval.host },
    { label: t("openclawExecApproval.node"), value: approval.nodeId },
    { label: t("openclawExecApproval.agent"), value: approval.agentId },
  ];
  return rows.filter((row): row is { label: string; value: string } =>
    Boolean(row.value),
  );
}

function allowedDecisions(
  approval: TranscriptBlockExecApproval,
): ExecApprovalDecision[] {
  const values = approval.allowedDecisions ?? [];
  return supportedDecisions.filter((decision) => values.includes(decision));
}

export function OpenClawExecApprovalCard({
  approval,
  sessionId,
}: {
  approval: TranscriptBlockExecApproval;
  sessionId: number;
}) {
  const { t } = useUiTranslation();
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
  const details = detailRows(t, approval);

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
        <span className="font-medium">{approvalTitle(t, approval.kind)}</span>
        <Badge className="ml-auto" variant={pending ? "secondary" : "outline"}>
          {pending ? t("openclawExecApproval.status.pending") : terminalLabel}
        </Badge>
      </div>

      <TranscriptCardBody className="flex flex-col gap-3">
        {approval.commandText && (
          <div className="flex flex-col gap-1">
            <span className="text-aux text-muted-foreground">
              {t("openclawExecApproval.command")}
            </span>
            <pre className="max-h-64 overflow-auto rounded-sm bg-muted/40 px-2.5 py-2 text-aux whitespace-pre-wrap">
              <code>{approval.commandText}</code>
            </pre>
          </div>
        )}

        {approval.description && (
          <div className="flex flex-col gap-1 text-aux">
            <span className="text-muted-foreground">
              {t("openclawExecApproval.description")}
            </span>
            <p className="whitespace-pre-wrap">{approval.description}</p>
          </div>
        )}

        {details.length > 0 && (
          <dl className="flex flex-wrap gap-x-4 gap-y-2 text-aux">
            {details.map((row) => (
              <div key={row.label} className="flex items-center gap-1.5">
                <dt className="text-muted-foreground">{row.label}</dt>
                <dd>{row.value}</dd>
              </div>
            ))}
          </dl>
        )}

        {approval.warnings && approval.warnings.length > 0 && (
          <div className="flex flex-col gap-1 text-aux">
            <span className="text-muted-foreground">
              {t("openclawExecApproval.warnings")}
            </span>
            <ul className="flex list-disc flex-col gap-0.5 pl-4 text-status-waiting">
              {approval.warnings.map((warning, index) => (
                <li key={index}>{warning}</li>
              ))}
            </ul>
          </div>
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
          {decisions.includes("allow-always") && (
            <p className="basis-full text-aux text-muted-foreground">
              {t("openclawExecApproval.alwaysScopeNote")}
            </p>
          )}
        </TranscriptCardBody>
      )}
    </TranscriptCard>
  );
}
