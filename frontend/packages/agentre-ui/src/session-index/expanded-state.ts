// localStorage key prefix 保持不变 —— 历史用户存的 agent:N 展开偏好继续可用。
// 项目侧调用方传 "project:N" 命名空间不冲突。
export const SIDEBAR_EXPANDED_KEY_PREFIX = "agentre.agentExpanded.";

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
}
