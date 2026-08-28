import * as React from "react";

import { WorkspaceFsSearchFiles } from "@/../wailsjs/go/app/App";

import type { workspace_fs_svc } from "@/../wailsjs/go/models";

import { errorText } from "./panel-feedback";

export type SearchHit = workspace_fs_svc.SearchHitView;

export type DirectorySearchState =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "loaded"; hits: SearchHit[]; truncated: boolean }
  | { status: "error"; message: string };

export type DirectorySearch = {
  /** 过滤态是否激活（搜索按钮的按下态、输入行是否存在）。 */
  active: boolean;
  /** 搜索按钮：激活 ↔ 关闭。关闭时查询串与结果一并清空（下一次是全新的搜索）。 */
  toggle: () => void;
  /** 清除按钮 / 显式关闭：与 toggle 从「已激活」切到「未激活」的效果相同。 */
  close: () => void;
  query: string;
  setQuery: (query: string) => void;
  state: DirectorySearchState;
  /** 失败态的重试：重新发起当前查询串。 */
  retry: () => void;
};

/** 输入静止多久才真正发起后端搜索（served requirement「防抖」）。 */
const DEBOUNCE_MS = 300;

const IDLE: DirectorySearchState = { status: "idle" };

/**
 * 一个恒定的「未激活」搜索句柄，给没有把 useDirectorySearch 接进来的调用方
 * 当默认值（DirectoryView 独立渲染时，例如既有的 row.test.tsx）。模块级常量
 * 保证引用稳定，不会因为每次渲染新建对象而触发额外的 effect 依赖变化。
 */
export const INACTIVE_DIRECTORY_SEARCH: DirectorySearch = {
  active: false,
  toggle: () => {},
  close: () => {},
  query: "",
  setQuery: () => {},
  state: IDLE,
  retry: () => {},
};

/**
 * useDirectorySearch 管「目录」档搜索过滤态的全部状态：激活开关、查询串、
 * 防抖取数与代际防串号（served requirement「文件搜索」）。
 *
 * 激活态与查询串故意不进 chat-sidebar-store 持久化：它们和 DirectoryView 自己
 * 的 levels / expanded 同一类——是这一次查看会话时的临时交互状态。换会话继续
 * 带着上一个会话的查询词没有意义，应用重启后也不该替用户把上一次的搜索词悄悄
 * 再发一次；持久化反而会让「关闭过滤态回到树形视图」在下次打开会话时显得像是
 * 没生效。
 */
export function useDirectorySearch(
  sessionId: number,
  /** 当前工作根的 root 实参（空串 = 会话 cwd）：搜索的范围就是当前这个根。 */
  root: string,
  showIgnored: boolean,
): DirectorySearch {
  const [active, setActive] = React.useState(false);
  const [query, setQuery] = React.useState("");
  const [state, setState] = React.useState<DirectorySearchState>(IDLE);
  const [retryTick, setRetryTick] = React.useState(0);
  const genRef = React.useRef(0);

  // 换会话或换工作根：过滤态、查询串与结果整套重置——一个根下的命中列表放到
  // 另一个根上是错的，查询词也不该悄悄地在新的根里再发一次。
  React.useEffect(() => {
    setActive(false);
    setQuery("");
    setState(IDLE);
  }, [sessionId, root]);

  // 关闭过滤态（toggle 或 close）：查询串与结果一并清空，下次激活是全新的一次
  // 搜索；也覆盖了「换会话」那次 setActive(false) 之后的清理。
  React.useEffect(() => {
    if (!active) {
      setQuery("");
      setState(IDLE);
    }
  }, [active]);

  React.useEffect(() => {
    // 代际先于任何一条 return 自增：放弃取数的那几条路径（关掉过滤态、清空查询
    // 串、换会话）同样必须让此前在途的响应作废，否则它回来时代际仍然相等，会把
    // 一份属于旧查询的结果摆到空查询 / 已关闭的过滤态上。
    genRef.current += 1;
    const gen = genRef.current;
    if (!active) return;
    const trimmed = query.trim();
    if (trimmed === "") {
      // 查询串为空时不发起搜索，显示提示引导输入（served requirement）。
      setState(IDLE);
      return;
    }
    // 立即显形加载态，即使真正的请求要等防抖计时器：敲键时始终看到「正在
    // 搜索」，而不是上一次查询词残留的结果或空态。
    setState({ status: "loading" });
    const timer = window.setTimeout(() => {
      WorkspaceFsSearchFiles(sessionId, root, query, showIgnored).then(
        (res) => {
          if (genRef.current !== gen) return; // 更新的查询已发起，这次响应作废
          setState({
            status: "loaded",
            hits: res.hits ?? [],
            truncated: res.truncated,
          });
        },
        (err: unknown) => {
          if (genRef.current !== gen) return;
          setState({ status: "error", message: errorText(err) });
        },
      );
    }, DEBOUNCE_MS);
    return () => window.clearTimeout(timer);
  }, [active, query, sessionId, root, showIgnored, retryTick]);

  return {
    active,
    toggle: React.useCallback(() => setActive((prev) => !prev), []),
    close: React.useCallback(() => setActive(false), []),
    query,
    setQuery,
    state,
    retry: React.useCallback(() => setRetryTick((tick) => tick + 1), []),
  };
}
