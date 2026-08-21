// frontend/src/lib/session-axis.ts
//
// 单一会话索引的**轴**定义与项目轴的组顺序。
// 「对话」与「项目」合并成一个索引后，这两个导航项退化成这里的两个 axis
// （见 docs/specs/2026-08-16-unified-chat-index.md 决策 2）。
//
// 轴的定义**留在宿主**，不进 `@agentre-ai/agentre-ui`：agentre-server 的会话列表按
// 「Agent / 状态」分组且由视口决定，两边的轴天生不同（决策 12）。
//
// 会话的**分桶**不在这里：三个轴各有自己的分页查询（ListChatIndexSessions 的
// recent / free / project 三个 scope），会话拿回来时已经分好组了。这里只剩「组的
// 顺序」这一件事，而它对项目轴就是把树摊平。
import type { IndexAxis as SharedIndexAxis } from "@agentre-ai/agentre-ui";

import type { app } from "../../wailsjs/go/models";

/**
 * 桌面端 offer 的三档。**清单归宿主、词汇表归包**（决策 17）：共享投影认得四根轴
 * （多一根 machine），桌面端只摆得出三根，所以这里从包的词汇表里挑，而不是另写一份
 * 字面量 —— 挑的写法让「桌面端的轴一定是投影认得的轴」成为编译期的事实。
 */
export type IndexAxis = Extract<SharedIndexAxis, "project" | "agent" | "time">;

/**
 * 桌面端摆出来的那几档，**按选择器里的顺序**。只有这一份：`AxisPicker` 拿它摆选项，
 * 持久化（`sidebar-axis-state.ts`）拿它校验读回来的值。两处各写一遍就会漂 ——
 * 多出来的那一档选得着，却在下次启动时被当非法值退回默认轴。
 */
export const INDEX_AXES: readonly IndexAxis[] = ["project", "agent", "time"];

export type ProjectOrder = { id: number; depth: number }[];

/**
 * 项目树摊平成「深度优先的 id + 缩进层级」序列 —— 这就是「按项目」轴的组顺序。
 * 没有可用 id 的节点跳过，不发 id 0（那会和「随手对话」的 refID 撞上）。
 */
export function flattenProjectTree(
  nodes: app.ProjectTreeNode[] | null | undefined,
  depth = 0,
  out: ProjectOrder = [],
): ProjectOrder {
  for (const node of nodes ?? []) {
    const id = node.project?.id ?? 0;
    if (id > 0) out.push({ id, depth });
    if (node.children?.length) {
      flattenProjectTree(node.children, id > 0 ? depth + 1 : depth, out);
    }
  }
  return out;
}
