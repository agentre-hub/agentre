import { useCallback, useRef, useState } from "react";

import type { TranscriptBlockToolPermission } from "../../dto";
import { useUiTranslation } from "../../../i18n";
import { failureDetailText } from "../../failure-detail";
import { useMarkToolPermissionResolved } from "../../live-state";
import { useTranscriptPorts } from "../../ports-context";
import { isTimeoutFailure } from "../../../lib/timeout-failure";

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

type Decision = { allow: boolean; alwaysAllowSession: boolean };

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
  // error 是本地化事实句;errorDetail 是机器原话,调用方只把它放进 title。
  const [error, setError] = useState<string | null>(null);
  const [errorDetail, setErrorDetail] = useState<string | undefined>();
  // 超时与其他失败分开:宿主据此写「没收到确认,可能已经生效」。
  const [timedOut, setTimedOut] = useState(false);
  // 点下的那一档与上一次的决定,给「点下的那颗单独转圈」与「重试」用。
  const [pending, setPending] = useState<Decision | null>(null);
  const lastRef = useRef<Decision | null>(null);

  const submit = useCallback(
    async (allow: boolean, alwaysAllowSession: boolean) => {
      if (!payload || !payload.requestId || !sessionId) return;
      if (isResolved || submitting) return;
      const decision = { allow, alwaysAllowSession };
      lastRef.current = decision;
      setPending(decision);
      setError(null);
      setErrorDetail(undefined);
      setTimedOut(false);
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
        setError(t("canonical.toolPermission.submitFailed"));
        setErrorDetail(failureDetailText(err) || undefined);
        setTimedOut(isTimeoutFailure(err));
      } finally {
        setPending(null);
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

  // retry 重放上一次的决定;没有失败过就什么都不做。
  const retry = useCallback(() => {
    const last = lastRef.current;
    if (last) void submit(last.allow, last.alwaysAllowSession);
  }, [submit]);

  return {
    payload,
    isResolved,
    submitting,
    pending,
    error,
    errorDetail,
    timedOut,
    submit,
    retry,
  };
}
