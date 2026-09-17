import type { IndexGroup, IndexGroupRow } from "./axis-groups";

/**
 * 项目轴的一个树节点。`buildAxisGroups` 出来的是**平铺**的先序列表（父在子前、带
 * depth），那是为了纯函数好测；画的时候却必须是树 —— 收起父项目要真的把子项目一起
 * 收起来，父项目「自己的会话」也只有知道它有没有子项目才决定要不要下沉。
 */
export interface IndexGroupNode<G extends IndexGroup = IndexGroup> {
  group: G;
  children: IndexGroupNode<G>[];
  /**
   * 本组连同全部后代的行。折叠父项目时它是唯一的出口：气泡要冒后代的
   * running / 等待输入，否则「折起来就看不见了」。
   */
  subtreeRows: IndexGroupRow[];
}

/**
 * 把平铺的组列表按 depth 还原成树。输入须是 `buildAxisGroups` 的输出顺序（先序）；
 * 非项目轴 depth 恒为 0，于是每组都是没有子节点的根，调用方不必分轴处理。
 */
export function nestProjectGroups<G extends IndexGroup>(
  groups: G[],
): IndexGroupNode<G>[] {
  const roots: IndexGroupNode<G>[] = [];
  // 当前路径上各层的祖先。遇到 depth = d 的组，路径截到 d 层，它挂在第 d-1 层下。
  const path: IndexGroupNode<G>[] = [];
  for (const group of groups) {
    const node: IndexGroupNode<G> = {
      group,
      children: [],
      subtreeRows: [...group.rows],
    };
    path.length = Math.min(path.length, group.depth);
    const parent = path[path.length - 1];
    if (parent) parent.children.push(node);
    else roots.push(node);
    for (const ancestor of path) ancestor.subtreeRows.push(...group.rows);
    path.push(node);
  }
  return roots;
}
