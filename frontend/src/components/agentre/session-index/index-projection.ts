// frontend/src/components/agentre/session-index/index-projection.ts
//
// 桌面端的组骨架 ⇄ 共享轴投影（`@agentre-hub/agentre-ui` 的 `buildAxisGroups`）之间
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
// **组的存亡归共享投影，宿主只摆骨架与排序**：`AxisInput.machines` / `agents` 是
// 共享投影的**已知组名单**（决策 10 / 11），名单里的机器 / Agent 即使没有行也成组，
// 「随手对话」更是常驻（决策 12）。宿主交来的 `slots` 只承载「每轴一条查询」取到的
// 页与只属于宿主的维度（项目树的缩进、agent 轴的 recentIDs）；名单里还没有页的机器
// 由投影自己成组。机器轴的排序是唯一的例外：投影按「在线优先、名字、设备号」排，
// 桌面端再按 roster 的「本机第一、其余在线优先」名次排回去（machine-roster.ts 的
// `machineRosterRank`）。
import {
  buildAxisGroups,
  UNASSIGNED_PROJECT_KEY,
  type AgentInfo,
  type AxisInput,
  type IndexAxis,
  type IndexGroup as SharedIndexGroup,
  type IndexRow,
  type MachineInfo,
} from "@agentre-hub/agentre-ui";

import { machineRosterRank } from "./machine-roster";

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
 * 共享投影的已知组名单：机器用宿主的 roster（顺序也是本机优先的 rank 依据），
 * Agent 用宿主的 name / 头像色，两者都只影响「哪些空组存在」。
 */
export type ProjectionRoster = {
  machines?: readonly MachineInfo[];
  agents?: readonly AgentInfo[];
};

/**
 * 共享组 → 桌面端那一套词汇（`project:<id>` / `agent:<id>` / `machine:<id>` / free /
 * flat）。认不出的组交回 null —— 宁愿不摆，也不造一个没有宿主事实可渲染的空壳。
 */
function desktopGroupFrom(shared: SharedIndexGroup): IndexGroup | null {
  const sessionIDs = shared.rows.map((row) => row.sessionId);
  const total = shared.total ?? 0;
  switch (shared.kind) {
    case "project":
      return {
        key: `project:${shared.key}`,
        kind: "project",
        refID: Number(shared.key),
        depth: shared.depth,
        sessionIDs,
        total,
      };
    case "agent":
      return {
        key: `agent:${shared.key}`,
        kind: "agent",
        refID: Number(shared.key),
        depth: 0,
        sessionIDs,
        total,
      };
    case "machine": {
      const deviceID = Number(shared.key.slice("device-".length));
      if (!Number.isFinite(deviceID)) return null;
      return {
        key: `machine:${deviceID}`,
        kind: "machine",
        refID: deviceID,
        depth: 0,
        sessionIDs,
        total,
      };
    }
    case "unassignedProject":
      return {
        key: "free",
        kind: "free",
        refID: 0,
        depth: 0,
        sessionIDs,
        total,
      };
    case "all":
      return {
        key: "flat",
        kind: "flat",
        refID: 0,
        depth: 0,
        sessionIDs,
        total,
      };
    case "unnamedAgent":
      // 桌面端 Agent 轴的每一行都来自一个按 agent 取数的组，认不出 Agent 的兜底组
      // 不会出现；真出现了也不在这里造一个 refID 0 的空组头。
      return null;
  }
}

/**
 * 把宿主的组骨架过一遍共享投影：哪些组存在、组内的分配与排序都听投影的，宿主留下的
 * 只有自己那几维（项目树缩进、agent 轴的 recentIDs、机器轴的本机优先名次）。
 *
 * `axis` 用桌面端的四档（`@/lib/session-axis` 的 IndexAxis，是共享词汇表的子集），
 * `slots` 的 `sessionIDs` 是各轴查询刚取回来的那一页；`roster` 是已知机器 / Agent
 * 名单，名单里的空组因此不靠宿主补。
 */
export function projectIndexGroups(
  axis: IndexAxis,
  slots: readonly IndexGroup[],
  metas: ReadonlyMap<number, IndexRowFacts>,
  roster: ProjectionRoster = {},
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
    // 项目轴仍用宿主自己的树骨架（`projects: []`）：共享投影只负责把行按同步标识
    // 归组，树的顺序与缩进由 slots 给出。机器 / Agent 名单则是权威的已知组名单。
    projects: [],
    agents: [...(roster.agents ?? [])],
    machines: [...(roster.machines ?? [])],
    totals,
  };
  const projected = buildAxisGroups(axis, input);
  const bySharedKey = new Map(projected.map((group) => [group.key, group]));

  // 1. 宿主骨架先落座：投影认得的组用投影分好的行，认不得的（空项目组、时间轴零行）
  //    原样空着 —— 这些组是宿主的事实（项目树 / 单组平铺），不是「有行才有组」。
  const out: IndexGroup[] = [];
  const seen = new Set<string>();
  for (const slot of slots) {
    const group = bySharedKey.get(sharedGroupKey(slot.kind, slot.refID));
    const resolved = group
      ? {
          ...slot,
          sessionIDs: group.rows.map((row) => row.sessionId),
          total: group.total ?? 0,
        }
      : { ...slot, sessionIDs: [] };
    out.push(resolved);
    seen.add(resolved.key);
  }

  // 2. 投影给出的、宿主骨架里没有的组由投影自己补（决策 10 / 11 / 12）：名单里还没
  //    取到页的机器、还没有会话的已知 Agent、常驻的「随手对话」。
  for (const group of projected) {
    const desk = desktopGroupFrom(group);
    if (!desk || seen.has(desk.key)) continue;
    out.push(desk);
    seen.add(desk.key);
  }

  // 3. 机器轴：投影按「在线优先、名字、设备号」排，桌面端再按 roster 的名次排回去 ——
  //    本机必须压过在线段里那些名字排在它前面的 daemon；名单外的设备沉到最后。
  if (axis === "machine") {
    const rank = machineRosterRank(roster.machines ?? []);
    out.sort(
      (a, b) =>
        (rank.get(a.refID) ?? Number.MAX_SAFE_INTEGER) -
        (rank.get(b.refID) ?? Number.MAX_SAFE_INTEGER),
    );
  }

  return out;
}
