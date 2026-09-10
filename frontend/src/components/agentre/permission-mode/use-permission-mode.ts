import * as React from "react";

import { SetChatPermissionMode } from "@/../wailsjs/go/app/App";

import type { PermissionModeMeta } from "../capability/types";

import {
  isPermissionModeDisabled,
  nextPermissionMode,
  normalizePermissionMode,
  type PermissionMode,
} from "@agentre-hub/agentre-ui";

export interface UsePermissionModeOptions {
  /** chat session id;0 / null / undefined = 当前没选会话 */
  sessionId: number | undefined | null;
  /**
   * 当前会话 runtime 的 PermissionModeMeta(allowedModes / defaultMode /
   * switchableDuringTurn / order),由 useSessionCapabilities() 拉取。
   */
  permissionModeMeta: PermissionModeMeta;
  /**
   * runtime key(claudecode/codex/builtin/remote)。仅供 permissionModeDisabledReason
   * 判定 claudecode bypass-lockout 规则使用,不参与 mode 集合或默认值决策。
   * 留空时 disabled-reason 永远返回 null。
   */
  runtimeKey?: string | null;
  /**
   * 来自 ChatSessionDetail.permissionMode;空串 / undefined / null = 用户从未
   * 切换过,按 backend 默认值显示。父组件在 LoadSession 拿到 session 之后传入;
   * session 切换时本 hook 用 effect 重新同步。
   */
  initialMode: PermissionMode | null | undefined;
  /**
   * Session 的 permission_mode_at_launch(spawn 时下发的快照)。仅传透给 pill
   * 让它正确禁用 bypass;hook 自身在 cycle 时也消费它(跳过被禁用 mode)。
   */
  initialModeAtLaunch?: PermissionMode | null;
  /** 是否已经 spawn 过 CLI(messages.length > 0 之类的派生判定)。 */
  hasActiveSession?: boolean;
  /**
   * Backend 管理员预设的 default permission mode(仅 claudecode 非空)。
   * 来自 ChatAgentItem.defaultPermissionMode。raw 为空时回落到这里,确保起手值
   * 与后端 spawn 时一致。
   */
  backendDefaultMode?: PermissionMode | null;
}

export interface UsePermissionModeReturn {
  mode: PermissionMode;
  error: string | null;
  setMode: (mode: PermissionMode) => void;
  cycleMode: () => void;
  permissionModeAtLaunch: PermissionMode | null | undefined;
  hasActiveSession: boolean;
}

/**
 * usePermissionMode: 串起当前 chat session 的 permission mode 状态 + 后端
 * SetChatPermissionMode 调用。
 *
 * 数据流:
 *  - DB (chat_sessions.permission_mode) 是 source-of-truth;父组件从 LoadSession
 *    拿到 detail.permissionMode 后通过 initialMode 注入。
 *  - 切换走 SetChatPermissionMode IPC:后端 DB-first 持久化,再 best-effort 下
 *    发到活跃 CLI;CLI 不在时不报错。
 *  - 乐观更新:先 setState 让 pill 即刻反映;失败时回滚到上一次成功值并 setError。
 */
export function usePermissionMode({
  sessionId,
  permissionModeMeta,
  runtimeKey,
  initialMode,
  initialModeAtLaunch,
  hasActiveSession,
  backendDefaultMode,
}: UsePermissionModeOptions): UsePermissionModeReturn {
  const { allowedModes, defaultMode } = permissionModeMeta;

  const [mode, setModeState] = React.useState<PermissionMode>(() =>
    normalizePermissionMode(
      initialMode,
      allowedModes,
      defaultMode,
      backendDefaultMode,
    ),
  );
  const [error, setError] = React.useState<string | null>(null);
  const lastModeRef = React.useRef<PermissionMode>(mode);

  // session 切换或 caps 加载完成时,把 state 重置到 DB 当前值。
  React.useEffect(() => {
    const next = normalizePermissionMode(
      initialMode,
      allowedModes,
      defaultMode,
      backendDefaultMode,
    );
    setModeState(next);
    lastModeRef.current = next;
    setError(null);
  }, [sessionId, initialMode, allowedModes, defaultMode, backendDefaultMode]);

  const setMode = React.useCallback(
    (next: PermissionMode) => {
      if (next === mode) return;
      const previous = lastModeRef.current;
      setModeState(next);
      lastModeRef.current = next;
      setError(null);

      if (!sessionId || sessionId <= 0) {
        return;
      }
      void SetChatPermissionMode({ sessionId, mode: next } as never)
        .then(() => {
          setError(null);
        })
        .catch((e: unknown) => {
          const msg = e instanceof Error ? e.message : String(e);
          console.error("[permission-mode] set failed", e);
          setModeState(previous);
          lastModeRef.current = previous;
          setError(msg);
        });
    },
    [mode, sessionId],
  );

  const cycleMode = React.useCallback(() => {
    const order = permissionModeMeta.order;
    if (order.length === 0) return;
    const ctx = {
      hasActiveSession: hasActiveSession ?? false,
      permissionModeAtLaunch: initialModeAtLaunch ?? null,
    };
    // 跳过被 isPermissionModeDisabled 判定为禁用的档(claudecode 的 bypass
    // 锁死场景);最坏情况绕一圈回到起点(理论上不会全 disabled)。
    let curr = lastModeRef.current;
    for (let step = 0; step < order.length; step++) {
      const cand = nextPermissionMode(curr, order);
      if (!isPermissionModeDisabled(cand, runtimeKey, ctx)) {
        setMode(cand);
        return;
      }
      curr = cand;
    }
    // 全部都 disabled — 回退到 setMode(order[0]) 不会触发任何 setState。
  }, [
    permissionModeMeta.order,
    runtimeKey,
    hasActiveSession,
    initialModeAtLaunch,
    setMode,
  ]);

  return {
    mode,
    error,
    setMode,
    cycleMode,
    permissionModeAtLaunch: initialModeAtLaunch ?? null,
    hasActiveSession: hasActiveSession ?? false,
  };
}
