import { useCallback, useState } from "react";

import type { TranscriptBlockToolPermission } from "../../dto";
import { useUiTranslation } from "../../../i18n";
import { useMarkToolPermissionResolved } from "../../live-state";
import { useTranscriptPorts } from "../../ports-context";

import type { CanonicalDTO, ToolPermissionDTO } from "../types";

export function readToolPermission(
  toolBlock: unknown,
): ToolPermissionDTO | undefined {
  const c = (toolBlock as { canonical?: CanonicalDTO }).canonical;
  if (!c || c.kind !== "tool.permission") return undefined;
  return c.toolPermission;
}

/** 块是一条还在等人决定的工具权限请求(提问、计划审批、已决的都不算)。 */
export function isPendingToolPermission(toolBlock: unknown): boolean {
  const payload = readToolPermission(toolBlock);
  return !!payload?.requestId && !payload.resolved;
}

export type ToolPermissionDecisionTarget = {
  toolBlock: unknown;
  sessionId?: number;
  /** 本条请求所属的 assistant 消息 id —— 乐观更新据此定位宿主的实时流。 */
  messageId?: number;
};

// useToolPermissionDecision 是工具权限请求唯一的提交路径:审批卡与停靠组件共用。
// 先乐观地把实时流里的这条标成已决,再经 answerToolPermission 端口回答;端口失败
// 就回滚成未决并把错误露出来。
export function useToolPermissionDecision({
  toolBlock,
  sessionId,
  messageId,
}: ToolPermissionDecisionTarget) {
  const { t } = useUiTranslation();
  const ports = useTranscriptPorts();
  const markToolPermissionResolved = useMarkToolPermissionResolved();
  const payload = readToolPermission(toolBlock);
  const isResolved = !!payload?.resolved;
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

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

  return { payload, isResolved, submitting, error, submit };
}
