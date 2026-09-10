// frontend/src/stores/attention-store.ts
//
// attention-store 是「这个 session 当前是否需要用户关注」的唯一计算源。
// 所有 UI 表面（侧栏 / tabs / 命令面板 / toolbar）只走 useSessionAttention。
// 4 个 reason 在 isAttention 维度平权，只用来选 UI 投影（色 / 文案）。
//
// computeAttention 是纯函数，单测主力；
// useSessionAttention 站在 useSessionWithOverlays 之上。
import { computeAttention } from "@agentre-hub/agentre-ui";
import * as React from "react";

import { useSessionWithOverlays } from "@/hooks/use-session-with-overlays";
import { useSessionMetaStore } from "@/stores/session-meta-store";
import { useSessionReadStore } from "@/stores/session-read-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

// AttentionReason, AttentionInput 类型定义已统一到 @/stores/types，此处 import + re-export。
import type { AttentionReason, AttentionInput } from "./types";
export type { AttentionReason, AttentionInput };

// useStableArray: 内容稳定化 hook —— 当新数组与旧数组元素相同（长度 + 逐元素 ===）
// 时，返回旧引用，避免内容未变但引用不同触发下游 useMemo 重算。
// 适用场景：消费方每次 render 都生成新数组字面量但内容可能相同（如 agent.sessionIds）。
function useStableArray<T>(arr: readonly T[]): readonly T[] {
  const [stable, setStable] = React.useState<readonly T[]>(() => arr);
  if (stable.length !== arr.length || stable.some((v, i) => v !== arr[i])) {
    setStable(arr);
  }
  return stable;
}

// computeAttention 的判定住在共享包里（`@agentre-hub/agentre-ui` 的
// session-index/attention）：它是纯函数，而 agentre-server 问的是同一个问题。
// 原样转出来，本仓库的 import 路径与单测一个都不用改；下面两个 hook 自己也用它。
export { computeAttention };

export function useSessionAttention(sessionId: number): {
  reason: AttentionReason | null;
  isAttention: boolean;
} {
  const view = useSessionWithOverlays(sessionId);
  return React.useMemo(() => {
    if (!view) return { reason: null, isAttention: false };
    const reason = computeAttention({
      agentStatus: view.agentStatus,
      needsAttention: view.needsAttention,
      lastMessageAt: view.lastMessageAt,
      lastReadAt: view.lastReadAt,
      bgRunning: view.bgRunning,
    });
    return { reason, isAttention: reason !== null };
  }, [view]);
}

export function useSessionAttentionList(
  sessionIds: readonly number[],
): Array<{ sessionId: number; reason: AttentionReason }> {
  // 内容稳定化：消费方（如 agent.sessionIds）每次 agent 对象重建都是新数组引用，
  // 即使内容未变也会触发下游 useMemo 重算。useStableArray 保证内容相同时返回旧引用。
  const stable = useStableArray(sessionIds);
  const metas = useSessionMetaStore((s) => s.metas);
  const statuses = useSessionStatusStore((s) => s.statuses);
  const overrides = useSessionReadStore((s) => s.overrides);

  return React.useMemo(() => {
    const out: Array<{ sessionId: number; reason: AttentionReason }> = [];
    for (const sid of stable) {
      const meta = metas.get(sid);
      if (!meta) continue;
      const status = statuses.get(sid);
      const lastReadAt = Math.max(
        meta.lastReadAt ?? 0,
        overrides.get(sid) ?? 0,
      );
      const reason = computeAttention({
        agentStatus: status?.agentStatus ?? "idle",
        needsAttention: status?.needsAttention ?? false,
        lastMessageAt: meta.lastMessageAt ?? 0,
        lastReadAt,
        bgRunning: status?.bgRunning ?? false,
      });
      if (reason !== null) out.push({ sessionId: sid, reason });
    }
    return out;
  }, [stable, metas, statuses, overrides]);
}
