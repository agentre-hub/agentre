// frontend/src/components/agentre/session-index/use-group-rows.ts
//
// 一个组的 session id → 两串行：常规列表 + attention 气泡。
//
// 合并前这段在对话页（useBuildSessions / useBuildAttentionSessions）与项目页
// （attentionRows / top5AgentSessions）各写了一份，且项目页那份**绕开 meta-store
// 内联调用 computeAttention** —— 因为它的会话来自另一条 RPC，没在 meta-store 注册过，
// useSessionAttentionList 的 `if (!meta) continue` 会把它们全部跳过（规格问题 3）。
//
// 数据通路收敛之后那条绕行没有了：索引的每一页都会落进 meta-store，attention 因此
// 只算一次、只有 store 里那一处。
import * as React from "react";
import type { TFunction } from "i18next";

import { useSessionAttentionList } from "@/stores/attention-store";
import { useSessionMetaStore } from "@/stores/session-meta-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import type { AgentSession } from "../agent-list";
import { indexRowFromMeta } from "./row-model";

export type GroupRows = {
  /** 常规列表。SessionGroup 会自动与气泡去重。 */
  sessions: AgentSession[];
  /** attention 气泡：折叠态下这是这一组唯一可见的入口。 */
  attentionSessions: AgentSession[];
};

const EMPTY: GroupRows = { sessions: [], attentionSessions: [] };

export function useGroupRows(
  sessionIDs: readonly number[],
  selectedSessionID: number,
  t: TFunction,
): GroupRows {
  const metas = useSessionMetaStore((s) => s.metas);
  const statuses = useSessionStatusStore((s) => s.statuses);
  const attentionItems = useSessionAttentionList(sessionIDs);

  return React.useMemo(() => {
    if (sessionIDs.length === 0) return EMPTY;

    const reasons = new Map(
      attentionItems.map((item) => [item.sessionId, item.reason]),
    );

    const build = (
      sid: number,
      attentionRank?: AgentSession["attentionRank"],
    ): AgentSession | null => {
      const meta = metas.get(sid);
      // meta 缺失 = 这条会话还没被任何一条列表查询覆盖到。如实跳过而不是渲染一行
      // 「无标题 / idle / 空时间」的假行 —— 那种行点进去才发现是别的会话。
      if (!meta) return null;
      return indexRowFromMeta({
        sessionID: sid,
        title: meta.title ?? "",
        lastMessageAt: meta.lastMessageAt ?? 0,
        agentStatus: statuses.get(sid)?.agentStatus ?? "idle",
        reason: reasons.get(sid) ?? null,
        ...(attentionRank !== undefined ? { attentionRank } : {}),
        t,
      });
    };

    // 气泡：按最近活动倒序（不是按 reason 优先级 —— 五个 reason 平权，
    // 用它们排序会让「刚动过的那条」被一条更早的审批压下去）。
    const attentionIDs = attentionItems
      .map((item) => item.sessionId)
      .sort(
        (a, b) =>
          (metas.get(b)?.lastMessageAt ?? 0) -
          (metas.get(a)?.lastMessageAt ?? 0),
      );
    const attentionSessions = attentionIDs.flatMap((sid) => {
      const row = build(sid, reasons.get(sid));
      return row ? [row] : [];
    });

    // 当前打开的会话若不在 attention 池里，钉到气泡末尾 —— 折叠态下它是唯一能看见
    // 「我现在开着哪条」的地方。rank 用 "selected"，展开态包会把它剔回常规列表。
    if (
      selectedSessionID > 0 &&
      sessionIDs.includes(selectedSessionID) &&
      !reasons.has(selectedSessionID)
    ) {
      const anchor = build(selectedSessionID, "selected");
      if (anchor) attentionSessions.push(anchor);
    }

    const sessions = sessionIDs.flatMap((sid) => {
      const row = build(sid);
      return row ? [row] : [];
    });

    return { sessions, attentionSessions };
  }, [sessionIDs, attentionItems, metas, statuses, selectedSessionID, t]);
}
