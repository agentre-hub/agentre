// 新建对话的标签页插在哪（spec 2026-11-17-group-new-tabs-by-project「插入规则」）。
//
// 纯函数，不读任何 store：调用方给出「某个标签页属于哪个项目」与「某个项目的顶层项目」
// 两个取值口。判定时缺的信息一律按「不是同组成员」处理——最坏结果是落到最右，
// 不会插到别的项目中间。

import type { ChatTab } from "@/stores/chat-tabs-store";
import type { app } from "../../wailsjs/go/models";

export type PlacementContext = {
  /** 标签页所属项目；不参与靠拢（peer、无项目、项目未知）时为 null。 */
  projectOfTab: (tab: ChatTab) => number | null;
  /** 项目所在的顶层项目；树里找不到时回落为项目自身。 */
  rootOf: (projectId: number) => number;
};

/**
 * 新标签页应插入的下标（等于 tabs.length 即追加到最右）。
 * - 项目为空、或栏上没有同一顶层项目族的标签页：追加到最右。
 * - 否则插到最右侧那个同组成员的右边；那个成员在固定区内时，改放到固定区之后。
 */
export function newTabInsertIndex(
  tabs: readonly ChatTab[],
  projectId: number,
  ctx: PlacementContext,
): number {
  if (projectId <= 0) return tabs.length;
  const root = ctx.rootOf(projectId);
  let last = -1;
  tabs.forEach((tab, i) => {
    const pid = ctx.projectOfTab(tab);
    if (pid !== null && pid > 0 && ctx.rootOf(pid) === root) last = i;
  });
  if (last < 0) return tabs.length;
  // 成员在固定区内：越过它所在的那段连续固定标签页。固定的标签页不一定是前缀
  // （取消固定原地不动），所以不能只看前缀长度，否则会夹进两枚固定标签页之间。
  let at = last + 1;
  while (at < tabs.length && tabs[at - 1].isPinned && tabs[at].isPinned) {
    at++;
  }
  return at;
}

/**
 * 标签页所属项目：会话看会话 meta、新对话与终端看自带的项目；
 * 远端设备标签页、附着终端（项目为 0）、meta 里没有项目的会话都返回 null。
 */
export function projectOfTab(
  tab: ChatTab,
  sessionMeta: (sessionId: number) => { projectId?: number } | undefined,
): number | null {
  let pid = 0;
  switch (tab.meta.kind) {
    case "session":
      pid = sessionMeta(tab.meta.sessionId)?.projectId ?? 0;
      break;
    case "new":
    case "terminal":
      pid = tab.meta.projectId;
      break;
    case "peer":
      return null;
  }
  return pid > 0 ? pid : null;
}

/** 由项目树建「项目 → 顶层项目」的查表；树里没有的项目回落为自身。 */
export function rootProjectResolver(
  tree: readonly app.ProjectTreeNode[],
): (projectId: number) => number {
  const rootById = new Map<number, number>();
  const walk = (nodes: readonly app.ProjectTreeNode[], root: number) => {
    for (const n of nodes) {
      const id = n.project?.id ?? 0;
      if (id <= 0) continue;
      const r = root > 0 ? root : id;
      rootById.set(id, r);
      if (n.children?.length) walk(n.children, r);
    }
  };
  walk(tree, 0);
  return (projectId) => rootById.get(projectId) ?? projectId;
}
