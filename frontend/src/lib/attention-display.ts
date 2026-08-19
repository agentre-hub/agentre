// frontend/src/lib/attention-display.ts
//
// attention reason → UI 投影。所有"红还是橙"、"显示什么 pill 文案"决策集中在这里；
// attention-store 不知道这些。
import i18n from "@/i18n";
import type { AttentionReason } from "@/stores/attention-store";
import type { AgentStatus } from "@/stores/types";

export function reasonToDisplayStatus(
  reason: AttentionReason | null,
  fallback: AgentStatus,
): AgentStatus {
  if (reason === "needs_attention" || reason === "unread") return "waiting";
  if (reason === "running" || reason === "bg_running") return "running";
  if (reason === "error") return "error";
  return fallback;
}

export function reasonToPillText(
  reason: AttentionReason | null,
): string | null {
  if (reason === "needs_attention") return i18n.t("attention.needsAttention");
  if (reason === "error") return i18n.t("attention.error");
  if (reason === "unread") return i18n.t("attention.unread");
  if (reason === "bg_running") return i18n.t("attention.background");
  return null;
}

/**
 * 一组 attention 行的**显示档位** → 组头那一枚记号该用哪一档。
 *
 * 组头此前写死 `text-status-running`，可它统计的是全部 attention 条数：3 条未读的项目
 * 显示绿色「3」，而那三行自己画的是琥珀点——组头和它自己的行对不上。这里让两者同源。
 *
 * 优先级刻意**不**沿用 `computeAttention` 的会话内顺序（它把 error 排在 running 之后）：
 * 那是单条会话选 reason 的顺序，拿来做组级取色会让一条出错被三条在跑盖成绿色。
 * 这里按「谁更需要你动手」排：出错要看 > 等你回最挡路 > 在跑只是通报。
 *
 * `idle` 不参与：按定义它就不是需要关注的行。
 */
const TONE_RANK: Record<AgentStatus, number> = {
  error: 3,
  waiting: 2,
  running: 1,
  idle: 0,
};

export function strongestAttentionTone(
  statuses: readonly AgentStatus[],
): AgentStatus | null {
  let best: AgentStatus | null = null;
  for (const status of statuses) {
    if (TONE_RANK[status] === 0) continue;
    if (best === null || TONE_RANK[status] > TONE_RANK[best]) best = status;
  }
  return best;
}
