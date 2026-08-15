import * as React from "react";
import { useCallback, useState } from "react";
import { Check, ChevronDown, ShieldAlert, X } from "lucide-react";

import {
  cn,
  shouldIgnoreClickForSelection,
  useCollapsible,
  useMarkToolPermissionResolved,
  useTranscriptPorts,
  useUiTranslation,
  Button,
  CollapsibleCode,
  TranscriptCard,
  TranscriptCardBody,
  TranscriptCardHeader,
  TranscriptPill,
  useTranscriptBooleanState,
} from "@agentre-ai/agentre-ui";
import type { TranscriptBlockToolPermission } from "@agentre-ai/agentre-ui";

import type { CanonicalCardProps } from "../props";
import type { CanonicalDTO, ToolPermissionDTO } from "../types";

function readPermission(toolBlock: unknown): ToolPermissionDTO | undefined {
  const c = (toolBlock as { canonical?: CanonicalDTO }).canonical;
  if (!c || c.kind !== "tool.permission") return undefined;
  return c.toolPermission;
}

// formatToolSummary 把 input 抽出来给 header 当一行摘要:Bash 突出 command,
// 其它工具退回压缩 JSON(截断 100 字符),让卡片不展开也能看清意图。
function formatToolSummary(
  toolName: string,
  input: Record<string, unknown> | undefined,
): string {
  if (!input) return "";
  if (toolName === "Bash" && typeof input.command === "string") {
    return input.command;
  }
  try {
    const compact = JSON.stringify(input);
    return compact.length > 100 ? compact.slice(0, 97) + "..." : compact;
  } catch {
    return "";
  }
}

