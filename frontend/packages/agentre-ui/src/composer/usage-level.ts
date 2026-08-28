/** 用量告警的三档。上下文窗口与订阅配额共用这一套定级。 */
export type UsageLevel = "ok" | "warn" | "danger";

// usageLevel 是告警阈值的**唯一**来源：上下文计量器与桌面端的订阅配额计量器都走它。
// 两处各写一份 90/75 迟早会改漏一处 —— 桌面端 chat.tsx 原来那份 quotaLevel 的注释
// 就是这么写的，把计量器搬进包而把阈值留在宿主，正好会制造出它警告的那种形态。
//
// 收 null 是为了配额那一侧：窗口可能整个缺失（未登录 / 无凭据），此时不告警。
export function usageLevel(percent: number | null): UsageLevel {
  if (percent == null) return "ok";
  if (percent >= 90) return "danger";
  if (percent >= 75) return "warn";
  return "ok";
}
