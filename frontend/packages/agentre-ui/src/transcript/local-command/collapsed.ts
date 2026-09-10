import type { LocalCommandStatus } from "../dto";

/**
 * 折叠态派生：未手动切换过时，运行中展开、完成后折叠；切换过则以显式
 * `expanded` 为准。
 *
 * 纯函数、只看条目自身，所以**不走端口** —— 它不需要宿主的任何东西，让它过一层
 * 接缝只会多一个宿主能答错的地方（两侧各写一份折叠规则，界面就会两套行为）。
 * 宿主的 store 在 `toggleExpanded` 里也用它，规则只有这一份。
 */
export function isLocalCommandCollapsed(entry: {
  status: LocalCommandStatus;
  expanded?: boolean;
}): boolean {
  return entry.expanded === undefined
    ? entry.status !== "running"
    : !entry.expanded;
}
