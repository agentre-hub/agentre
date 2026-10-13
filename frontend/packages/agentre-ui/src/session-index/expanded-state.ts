import * as React from "react";

// localStorage key prefix 保持不变 —— 历史用户存的 agent:N 展开偏好继续可用。
// 项目侧调用方传 "project:N" 命名空间不冲突。
export const SIDEBAR_EXPANDED_KEY_PREFIX = "agentre.agentExpanded.";

// 写入会通知已经挂着的组：展开态此前只在挂载那一刻读一次，之后就是组件自己的
// state，宿主想把某一组打开（刚在它下面建了个子项目）没有任何入口。
const listeners = new Set<() => void>();

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function readSidebarExpanded(key: string): boolean | undefined {
  if (!key) return undefined;
  try {
    const raw = localStorage.getItem(SIDEBAR_EXPANDED_KEY_PREFIX + key);
    if (raw === "1") return true;
    if (raw === "0") return false;
    return undefined;
  } catch {
    return undefined;
  }
}

export function writeSidebarExpanded(key: string, value: boolean): void {
  if (!key) return;
  try {
    localStorage.setItem(SIDEBAR_EXPANDED_KEY_PREFIX + key, value ? "1" : "0");
  } catch {
    // ignore storage errors (private mode quota, etc.)
  }
  for (const listener of listeners) listener();
}

/**
 * 一个组的展开态：`[expanded, toggle]`。
 *
 * 有 key 时以落盘的那一份为准，所以别处调 `writeSidebarExpanded(key, …)` 会让挂着
 * 的组跟着动。本地那份 state 兜两种情况：没有 key 的组，以及存储写不进去（隐私
 * 模式 / 配额）时点 chevron 照样得有反应。
 */
export function useSidebarExpanded(
  key: string | undefined,
  fallback: boolean,
): [boolean, () => void] {
  const [local, setLocal] = React.useState(
    () => readSidebarExpanded(key ?? "") ?? fallback,
  );
  const stored = React.useSyncExternalStore(
    subscribe,
    () => readSidebarExpanded(key ?? ""),
    () => undefined,
  );
  const expanded = stored ?? local;
  const toggle = React.useCallback(() => {
    setLocal(!expanded);
    if (key) writeSidebarExpanded(key, !expanded);
  }, [expanded, key]);
  return [expanded, toggle];
}
