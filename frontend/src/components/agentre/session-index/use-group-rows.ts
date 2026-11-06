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
import { useShallow } from "zustand/react/shallow";

import { attentionReasonFor } from "@/stores/attention-store";
import type { AttentionReason } from "@/stores/attention-store";
import {
  useSessionMetaStore,
  type SessionMeta,
} from "@/stores/session-meta-store";
import { useSessionReadStore } from "@/stores/session-read-store";
import {
  useSessionStatusStore,
  type SessionStatusValue,
} from "@/stores/session-status-store";

import type { AgentSession } from "../agent-list";
import { indexRowFromMeta } from "./row-model";
import { useMinuteClock } from "./use-minute-clock";

export type GroupRows = {
  /** 常规列表。SessionGroup 会自动与气泡去重。 */
  sessions: AgentSession[];
  /** attention 气泡：折叠态下这是这一组唯一可见的入口。 */
  attentionSessions: AgentSession[];
};

const EMPTY: GroupRows = { sessions: [], attentionSessions: [] };

/**
 * 只订阅这一批 id 在 meta-store 里的条目。整张 map 每次任何会话变动都换引用，
 * 订阅它会让每个组在别的组的会话变化时也重算；这里按 id 取值并 shallow 比较，
 * 只有本批 id 的条目真的换了才通知。
 */
export function useMetaSlice(
  ids: readonly number[],
): ReadonlyMap<number, SessionMeta> {
  const values = useSessionMetaStore(
    useShallow((s) => ids.map((id) => s.metas.get(id))),
  );
  return React.useMemo(() => {
    const out = new Map<number, SessionMeta>();
    ids.forEach((id, i) => {
      const v = values[i];
      if (v) out.set(id, v);
    });
    return out;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [values]);
}

function useStatusSlice(ids: readonly number[]) {
  return useSessionStatusStore(
    useShallow((s) => ids.map((id) => s.statuses.get(id))),
  );
}

function useReadOverrideSlice(ids: readonly number[]) {
  return useSessionReadStore(
    useShallow((s) => ids.map((id) => s.overrides.get(id))),
  );
}

type RowCacheEntry = {
  meta: SessionMeta;
  status: SessionStatusValue | undefined;
  reason: AttentionReason | null;
  rank: AgentSession["attentionRank"];
  t: TFunction;
  minute: number;
  row: AgentSession;
};

export function useGroupRows(
  sessionIDs: readonly number[],
  selectedSessionID: number,
  t: TFunction,
  opts?: {
    /**
     * 气泡候选池：缺省 = sessionIDs。agent 轴把「前 5 条常规列表」与
     * attention 池（前 5 + running/waiting/error）分开传 —— 已读 error 只可能被
     * 气泡的 unread 闸门放行，而不会从池里漏进常规列表（规格问题，sess-1819）。
     */
    attentionPoolIDs?: readonly number[];
  },
): GroupRows {
  // 气泡只从 attention 池算；常规列表只用 sessionIDs。两者合一（其余轴）时行为不变。
  const poolIDs = opts?.attentionPoolIDs ?? sessionIDs;
  // 本组关心的 id = 池 ∪ 常规列表；订阅只覆盖这一批。
  const allIDs = React.useMemo(
    () => Array.from(new Set([...poolIDs, ...sessionIDs])),
    [poolIDs, sessionIDs],
  );
  const metas = useMetaSlice(allIDs);
  const statusValues = useStatusSlice(allIDs);
  const overrideValues = useReadOverrideSlice(allIDs);
  // 未变的行复用同一个对象：下游（行 memo）靠引用判断「这一行没动」。
  const [rowCache] = React.useState(() => new Map<string, RowCacheEntry>());
  // 行尾相对时间按分钟走:跨过一分钟时整组重建一次行(行对象换新,行 memo 随之重渲染)。
  const minute = useMinuteClock();

  return React.useMemo(() => {
    if (poolIDs.length === 0) return EMPTY;

    const statuses = new Map<number, SessionStatusValue | undefined>();
    const overrides = new Map<number, number | undefined>();
    allIDs.forEach((id, i) => {
      statuses.set(id, statusValues[i]);
      overrides.set(id, overrideValues[i]);
    });

    // attention 判定：与 useSessionAttentionList 同一个纯函数、同一个口径，
    // 只是输入限定在本组的 id。
    const reasons = new Map<number, AttentionReason>();
    for (const sid of poolIDs) {
      const meta = metas.get(sid);
      if (!meta) continue;
      const reason = attentionReasonFor(
        meta,
        statuses.get(sid),
        overrides.get(sid),
      );
      if (reason !== null) reasons.set(sid, reason);
    }
    const attentionItems = Array.from(reasons, ([sessionId, reason]) => ({
      sessionId,
      reason,
    }));
    const cache = rowCache;
    const seen = new Set<string>();

    const build = (
      sid: number,
      attentionRank?: AgentSession["attentionRank"],
    ): AgentSession | null => {
      const meta = metas.get(sid);
      // meta 缺失 = 这条会话还没被任何一条列表查询覆盖到。如实跳过而不是渲染一行
      // 「无标题 / idle / 空时间」的假行 —— 那种行点进去才发现是别的会话。
      if (!meta) return null;
      const status = statuses.get(sid);
      const reason = reasons.get(sid) ?? null;
      const cacheKey = `${sid}:${attentionRank ?? ""}`;
      seen.add(cacheKey);
      const hit = cache.get(cacheKey);
      if (
        hit &&
        hit.meta === meta &&
        hit.status === status &&
        hit.reason === reason &&
        hit.t === t &&
        hit.minute === minute
      ) {
        return hit.row;
      }
      const row = indexRowFromMeta({
        sessionID: sid,
        title: meta.title ?? "",
        lastMessageAt: meta.lastMessageAt ?? 0,
        agentStatus: status?.agentStatus ?? "idle",
        reason,
        ...(attentionRank !== undefined ? { attentionRank } : {}),
        t,
      });
      cache.set(cacheKey, {
        meta,
        status,
        reason,
        rank: attentionRank,
        t,
        minute,
        row,
      });
      return row;
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
      poolIDs.includes(selectedSessionID) &&
      !reasons.has(selectedSessionID)
    ) {
      const anchor = build(selectedSessionID, "selected");
      if (anchor) attentionSessions.push(anchor);
    }

    const sessions = sessionIDs.flatMap((sid) => {
      const row = build(sid);
      return row ? [row] : [];
    });

    for (const key of cache.keys()) if (!seen.has(key)) cache.delete(key);
    return { sessions, attentionSessions };
  }, [
    sessionIDs,
    poolIDs,
    allIDs,
    rowCache,
    metas,
    statusValues,
    overrideValues,
    selectedSessionID,
    t,
    minute,
  ]);
}
