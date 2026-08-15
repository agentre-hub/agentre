import * as React from "react";
import { useTranslation } from "react-i18next";
import {
  Check,
  ChevronRight,
  Copy,
  LoaderCircle,
  Terminal,
  TriangleAlert,
  Wrench,
} from "lucide-react";
import {
  cn,
  statusConfig,
  summarizeRawTool,
  useCollapsible,
  copyTextWithToast,
  CollapsibleCode,
  CollapsibleCodeParams,
  toolInputEntries,
  TranscriptCard,
  TranscriptCardBody,
  TranscriptCardHeader,
  TranscriptPill,
  useTranscriptBooleanState,
} from "@agentre-ai/agentre-ui";
import type { AgentStatus, TranscriptBlock } from "@agentre-ai/agentre-ui";

import {
  commandResultOf,
  isFailedCommandResult,
  type CommandResult,
} from "../command-result";
import type { CanonicalCardProps } from "../props";

import { ToolPermissionOverlay } from "./tool-permission-overlay";

// RawToolCard 是不进 canonical 集合的工具(Bash/Read/Glob/MCP 等)的兜底卡。
// 视觉等价于旧 ToolInvocationCard:折叠/展开 + 状态 pill + 参数表 + 错误染色 +
// command_execution 结果解析,但识别都走 input shape (input.command 存在 ⇒
// shell-shape),**不复活 name 硬集合** —— 那是 backend-specific 知识泄漏。
export const RawToolCard: React.FC<CanonicalCardProps> = ({
  toolBlock,
  resultBlock,
  cwd,
  sessionId,
  uiStateKey,
}) => {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useTranscriptBooleanState(uiStateKey, false);
  const { mounted, onTransitionEnd } = useCollapsible(expanded);

  const toolName = toolBlock.toolName ?? "tool";
  const input = toolBlock.toolInput as Record<string, unknown> | undefined;
  const isBackground = input?.run_in_background === true;
  const bgSubagent = (toolBlock as TranscriptBlock).subagent;
  // The 「后台运行」 pill is a "running in background right now" indicator. A
  // run_in_background Bash gets its tool_result (launch ACK) immediately, so we
  // can't key off hasResult — drive it off the background subagent's status.
  const bgRunning =
    isBackground &&
    bgSubagent?.status !== "completed" &&
    bgSubagent?.status !== "failed" &&
    bgSubagent?.status !== "canceled";
  const bgTaskId = bgSubagent?.taskId ?? undefined;
  const isShellShape = typeof input?.command === "string";
  const toolLabel = isShellShape ? "Bash" : toolName;
  const ToolIcon = isShellShape ? Terminal : Wrench;

  const summary = React.useMemo(
    () => summarizeRawTool(toolName, input, { cwd }),
    [toolName, input, cwd],
  );

  const commandResult = React.useMemo(
    () => commandResultOf(resultBlock),
    [resultBlock],
  );
  const commandFailed = isFailedCommandResult(commandResult);

  const hasResult = !!resultBlock;
  const isError = !!resultBlock?.isError || commandFailed;
  const status: AgentStatus = isError
    ? "error"
    : hasResult
      ? "running"
      : "waiting";
  const pillConfig = statusConfig[status];

  const statusLabel =
    typeof commandResult?.exitCode === "number"
      ? `EXIT ${commandResult.exitCode}`
      : isError
        ? t("canonical.status.error")
        : hasResult
          ? t("canonical.status.done")
          : t("canonical.status.running");
  const StatusIcon = isError ? TriangleAlert : hasResult ? Check : LoaderCircle;

  const perm = (toolBlock as TranscriptBlock).toolPermission;
  const showOverlay = perm && !perm.resolved;
  const allowedBadge =
    perm?.resolved && perm.allowed
      ? perm.alwaysAllow
        ? t("canonical.raw.allowedSession")
        : t("canonical.raw.allowed")
      : null;

  const params = React.useMemo(() => toolInputEntries(input), [input]);
  const resultMeta = commandResult
    ? formatCommandMeta(commandResult)
    : hasResult
      ? null
      : t("canonical.raw.waitingResult");

  async function copyParams() {
    await copyTextWithToast(input ? JSON.stringify(input, null, 2) : "", {
      errorTitle: t("common.copyFailed"),
      successTitle: t("common.copied"),
    });
  }

  return (
    <TranscriptCard
      data-testid="raw-tool-card"
      aria-label={t("canonical.raw.aria", { tool: toolName })}
      tone={isError ? "error" : "default"}
      className="font-mono text-aux"
    >
      <TranscriptCardHeader
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
      >
        <ChevronRight
          className={cn(
            "size-3 shrink-0 text-muted-foreground transition-transform",
            expanded && "rotate-90",
          )}
          aria-hidden="true"
        />
        <ToolIcon
          className="size-3.5 shrink-0 text-primary-text"
          aria-hidden="true"
        />
        <span className="shrink-0 font-semibold text-primary-text">
          {toolLabel}
        </span>
        {summary && (
          <>
            <span className="text-muted-foreground">·</span>
            <span className="min-w-0 truncate text-muted-foreground">
              {summary}
            </span>
          </>
        )}
        <span className="min-w-0 flex-1" />
        {bgRunning && (
          <TranscriptPill
            data-testid="bg-running-pill"
            className="rounded-full border border-border"
          >
            <LoaderCircle
              className="size-2.5 animate-spin"
              aria-hidden="true"
            />
            {t("canonical.raw.backgroundRunning")}
            {bgTaskId && (
              <>
                <span className="opacity-50">·</span>
                <span className="font-mono">{bgTaskId}</span>
              </>
            )}
          </TranscriptPill>
        )}
        {allowedBadge && (
          <TranscriptPill tone="done" title={t("canonical.raw.approvedTitle")}>
            <Check className="size-2.5" aria-hidden="true" />
            {allowedBadge}
          </TranscriptPill>
        )}
        <TranscriptPill className={pillConfig.pillClassName}>
          <StatusIcon
            className={cn(
              "size-2.5",
              !hasResult && !isError ? "animate-spin" : "",
            )}
            aria-hidden="true"
          />
          {statusLabel}
        </TranscriptPill>
      </TranscriptCardHeader>
      <div
        aria-hidden={!expanded}
        onTransitionEnd={onTransitionEnd}
        className="grid transition-[grid-template-rows] duration-200 ease-out motion-reduce:transition-none"
        style={{ gridTemplateRows: expanded ? "1fr" : "0fr" }}
      >
        <div className="min-h-0 overflow-hidden">
          {mounted ? (
            <TranscriptCardBody
              data-selectable-text="true"
              className="flex flex-col gap-3"
            >
              <Section
                label={t("canonical.raw.sections.params")}
                meta={
                  params.length > 0
                    ? t("canonical.code.paramCount", { count: params.length })
                    : undefined
                }
                actions={
                  params.length > 0 ? (
                    <button
                      type="button"
                      onClick={() => void copyParams()}
                      className="inline-flex items-center gap-1 rounded border border-border-strong px-1.5 py-0.5 font-sans text-meta text-muted-foreground transition-colors hover:border-primary hover:bg-primary-soft hover:text-primary-text"
                    >
                      <Copy className="size-3" aria-hidden="true" />
                      {t("canonical.code.copyAll")}
                    </button>
                  ) : undefined
                }
              >
                {params.length === 0 ? (
                  <div className="text-muted-foreground">
                    {t("canonical.raw.emptyParams")}
                  </div>
                ) : (
                  <CollapsibleCodeParams
                    entries={params}
                    testIdPrefix={
                      uiStateKey ? `${uiStateKey}:param` : undefined
                    }
                  />
                )}
              </Section>
              <Section
                label={t("canonical.raw.sections.result")}
                meta={
                  resultMeta ? (
                    <span
                      className={
                        !hasResult ? pillConfig.textClassName : undefined
                      }
                    >
                      {resultMeta}
                    </span>
                  ) : null
                }
              >
                <div className="flex flex-col gap-1">
                  {commandResult ? (
                    commandResult.output ? (
                      <CollapsibleCode
                        value={commandResult.output}
                        surface={isError ? "destructive" : "muted"}
                        bodyClassName="rounded-sm px-2.5 py-2"
                      />
                    ) : (
                      <div className="rounded-sm bg-muted/40 px-2.5 py-2 text-muted-foreground">
                        {t("canonical.raw.emptyOutput")}
                      </div>
                    )
                  ) : hasResult ? (
                    resultBlock?.text ? (
                      <CollapsibleCode
                        value={resultBlock.text}
                        surface={isError ? "destructive" : "muted"}
                        bodyClassName="rounded-sm px-2.5 py-2"
                      />
                    ) : (
                      <div className="rounded-sm bg-muted/40 px-2.5 py-2 text-muted-foreground">
                        {t("canonical.raw.emptyResult")}
                      </div>
                    )
                  ) : (
                    <div className="rounded-sm bg-muted/40 px-2.5 py-2 text-muted-foreground">
                      {"—"}
                    </div>
                  )}
                </div>
              </Section>
            </TranscriptCardBody>
          ) : null}
        </div>
      </div>
      {showOverlay && perm && (
        <ToolPermissionOverlay
          payload={{ requestId: perm.requestId, toolName: perm.toolName }}
          sessionId={sessionId}
        />
      )}
    </TranscriptCard>
  );
};

function Section({
  children,
  label,
  meta,
  actions,
}: {
  children: React.ReactNode;
  label: string;
  meta?: React.ReactNode;
  actions?: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center gap-2">
        <span className="font-sans text-meta font-semibold text-muted-foreground">
          {label}
        </span>
        {meta ? (
          <span className="font-mono text-meta text-muted-foreground">
            {meta}
          </span>
        ) : null}
        <span className="h-px min-w-0 flex-1 bg-border" />
        {actions}
      </div>
      {children}
    </div>
  );
}

function formatCommandMeta(result: CommandResult): string {
  const parts: string[] = [];
  if (typeof result.exitCode === "number")
    parts.push(`exit ${result.exitCode}`);
  if (result.status) parts.push(result.status);
  return parts.join(" · ");
}
