// frontend/src/components/agentre/session-index/index-projection.ts
//
// 桌面端的组骨架 ⇄ 共享轴投影（`@agentre-ai/agentre-ui` 的 `buildAxisGroups`）之间
// 的适配层（规格：docs/specs/2026-08-18-org-index-convergence.md「共享包承载什么」）。
//
// 两边说的不是一套话，这一层就是唯一的翻译处：
//   - **组的词汇**：桌面端是 project / agent / free / flat，共享包是 project /
//     agent / machine / all / unassignedProject / unnamedAgent。翻译只在
//     `sharedGroupKey` 这一个地方发生。
//   - **行的形状**：桌面端手里只有 `sessionIDs: number[]`（行字段一律从 store 现算，
//     见 use-group-rows.ts），共享投影收的是 `IndexRow[]`。这里把 id 补成行喂进去，
//     再把投影分好的组换回 id —— 桌面端的行渲染仍旧走 store，一条没改。
//   - **总数**：桌面端每个轴各有一条分页查询，「查看全部 N」的 N 只有宿主数得出来。
//     它经 `AxisInput.totals`（组键 → 总数）进去，再从 `IndexGroup.total` 回来。
//
// **组骨架仍归宿主**：哪些组存在、按什么顺序排，是「每轴一条查询」这件事决定的
// （项目轴按项目树摊平、Agent 轴置顶优先 + 最近活动、时间轴单组），而且桌面端要摆
// 空项目组、要常驻「随手对话」（决策 6）—— 共享投影按决策 10 只摆有会话的组，
// 它给不出这些空组。所以这里是「骨架由宿主给、组内的分配 / 补齐 / 排序由投影做」：
// 投影没给出的组（= 一条会话都没有）落回空列表，宿主的总数照旧。
import {
  buildAxisGroups,
  UNASSIGNED_PROJECT_KEY,
  type AxisInput,
  type IndexAxis,
  type IndexRow,
} from "@agentre-ai/agentre-ui";

export type IndexGroupKind = "project" | "agent" | "free" | "flat" | "machine";

export type IndexGroup = {
  /** React key，同时是折叠状态的 localStorage 命名空间（"project:7" / "agent:3"）。 */
  key: string;
  kind: IndexGroupKind;
  /** 项目 id / agent id / 设备 id（0 = 本机）；free 与 flat 恒为 0。 */
  refID: number;
  /** 项目树的缩进层级；其余轴恒为 0。 */
  depth: number;
  /** 已加载的会话 id，最近活动优先。 */
  sessionIDs: number[];
  /**
   * 常规列表专用 id（agent 轴 = ListChatAgents 的「前 5 条」；其余轴缺省 = sessionIDs）。
   * 气泡候选池始终是 sessionIDs —— agent 轴的 attention 池（running/waiting/error）只该
   * 喂气泡，不能把已读的 error 一起摊进常规列表（规格「每组的会话 = 前 5 条 + attention」）。
   *
   * 它是**组骨架的一部分**（哪几条属于常规列表，只有取数的宿主分得清），所以和 key /
   * kind 一样由投影原样带过，不参与组内的重排。
   */
  recentIDs?: number[];
  /** 该组的会话总数。大于已加载数时渲染「查看全部 N」。 */
  total: number;
};

/** 行事实的来源，只取投影用得上的两列（宿主传 session-meta-store 的 metas）。 */
export type IndexRowFacts = { title?: string; lastMessageAt?: number };

/**
 * 时间轴那一组在共享投影里的键。包里是个字面量、没随三个兜底键一起导出
 * （axis-groups.ts 的 `key: "__all__"`），所以这里抄一份；抄错了「时间轴带回
 * 分页总数」那条测试就会红，不会悄悄退化成没有分页。
 */
const SHARED_TIME_GROUP_KEY = "__all__";

/** 桌面端的组 → 它在共享投影里的组键。两套词汇的唯一映射点。 */
function sharedGroupKey(kind: IndexGroupKind, refID: number): string {
  switch (kind) {
    case "project":
      return String(refID);
    case "agent":
      return String(refID);
    case "machine":
      // 共享投影的机器组键就是 `device-<设备标识>`（axis-groups 的 machineGroups）。
      // 0 = 本机在这里没有任何特殊待遇 —— 它就是一台机器。
      return `device-${refID}`;
    case "free":
      return UNASSIGNED_PROJECT_KEY;
    case "flat":
      return SHARED_TIME_GROUP_KEY;
  }
}

