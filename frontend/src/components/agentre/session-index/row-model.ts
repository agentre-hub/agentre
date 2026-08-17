// frontend/src/components/agentre/session-index/row-model.ts
//
// 会话行的**唯一**投影：store 里的事实 → 侧栏行的展示模型。
//
// 合并前这份逻辑写了两遍（chat-page 的 agentSessionFromMeta 与 project-page 的
// projectSessionToAgentSession，规格问题 2），两份的 trailingLabel 分支逐字相同，
// 差异全在数据源与 untitled 文案上 —— 而正是那些差异让同一条会话在两个页面显示不同。
import type { TFunction } from "i18next";

import {
  reasonToDisplayStatus,
  reasonToPillText,
} from "@/lib/attention-display";
import { relativeTime } from "@/lib/relative-time";
import type { AttentionReason } from "@/stores/attention-store";
import type { AgentStatus } from "@/stores/types";

import type { AgentSession } from "../agent-list";

export type IndexRowInput = {
  sessionID: number;
  title: string;
  lastMessageAt: number;
  /** 来自 session-status-store 的原始运行态；缺省 "idle"。 */
  agentStatus: string;
  /** 来自 attention-store 的判定；null = 不需要关注。 */
  reason: AttentionReason | null;
  /**
   * 该行在 attention 气泡里的排位。只有气泡里的行才有；
   * `"selected"` 是「当前打开的会话」这个锚点，包只解释这一个值。
   */
  attentionRank?: AttentionReason | "selected";
  /** 目标地址。桌面端不传（点击是开标签页，没有地址可言）。 */
  href?: string;
  t: TFunction;
};

/**
 * 行尾那一列：优先说「为什么需要你」，没什么可说时才退回相对时间。
 *
 * `running` / `error` 是**状态码**而不是文案，按约定不进 i18n
 * （与包里 statusConfig 的 RUNNING / IDLE 同类）。
 */
function trailingLabel(
  status: AgentStatus,
  reason: AttentionReason | null,
  lastMessageAt: number,
): string {
  if (reason === "bg_running") return reasonToPillText(reason) ?? "";
  if (status === "running") return "running";
  if (status === "waiting") return reasonToPillText(reason) ?? "";
  if (status === "error") return "error";
  return relativeTime(lastMessageAt);
}

export function indexRowFromMeta(input: IndexRowInput): AgentSession {
  const {
    sessionID,
    title,
    lastMessageAt,
    agentStatus,
    reason,
    attentionRank,
    href,
    t,
  } = input;
  const status = reasonToDisplayStatus(
    reason,
    (agentStatus as AgentStatus) || "idle",
  );
  return {
    id: String(sessionID),
    status,
    title: title || t("sessionIndex.untitledSession"),
    trailingLabel: trailingLabel(status, reason, lastMessageAt),
    // 不传时**不写这个键** —— 常规列表行没有 rank，写一个 undefined 进去会让
    // 包里那句 `attentionRank !== "selected"` 的判断读起来像「它有 rank」。
    ...(attentionRank !== undefined ? { attentionRank } : {}),
    ...(href !== undefined ? { href } : {}),
  };
}