// ToolPermissionCard 渲染 canonical.tool.permission —— 通用工具审批(非
// ExitPlanMode,后者走 PlanApproveCard)。来源:claudecode can_use_tool 控制请求。
//
// 数据来源同 PlanApproveCard:
//   - liveBlocks(resolved=false → 渲染交互态 仅本次允许 / 本会话始终允许 / 拒绝)
//   - 历史持久化 ChatBlock(resolved=true → 渲染只读 banner,标明决策)
export const ToolPermissionCard: React.FC<CanonicalCardProps> = ({
  toolBlock,
  sessionId,
  messageId,
  uiStateKey,
}) => {
  const { t } = useUiTranslation();
  const ports = useTranscriptPorts();
  const payload = readPermission(toolBlock);
  const markToolPermissionResolved = useMarkToolPermissionResolved();

  const [collapsed, setCollapsed] = useTranscriptBooleanState(uiStateKey, true);
  const { mounted, onTransitionEnd } = useCollapsible(!collapsed);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const isResolved = !!payload?.resolved;

  // submit 必须在任何 early-return 前声明,否则两次 render 之间 hook 数会变化。
  const submit = useCallback(
    async (allow: boolean, alwaysAllowSession: boolean) => {
      if (!payload || !payload.requestId || !sessionId) return;
      if (isResolved || submitting) return;
      setError(null);
      setSubmitting(true);
      const optimistic: TranscriptBlockToolPermission = {
        requestId: payload.requestId,
        toolName: payload.toolName,
        toolInput: (payload.toolInput ?? {}) as Record<string, unknown>,
        resolved: true,
        allowed: allow,
        alwaysAllow: alwaysAllowSession,
      };
      try {
        markToolPermissionResolved({
          sessionId,
          assistantMessageId: messageId ?? 0,
          toolPermission: optimistic,
        });
        await ports.answerToolPermission({
          sessionId,
          requestId: payload.requestId,
          allow,
          alwaysAllowSession,
        });
      } catch (err) {
        // 后端 takePermWaiter 在 RespondToControl 失败前已经 take+delete,
        // 回滚后再点也会 "no waiting tool permission" 失败;切回未决态只是
        // 为了把错误文案露出来。
        markToolPermissionResolved({
          sessionId,
          assistantMessageId: messageId ?? 0,
          toolPermission: {
            ...optimistic,
            resolved: false,
            allowed: false,
            alwaysAllow: false,
          },
        });
        setError(
          err instanceof Error
            ? err.message
            : t("canonical.toolPermission.submitFailed"),
        );
      } finally {
        setSubmitting(false);
      }
    },
    [
      payload,
      ports,
      sessionId,
      messageId,
      isResolved,
      submitting,
      markToolPermissionResolved,
      t,
    ],
  );

  if (!payload || !payload.requestId) return null;

  const summary = formatToolSummary(
    payload.toolName,
    payload.toolInput as Record<string, unknown> | undefined,
  );
  const inputJson = payload.toolInput
    ? JSON.stringify(payload.toolInput, null, 2)
    : "";

  return (
    <TranscriptCard
      data-testid="tool-permission-card"
      data-selectable-text="true"
      className={cn(
        "text-card-foreground",
        isResolved && !payload.allowed
          ? "border-destructive/40"
          : "border-status-waiting/40",
      )}
    >
      <TranscriptCardHeader
        onClick={(event) => {
          if (shouldIgnoreClickForSelection(event)) return;
          setCollapsed((c) => !c);
        }}
        aria-expanded={!collapsed}
      >
        <ShieldAlert
          className={cn(
            "h-4 w-4 shrink-0",
            isResolved
              ? payload.allowed
                ? "text-status-running"
                : "text-destructive"
              : "text-status-waiting",
          )}
        />
        <span data-copyable-control-text="true" className="font-medium">
          {payload.toolName}
        </span>
        {summary && (
          <span
            data-copyable-control-text="true"
            className="truncate text-aux text-muted-foreground"
          >
            {summary}
          </span>
        )}
        <span className="ml-auto flex items-center gap-2 text-aux text-muted-foreground">
          {isResolved && (
            <TranscriptPill
              data-copyable-control-text="true"
              className={
                payload.allowed
                  ? "bg-status-running-bg text-status-running"
                  : "bg-destructive/10 text-destructive"
              }
            >
              {payload.allowed
                ? payload.alwaysAllow
                  ? t("canonical.toolPermission.allowedSession")
                  : t("canonical.toolPermission.allowed")
                : t("canonical.toolPermission.denied")}
            </TranscriptPill>
          )}
          <ChevronDown
            className={cn(
              "h-4 w-4 transition-transform",
              collapsed ? "" : "rotate-180",
            )}
          />
        </span>
      </TranscriptCardHeader>

      <div
        aria-hidden={collapsed}
        onTransitionEnd={onTransitionEnd}
        className="grid transition-[grid-template-rows] duration-200 ease-out motion-reduce:transition-none"
        style={{ gridTemplateRows: collapsed ? "0fr" : "1fr" }}
      >
        <div className="min-h-0 overflow-hidden">
          {mounted && inputJson ? (
            <TranscriptCardBody>
              <CollapsibleCode
                value={inputJson}
                surface="muted"
                bodyClassName="rounded-sm px-2.5 py-2"
              />
            </TranscriptCardBody>
          ) : null}
        </div>
      </div>

      {!isResolved && (
        <TranscriptCardBody className="flex flex-wrap items-center gap-2">
          <Button
            size="sm"
            disabled={submitting}
            onClick={() => submit(true, false)}
          >
            <Check className="mr-1 h-3.5 w-3.5" />
            {t("canonical.toolPermission.allowOnce")}
          </Button>
          <Button
            size="sm"
            variant="secondary"
            disabled={submitting}
            onClick={() => submit(true, true)}
          >
            {t("canonical.toolPermission.allowSession")}
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={submitting}
            onClick={() => submit(false, false)}
          >
            <X className="mr-1 h-3.5 w-3.5" />
            {t("common.reject")}
          </Button>
          {error && <span className="text-aux text-destructive">{error}</span>}
        </TranscriptCardBody>
      )}
    </TranscriptCard>
  );
};
