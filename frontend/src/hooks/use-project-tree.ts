// frontend/src/hooks/use-project-tree.ts
import * as React from "react";

import { samePayload } from "@/lib/same-payload";

import { ProjectListTree } from "../../wailsjs/go/app/App";
import type { app } from "../../wailsjs/go/models";

type ProjectTreeNode = app.ProjectTreeNode;

type Cache = {
  tree: ProjectTreeNode[];
  promise: Promise<ProjectTreeNode[]> | null;
  loaded: boolean;
};

let cache: Cache = { tree: [], promise: null, loaded: false };
let listeners: Array<() => void> = [];
// 单调递增的请求号: 只有「最新发出的那次」才有资格写缓存。invalidate 刻意绕过在飞
// 去重(见下), 于是两次取数可能重叠, 响应乱序回来时不加这道闸就会用旧树盖掉新树。
// 故意不在 __reset 里归零 —— 上一个用例遗留的在飞请求号只会比新的小, 自然被丢弃。
let latestRequest = 0;

export function __resetProjectTreeForTesting() {
  cache = { tree: [], promise: null, loaded: false };
  listeners = [];
}

function notify() {
  for (const l of listeners) l();
}

// invalidateProjectTreeCache: 「我刚改过项目(建/删/改名/合并/排序), 必须看到改后的树」。
// 不复用在飞的请求 —— 那一条可能是改动落库之前就发出去的, 复用等于拿旧树当结果。
export function invalidateProjectTreeCache() {
  void fetchTree();
}

// reloadProjectTreeCache: 「顺手校准一下」。项目树没有任何推送通道, 侧栏的统一刷新
// 入口(reloadSidebarSources)每轮对话起手 / 落定各调它一次, 所以这里必须
// stale-while-revalidate:
//   - 旧树留在缓存里直到新数据到手。先清空再发 RPC 的话, 同一 tick 里
//     chat-agents-store / session-index-store 的 loading 写入必然触发索引页重渲染,
//     那一帧读到空树 —— 默认的项目轴整块塌成只剩「随手对话」, 看起来就是列表每轮重载。
//   - 有在飞的请求就复用, 不重复发 RPC(与另外两个数据源的 inflight 去重对齐)。
export function reloadProjectTreeCache(): Promise<ProjectTreeNode[]> {
  if (cache.promise) return cache.promise;
  return fetchTree();
}

// isProjectTreeCacheLoaded 给非 React 处用 (sidebar-reload): 没人订阅过
// 项目树时不必为 chat 侧的 sidebar 刷新顺带拉一遍项目数据。
export function isProjectTreeCacheLoaded(): boolean {
  return cache.loaded;
}

// ensureProjectTreeLoaded 给非 React 处复用 loadOnce 语义: 已加载 → 返回缓存,
// 否则触发并 await 加载, 中途有别人发起加载就复用 in-flight promise。
export function ensureProjectTreeLoaded(): Promise<ProjectTreeNode[]> {
  return loadOnce();
}

// commit: 把一次取数的结果写进缓存。乱序响应(requestID 不是最新的)直接丢弃 ——
// 它携带的是更早的快照, 写进去就是回退。
function commit(requestID: number, tree: ProjectTreeNode[]): ProjectTreeNode[] {
  if (requestID !== latestRequest) return cache.tree;
  // 内容没变就保留旧数组的引用并跳过 notify: 项目树每轮都校准一次, 换新数组会让
  // 整个索引页(以及面包屑 / tab 视图)白白重渲一遍。
  if (cache.loaded && samePayload(cache.tree, tree)) {
    cache = { ...cache, promise: null };
    return cache.tree;
  }
  cache = { tree, promise: null, loaded: true };
  notify();
  return tree;
}

function fetchTree(): Promise<ProjectTreeNode[]> {
  const requestID = ++latestRequest;
  const promise = (async () => {
    try {
      return commit(requestID, (await ProjectListTree()) ?? []);
    } catch {
      // 取数失败保留已有的树: 一次网络抖动不该让侧栏整块塌掉。首屏就失败时
      // cache.tree 本来就是空的, 与旧行为一致(loaded 置真, 不转圈)。
      return commit(requestID, cache.tree);
    }
  })();
  cache = { ...cache, promise };
  return promise;
}

async function loadOnce(): Promise<ProjectTreeNode[]> {
  if (cache.loaded) return cache.tree;
  if (cache.promise) return cache.promise;
  return fetchTree();
}

export function useProjectTree() {
  const [, force] = React.useReducer((n: number) => n + 1, 0);
  React.useEffect(() => {
    const l = () => force();
    listeners.push(l);
    if (!cache.loaded) void loadOnce();
    return () => {
      listeners = listeners.filter((x) => x !== l);
    };
  }, []);
  const invalidate = React.useCallback(() => {
    invalidateProjectTreeCache();
  }, []);
  return { tree: cache.tree, invalidate, loaded: cache.loaded };
}
