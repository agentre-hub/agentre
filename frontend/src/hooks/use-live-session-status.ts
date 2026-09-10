// use-live-session-status.ts
//
// 薄壳代理 —— 内部优先走 useSessionWithOverlays，没有 meta 时退化为直读
// session-status-store（保持与旧消费方行为完全兼容）。
//
// 导出名保持不变，消费方 (project-page / agent-list 等) 无需改动。
import { useMemo } from "react";

import { useSessionStatusStore } from "@/stores/session-status-store";
import { useSessionWithOverlays } from "./use-session-with-overlays";

// EffectiveSessionStatus 是列表态行渲染需要的最小 status 投影。
export type EffectiveSessionStatus = {
  agentStatus: string;
  needsAttention: boolean;
};

// useEffectiveSessionStatus 给列表 / sidebar / popover 行用：
// 优先读 useSessionWithOverlays（meta + status 都在），meta 缺失时回退到
// 直读 session-status-store，store 也没有时返回 fallback 快照。
export function useEffectiveSessionStatus(
  sessionId: number,
  fallback: EffectiveSessionStatus,
): EffectiveSessionStatus {
  const view = useSessionWithOverlays(sessionId);
  // view 为 null 时（meta 还没到位），退化为直读 status store，保持旧行为
  const liveStatus = useSessionStatusStore((s) =>
    view === null && sessionId > 0 ? (s.statuses.get(sessionId) ?? null) : null,
  );
  return useMemo(() => {
    if (view !== null) {
      // view 存在时用 overlay 合并后的值
      if (
        view.agentStatus === fallback.agentStatus &&
        view.needsAttention === fallback.needsAttention
      ) {
        return fallback;
      }
      return {
        agentStatus: view.agentStatus,
        needsAttention: view.needsAttention,
      };
    }
    if (!liveStatus) return fallback;
    if (
      liveStatus.agentStatus === fallback.agentStatus &&
      liveStatus.needsAttention === fallback.needsAttention
    ) {
      return fallback;
    }
    return {
      agentStatus: liveStatus.agentStatus,
      needsAttention: liveStatus.needsAttention,
    };
  }, [view, liveStatus, fallback]);
}
