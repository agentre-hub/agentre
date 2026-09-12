// frontend/src/components/agentre/session-index/row-model.ts
//
// 会话行投影的**宿主适配层**：store 里的事实 → 包认的输入。
//
// 合并前这份逻辑写了两遍（chat-page 的 agentSessionFromMeta 与 project-page 的
// projectSessionToAgentSession，规格问题 2），两份的 trailingLabel 分支逐字相同，
// 差异全在数据源与 untitled 文案上 —— 而正是那些差异让同一条会话在两个页面显示不同。
//
// 投影本身现在住在共享包里（`@agentre-hub/agentre-ui` 的 session-index/attention）：
// agentre-server 问的是同一个问题，此前它各画了一份缩水的。留在这里的只有三件宿主
// 独有的事：会话主键是 number、空标题的退化文案、以及本仓库那份相对时间格式化。
import {
  indexRowFromMeta as indexRowFromMetaShared,
  type AttentionReason,
  type SessionAttentionRank,
} from "@agentre-hub/agentre-ui";
import type { TFunction } from "i18next";

import { uiT } from "@/lib/attention-display";
import { relativeTime } from "@/lib/relative-time";
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
  attentionRank?: SessionAttentionRank;
  /** 目标地址。桌面端不传（点击是开标签页，没有地址可言）。 */
  href?: string;
  t: TFunction;
};

export function indexRowFromMeta(input: IndexRowInput): AgentSession {
  const { sessionID, title, agentStatus, t, ...rest } = input;
  return indexRowFromMetaShared({
    ...rest,
    id: String(sessionID),
    // 空标题的退化文案是宿主的真相，包不编 —— agentre-server 那一端的退化规则
    // 是「工作目录 · 后端 · 状态」，与这一句毫无关系。
    title: title || t("sessionIndex.untitledSession"),
    agentStatus: (agentStatus as AgentStatus) || "idle",
    relativeTime,
    // 包的 pill 文案落在它自己的 namespace 下，绑定只有 attention-display 那一处。
    t: uiT,
  });
}
