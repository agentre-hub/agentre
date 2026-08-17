// 会话索引的分组维度（axis）的 localStorage 持久化 —— 与侧栏宽度同层
// （`sidebar-width-state.ts`），都是「这个侧栏长什么样」的本机偏好。
//
// 与宽度不同的是这里**不带 key 命名空间**：合并之后只剩一个索引，
// 分组维度是全局唯一的一份（决策 8 同理把侧栏宽度收敛到单个 "chat" 键）。
import type { IndexAxis } from "@/lib/session-axis";

export const SIDEBAR_AXIS_KEY = "agentre.sidebarAxis";

/**
 * 默认按项目分组：项目绑本地路径与执行目标，是干活时的真上下文（决策 2）。
 */
export const DEFAULT_INDEX_AXIS: IndexAxis = "project";

const AXES: readonly IndexAxis[] = ["project", "agent", "time"];

function isIndexAxis(value: string): value is IndexAxis {
  return (AXES as readonly string[]).includes(value);
}

export function readSidebarAxis(): IndexAxis {
  try {
    const raw = localStorage.getItem(SIDEBAR_AXIS_KEY);
    // 认不出的值一律退回默认 —— 与其按一个没人定义过的维度分组，不如回到项目轴。
    if (raw !== null && isIndexAxis(raw)) return raw;
    return DEFAULT_INDEX_AXIS;
  } catch {
    return DEFAULT_INDEX_AXIS;
  }
}

export function writeSidebarAxis(axis: IndexAxis): void {
  try {
    localStorage.setItem(SIDEBAR_AXIS_KEY, axis);
  } catch {
    // 静默：私有模式 / 配额满之类。与 sidebar-width-state 同一处理。
  }
}
