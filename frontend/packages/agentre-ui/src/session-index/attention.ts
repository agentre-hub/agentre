import type { AgentStatus } from "../transcript/agent-status";

import type { SessionAttentionRank, SessionRowModel } from "./types";

/**
 * 「这条会话需不需要你」的**判定与投影**。两端共用一份。
 *
 * 这一族从桌面端的 `stores/attention-store` + `lib/attention-display` +
 * `session-index/row-model` 搬来。它们本来就是纯函数，而且回答的是同一个问题：
 * 会话索引里的一行该长什么样。留在宿主里的结果是 agentre-server 各自长出了缩水的
 * 复制品 —— `toAgentStatus` 是 `reasonToDisplayStatus` 的一半，未读只剩一个手画的
 * 圆点，attention 气泡里的 rank 干脆硬写成一个字符串常量。
 *
 * **留在宿主的是「事实从哪来」**：桌面端从 session-status-store / session-read-store
 * 读，agentre-server 从账号镜像的那一行读。包只收拢好的输入，不认识任何一端的 store。
 */

/**
 * 一条会话需要关注的五种理由，在「需不需要关注」这一维上**平权** —— 它们只用来选
 * UI 投影（颜色 / pill 文案），不表达严重程度。真正的排序在
 * `strongestAttentionTone`，那是另一个问题（见它自己的说明）。
 */
export type AttentionReason =
  | "needs_attention"
  | "running"
  | "error"
  | "bg_running"
  | "unread";

/** `computeAttention` 的输入：宿主把自己的事实摊成这五格。 */
export type AttentionInput = {
  agentStatus: AgentStatus;
  /** 有待决的审批 / 提问挡在那里。 */
  needsAttention: boolean;
  lastMessageAt: number;
  /** 这个身份最后一次读它的时刻；从没打开过时是 0。 */
  lastReadAt: number;
  /** 这条会话有后台子任务在跑。 */
  bgRunning?: boolean;
};

/**
 * 判定这一行此刻需不需要你，以及为什么。null = 不需要。
 *
 * 顺序不是审美：
 *
 *   - `needs_attention` 压过一切 —— 有东西**挡在那里等你按**，别的都还能等。
 *   - `running` 排在 `error` 之前：跑着的那条随时会自己变成别的，出错的那条不会。
 *     把 error 排前面会让一条正在重跑的会话固定显示上一轮的失败。
 *   - `error` 要**未读**才算：你已经看过的那次失败不该继续拦着你。
 *   - `bg_running` 独立于已读未读（后台子任务与你读没读过它无关），但让位给上面两档。
 *   - `unread` 是最后一档，也是最弱的一档。
 */
export function computeAttention(
  input: AttentionInput,
): AttentionReason | null {
  if (input.needsAttention) return "needs_attention";
  if (input.agentStatus === "running") return "running";
  const unread = input.lastMessageAt > input.lastReadAt;
  if (input.agentStatus === "error" && unread) return "error";
  if (input.bgRunning) return "bg_running";
  if (input.agentStatus === "idle" && unread) return "unread";
  return null;
}

/**
 * reason → 行首那颗点该画哪一档。没有 reason 时退回这一行本来的状态。
 *
 * 「未读」画成 `waiting` 而不是自己一档：状态点只有四种颜色，而未读要说的是
 * 「有东西等你看」—— 与「有东西等你按」同一族，都是在等你，不是在跑也不是出错。
 */
export function reasonToDisplayStatus(
  reason: AttentionReason | null,
  fallback: AgentStatus,
): AgentStatus {
  if (reason === "needs_attention" || reason === "unread") return "waiting";
  if (reason === "running" || reason === "bg_running") return "running";
  if (reason === "error") return "error";
  return fallback;
}

