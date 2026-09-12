import * as React from "react";

import { WorkspaceFsListDir } from "@/../wailsjs/go/app/App";
import { useSessionStatus } from "@/stores/session-status-store";

import type { workspace_fs_svc } from "@/../wailsjs/go/models";

import { collapseDirChain, type ChainEntry } from "../derive";

import { errorText } from "./panel-feedback";

type Entry = workspace_fs_svc.EntryView;

/** 一层目录的取数状态。已加载的层按「相对 cwd 的路径」缓存，根是空串。 */
export type Level =
  | { status: "loading" }
  | { status: "loaded"; entries: Entry[]; truncated: boolean }
  | { status: "error"; message: string };

export const ROOT = "";

/**
 * 目录树的懒加载：一次只列一层，谁在什么时候被重拉都由这里决定。树自己的两份状态
 * （已加载的层、展开集合）仍归 DirectoryView 持有——空态分支要先于这些 effect
 * 读到它们。
 */
export function useDirectoryLevels({
  sessionId,
  cwd,
  root,
  showIgnored,
  levels,
  setLevels,
  expanded,
  setExpanded,
}: {
  sessionId: number;
  cwd: string;
  root: string;
  showIgnored: boolean;
  levels: Record<string, Level>;
  setLevels: React.Dispatch<React.SetStateAction<Record<string, Level>>>;
  expanded: ReadonlySet<string>;
  setExpanded: React.Dispatch<React.SetStateAction<ReadonlySet<string>>>;
}) {
  // gen 是取数代际：会话 / 忽略开关变化后，先前在途的响应必须丢弃，否则慢的那
  // 一次会把新快照覆盖回旧数据。
  const genRef = React.useRef(0);
  const sessionRef = React.useRef(sessionId);
  // 换工作根与换会话同一档：两棵树的相对路径互不通用，展开态不能跟着过去。
  const rootRef = React.useRef(root);

  // 重取那一步需要「当前展开了哪些层」，但它不能把 expanded 放进依赖里（那样每次
  // 展开都会重取整棵树），所以在这里把最新值镜像到 ref。
  const expandedRef = React.useRef(expanded);
  React.useEffect(() => {
    expandedRef.current = expanded;
  }, [expanded]);

  // doneTick 每次本会话轮次结束自增一次；别的会话结束不会动它。轮次结束是「文件
  // 可能变了」的唯一强信号，快照据此重拉（决策 13）。
  const doneTick = useSessionStatus(sessionId)?.doneTick ?? 0;

  const load = React.useCallback(
    (relPath: string) => {
      const gen = genRef.current;
      setLevels((prev) => ({ ...prev, [relPath]: { status: "loading" } }));
      WorkspaceFsListDir(sessionId, root, relPath, showIgnored).then(
        (res) => {
          if (genRef.current !== gen) return;
          setLevels((prev) => ({
            ...prev,
            [relPath]: {
              status: "loaded",
              entries: res.entries ?? [],
              truncated: res.truncated,
            },
          }));
        },
        (err: unknown) => {
          if (genRef.current !== gen) return;
          setLevels((prev) => ({
            ...prev,
            [relPath]: { status: "error", message: errorText(err) },
          }));
        },
      );
    },
    [sessionId, root, showIgnored, setLevels],
  );

  // 快照失效并重取：换会话时连展开态一起清空；「显示忽略项」变化或本会话轮次结束
  // 时保留展开态，把根与每个已展开的层各重拉一遍（开关会改变后端返回的条目集合，
  // 轮次结束则可能改变任意一层的内容）。
  React.useEffect(() => {
    if (cwd === "") return;
    genRef.current += 1;
    const sameTree =
      sessionRef.current === sessionId && rootRef.current === root;
    sessionRef.current = sessionId;
    rootRef.current = root;
    const keep = sameTree ? expandedRef.current : new Set<string>();
    if (!sameTree) setExpanded(keep);
    setLevels({});
    for (const relPath of [ROOT, ...keep]) load(relPath);
  }, [
    sessionId,
    showIgnored,
    cwd,
    root,
    load,
    doneTick,
    setExpanded,
    setLevels,
  ]);

  // chainChildrenOf 是 collapseDirChain 在这个懒加载数据源下的 childrenOf：只读
  // 已经取回的 levels，未加载的层返回 null——链压缩因此只折叠「已经知道」的部
  // 分，绝不会为了探链本身去发起额外请求（spec「树形模式的链压缩」）。
  const chainChildrenOf = React.useCallback(
    (relPath: string): Array<ChainEntry<string>> | null => {
      const level = levels[relPath];
      if (level === undefined || level.status !== "loaded") return null;
      return level.entries.map((entry) => ({
        name: entry.name,
        isDir: entry.isDir,
        cursor: relPath ? `${relPath}/${entry.name}` : entry.name,
      }));
    },
    [levels],
  );

  // 一个用户「展开」动作要能揭示整条已经缓存到的链，而不是每次只多下一段、
  // 逼用户对同一行反复点击（第二次点击只会把它收起）。这个 effect 在展开的
  // 起点仍处于「链探到头但还不知道再往下」时补一次取数；链每深入一层，
  // levels 变化会让它再检查一遍，直到链的真正末端解析出来（分支/文件/空目
  // 录）或失败为止——用户展开一次，压缩链背后的多级懒加载对其不可见。
  React.useEffect(() => {
    for (const start of expanded) {
      const chain = collapseDirChain(start, start, chainChildrenOf);
      if (chain.children === null && levels[chain.cursor] === undefined) {
        load(chain.cursor);
      }
    }
  }, [expanded, levels, chainChildrenOf, load]);

  return { load, chainChildrenOf };
}
