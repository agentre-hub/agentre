/**
 * 手机首页的分段：需要你 / 进行中 / 今天 / 更早。纯函数，宿主给「今天开始时刻」
 * （时区在宿主手里，包不猜）。
 *
 * 输入形状与 `lifecycleToAgentStatus` 的 `LifecycleStatusInput` 同源：`waiting` 就是
 * 「等你输入」（waiting_for_input），`lifecycleState` 是 `running` 即在跑 —— 判据
 * 不在这里另造一套。
 */
import {
  lifecycleToAgentStatus,
  type LifecycleStatusInput,
} from "./lifecycle-status";

export type PhaseSectionKey = "needsYou" | "inProgress" | "today" | "earlier";

export type PhaseSectionInput = LifecycleStatusInput & {
  lastMessageAt: number;
};

export type PhaseSection<T extends PhaseSectionInput> = {
  key: PhaseSectionKey;
  items: T[];
};

const ORDER: readonly PhaseSectionKey[] = [
  "needsYou",
  "inProgress",
  "today",
  "earlier",
];

function sectionOf(
  item: PhaseSectionInput,
  todayStart: number,
): PhaseSectionKey {
  const status = lifecycleToAgentStatus(item);
  if (status === "waiting") return "needsYou";
  if (status === "running") return "inProgress";
  return item.lastMessageAt >= todayStart ? "today" : "earlier";
}

/** 空段不返回；段内按最后消息时间倒序（稳定，不改入参）。 */
export function phaseSections<T extends PhaseSectionInput>(
  items: readonly T[],
  todayStart: number,
): PhaseSection<T>[] {
  const buckets = new Map<PhaseSectionKey, T[]>();
  for (const item of items) {
    const key = sectionOf(item, todayStart);
    const bucket = buckets.get(key);
    if (bucket) bucket.push(item);
    else buckets.set(key, [item]);
  }
  return ORDER.flatMap((key) => {
    const bucket = buckets.get(key);
    if (!bucket) return [];
    return [
      {
        key,
        items: [...bucket].sort((a, b) => b.lastMessageAt - a.lastMessageAt),
      },
    ];
  });
}