/**
 * 行的身份。桌面端的身份就是 sessionId（本机一台，没有指纹这一维），前面再钉一个
 * 页内序号：投影按最后活动时间倒序、同一时间比 `key`，钉上序号就让「时间相同」退回
 * **服务端给的那一页的顺序**，而不是 id 字符串的字典序。
 */
function rowKey(rank: number, sessionID: number): string {
  return `${String(rank).padStart(6, "0")}:${sessionID}`;
}

/**
 * 把宿主的组骨架过一遍共享投影：组内的会话由投影分配与排序，组本身原样保留。
 *
 * `axis` 用桌面端的三档（`@/lib/session-axis` 的 IndexAxis，是共享词汇表的子集），
 * `slots` 的 `sessionIDs` 是各轴查询刚取回来的那一页。
 */
export function projectIndexGroups(
  axis: IndexAxis,
  slots: readonly IndexGroup[],
  metas: ReadonlyMap<number, IndexRowFacts>,
): IndexGroup[] {
  const rows: IndexRow[] = [];
  const totals: Record<string, number> = {};

  for (const slot of slots) {
    const groupKey = sharedGroupKey(slot.kind, slot.refID);
    totals[groupKey] = slot.total;
    slot.sessionIDs.forEach((sessionID, rank) => {
      const meta = metas.get(sessionID);
      rows.push({
        key: rowKey(rank, sessionID),
        sessionId: sessionID,
        // 桌面端的会话都是本机发起的，没有指纹这一维可填。
        fingerprint: "",
        // 会话归哪个 Agent / 哪个项目，桌面端是**取数时就知道**的（每个组各有一条
        // 查询），不必回头去猜 meta —— meta 还没到位的行会被猜到别的组里去。
        agentSyncId: slot.kind === "agent" ? groupKey : "",
        projectSyncId: slot.kind === "project" ? groupKey : "",
        // 会话跑在哪台机器上同样是**取数时就知道**的（机器轴每台机器一条查询）。
        // 别的轴不给这一维：那三条查询不按机器取数，猜一个会把行分到错的机器上。
        deviceId: slot.kind === "machine" ? slot.refID : undefined,
        updatedAt: meta?.lastMessageAt ?? 0,
        title: meta?.title ?? "",
        // 运行态在 session-status-store，行渲染仍从那里现算（use-group-rows.ts）。
        // 投影不看这个字段，为了填一个没人读的值让整条侧栏跟着每次流式状态变动
        // 重渲，不值当。
        lifecycleState: "",
      });
    });
  }

  const input: AxisInput = {
    rows,
    // 项目 / Agent 名单在投影里只用来给组**起名字、上色、排序**，而这三件事桌面端
    // 都不从这里拿：名字与颜色由 index-page 按 refID 查树与 agent 列表，顺序是下面
    // 那句「骨架说了算」。机器名单同理：组头的机器名与在线态由 index-group-row 从
    // 名单（machine-roster.ts）自己查，投影只需要把行分进 `device-<id>` 里。
    projects: [],
    agents: [],
    machines: [],
    totals,
  };
  const projected = new Map(
    buildAxisGroups(axis, input).map((group) => [group.key, group]),
  );

  return slots.map((slot) => {
    const group = projected.get(sharedGroupKey(slot.kind, slot.refID));
    // 投影里没有这一组 = 这一组一条会话都没有（决策 10）。宿主照摆，空着，
    // 总数用宿主自己那一份 —— 只有这一种情况回退，别的情况总数一律**从投影回来**，
    // 这样 totals 没接上时会当场变成 0（「查看全部 N」消失），而不是悄悄退回宿主值。
    if (!group) return { ...slot, sessionIDs: [] };
    return {
      ...slot,
      sessionIDs: group.rows.map((row) => row.sessionId),
      total: group.total ?? 0,
    };
  });
}
