/**
 * 候选按时间分组（规格「UI 与状态」：左栏「按时间分组」）。
 *
 * 纯函数，与呈现件分开：分组口径是「哪一天跑的」，不是「离现在几小时」——
 * 按 24 小时窗口切会让昨晚 23:50 那条落进「今天」，而用户找的是「昨天那条」。
 */
import type { ImportCandidateView } from "./ports";

export type CandidateBucket = "today" | "yesterday" | "earlier";

export interface CandidateGroup {
  bucket: CandidateBucket;
  items: ImportCandidateView[];
}

function startOfDay(at: number): number {
  const d = new Date(at);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}

/**
 * 前一天的零点。**按日历退一天，不是减 24 小时** —— 夏令时那两天一天并不是 24
 * 小时，减出来的边界会落到前天深夜（春季）或昨天凌晨（秋季），于是前天那条被标成
 * 「昨天」。分组口径是「哪一天跑的」，那就得按日历算。
 */
function startOfPreviousDay(dayStart: number): number {
  const d = new Date(dayStart);
  d.setDate(d.getDate() - 1);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}

/** 排序键：末次活动优先，没有末次就退回起始时间（元信息缺 endedAt 的后端）。 */
function activityAt(candidate: ImportCandidateView): number {
  return candidate.endedAt || candidate.startedAt;
}

/**
 * 按「今天 / 昨天 / 更早」切三段，段内按末次活动倒序。
 *
 * 空段不出现 —— 一个只有标题、下面什么都没有的分组头是噪声。
 */
export function buildCandidateGroups(
  candidates: readonly ImportCandidateView[],
  now: number,
): CandidateGroup[] {
  const today = startOfDay(now);
  const yesterday = startOfPreviousDay(today);

  const order: CandidateBucket[] = ["today", "yesterday", "earlier"];
  const byBucket = new Map<CandidateBucket, ImportCandidateView[]>();
  for (const candidate of candidates) {
    const at = activityAt(candidate);
    const bucket: CandidateBucket =
      at >= today ? "today" : at >= yesterday ? "yesterday" : "earlier";
    const list = byBucket.get(bucket);
    if (list) list.push(candidate);
    else byBucket.set(bucket, [candidate]);
  }

  return order.flatMap((bucket) => {
    const items = byBucket.get(bucket);
    if (!items || items.length === 0) return [];
    return [
      {
        bucket,
        items: [...items].sort((a, b) => activityAt(b) - activityAt(a)),
      },
    ];
  });
}
