import type { TFunction } from "i18next";

import type { workspace_fs_svc } from "@/../wailsjs/go/models";

export type Change = workspace_fs_svc.ChangeView;

/**
 * GitRow 是 Git 模式一行的显示模型。Git 变动天然跨目录且路径深，所以这里是扁平
 * 列表而不是树（设计决策 11）：`name` 主显、永不截断，`dir` 是右侧灰色的目录
 * 后缀、从头截断，根目录下的文件后缀为空串。
 */
export type GitRow = {
  /** 完整相对路径，行的 key 与「打开文件」的入参。 */
  path: string;
  name: string;
  dir: string;
  /** 后端的五类状态之一：modified / added / deleted / renamed / untracked。 */
  status: string;
  /** 仅 status==="renamed" 时非空。 */
  oldPath: string;
  added: number;
  deleted: number;
  binary: boolean;
};

/**
 * deriveGitRows 把后端的变动清单拆成行模型，并按完整路径重排一次，让新建文件与
 * 同目录的兄弟文件挨在一起。后端已经按路径排过（截断也在排序之后），这里重排只
 * 是不把行序寄托在传输顺序上，顺带走 localeCompare 与其它列表一致。
 */
export function deriveGitRows(changes: Change[] | null | undefined): GitRow[] {
  if (!changes) return [];
  return changes
    .map((change) => {
      const cut = change.path.lastIndexOf("/");
      return {
        path: change.path,
        name: cut < 0 ? change.path : change.path.slice(cut + 1),
        dir: cut < 0 ? "" : change.path.slice(0, cut),
        status: change.status,
        oldPath: change.oldPath ?? "",
        added: change.added ?? 0,
        deleted: change.deleted ?? 0,
        binary: Boolean(change.binary),
      };
    })
    .sort((a, b) => a.path.localeCompare(b.path));
}

export type GitStatusMeta = { letter: string; className: string };

/**
 * GIT_STATUS_META 是五类状态各自的字母与配色，Git 页与目录模式的状态叠加共用
 * 这一份映射（served requirement 明确要求复用，不重复定义颜色语义）。字母对
 * 读屏隐藏，另有 gitStatusLabel 给出文字标签（无障碍约定）。
 */
export const GIT_STATUS_META: Record<string, GitStatusMeta> = {
  modified: { letter: "M", className: "text-status-waiting-text" },
  added: { letter: "A", className: "text-status-running-text" },
  deleted: { letter: "D", className: "text-destructive-text" },
  renamed: { letter: "R", className: "text-primary-text" },
  untracked: { letter: "?", className: "text-muted-foreground" },
};

/**
 * gitStatusLabel 逐个写死 key 而不是拼 `chatContext.git.status.${status}` —— 动态
 * key 会从 i18n 的静态 key 覆盖检查里溜掉，漏翻译不会被测出来。
 */
export function gitStatusLabel(t: TFunction, status: string): string {
  switch (status) {
    case "added":
      return t("chatContext.git.status.added");
    case "deleted":
      return t("chatContext.git.status.deleted");
    case "renamed":
      return t("chatContext.git.status.renamed");
    case "untracked":
      return t("chatContext.git.status.untracked");
    default:
      return t("chatContext.git.status.modified");
  }
}

/**
 * countSubtreeChanges 数出 dirRelPath 子树内有多少条变动路径落在其中（不含
 * dirRelPath 自身——一个目录不会是它自己的变动行）。dirRelPath === "" 代表
 * 会话工作目录本身（目录模式的树根），此时子树就是整份变动清单。
 *
 * 压缩链的行要传**链首**：collapseDirChain 判「中间段只有一个子目录、不含文件」
 * 用的是 listDir 的结果，也就是磁盘上还在的条目，而 git 会报已删除的文件——被链
 * 吸收掉的那几段里因此可能有变动，它们在整棵树上没有任何一行。按链首统计既覆盖
 * 得到它们，在中间段确实干净时也与按链尾统计等值。
 */
export function countSubtreeChanges(
  paths: readonly string[],
  dirRelPath: string,
): number {
  if (dirRelPath === "") return paths.length;
  const prefix = `${dirRelPath}/`;
  let count = 0;
  for (const p of paths) {
    if (p.startsWith(prefix)) count++;
  }
  return count;
}