/**
 * reason → 行尾那枚 pill 的文案。取词器由宿主给（包不建 i18next 实例，见
 * `src/i18n`），key 落在包自己的 `agentreUi` namespace 下，两端因此是同一句话。
 *
 * `running` 没有 pill：那一档的行尾写的是状态码 `running` 本身（与 statusConfig 的
 * RUNNING / IDLE 同类，按约定不进 i18n）。null 同理 —— 没有理由的行不需要解释自己。
 */
export function reasonToPillText(
  reason: AttentionReason | null,
  t: (key: string) => string,
): string | null {
  if (reason === "needs_attention") return t("attention.needsAttention");
  if (reason === "error") return t("attention.error");
  if (reason === "unread") return t("attention.unread");
  if (reason === "bg_running") return t("attention.background");
  return null;
}

/**
 * 一组 attention 行的显示档位 → 组头那一枚记号该用哪一档。
 *
 * 优先级刻意**不**沿用 `computeAttention` 的会话内顺序（它把 error 排在 running
 * 之后）：那是单条会话选 reason 的顺序，拿来做组级取色会让一条出错被三条在跑盖成
 * 绿色。这里按「谁更需要你动手」排：出错要看 > 等你回最挡路 > 在跑只是通报。
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

/** `indexRowFromMeta` 的输入。 */
export type IndexRowInput = {
  /** 行的身份。桌面端是会话主键的字符串形，agentre-server 是 conversation_id。 */
  id: string;
  /**
   * 已经**兜过底**的标题。空标题的退化规则各端不同（桌面端一句「未命名会话」，
   * agentre-server 是「工作目录 · 后端 · 状态」），那是宿主的真相，包不编。
   */
  title: string;
  lastMessageAt: number;
  /** 这一行本来的运行态。reason 会在它之上做投影。 */
  agentStatus: AgentStatus;
  reason: AttentionReason | null;
  /** 只有 attention 气泡里的行才有；`"selected"` 是包唯一解释的那个值。 */
  attentionRank?: SessionAttentionRank;
  href?: string;
  /** 相对时间的格式化。locale 在宿主手里，包不猜。 */
  relativeTime: (ms: number) => string;
  /** `agentreUi` namespace 的取词器（`useUiTranslation().t`）。 */
  t: (key: string) => string;
};

/**
 * 行尾那一列：优先说「为什么需要你」，没什么可说时才退回相对时间。
 *
 * `running` / `error` 是**状态码**而不是文案，按约定不进 i18n（与 statusConfig 的
 * RUNNING / IDLE 同类）。
 */
function attentionTrailingLabel(
  status: AgentStatus,
  reason: AttentionReason | null,
  lastMessageAt: number,
  relativeTime: (ms: number) => string,
  t: (key: string) => string,
): string {
  if (reason === "bg_running") return reasonToPillText(reason, t) ?? "";
  if (status === "running") return "running";
  if (status === "waiting") return reasonToPillText(reason, t) ?? "";
  if (status === "error") return "error";
  return relativeTime(lastMessageAt);
}

/**
 * 会话行的**唯一**投影：宿主收拢好的事实 → `SessionRow` 认的展示模型。
 *
 * 宿主自己的插槽（`leading` / `secondaryLabel` / `trailing` / `rowActions`）不在这里：
 * 它们是宿主决定放什么的地方，投影完再摊上去即可。
 */
export function indexRowFromMeta(input: IndexRowInput): SessionRowModel {
  const {
    id,
    title,
    lastMessageAt,
    agentStatus,
    reason,
    attentionRank,
    href,
    relativeTime,
    t,
  } = input;
  const status = reasonToDisplayStatus(reason, agentStatus);
  return {
    id,
    status,
    title,
    trailingLabel: attentionTrailingLabel(
      status,
      reason,
      lastMessageAt,
      relativeTime,
      t,
    ),
    // 不传时**不写这个键** —— 常规列表行没有 rank，写一个 undefined 进去会让
    // 包里那句 `attentionRank !== "selected"` 的判断读起来像「它有 rank」。
    ...(attentionRank !== undefined ? { attentionRank } : {}),
    ...(href !== undefined ? { href } : {}),
  };
}
