// 侧栏宽度的 localStorage 持久化。chat / projects 两个页面共用一份读写逻辑，
// 通过 key 命名空间 (chat / projects) 分别持久化，互不串。
export const SIDEBAR_WIDTH_KEY_PREFIX = "agentre.sidebarWidth.";

export const SIDEBAR_DEFAULT_WIDTH = 320;
export const SIDEBAR_MIN_WIDTH = 220;
export const SIDEBAR_MAX_WIDTH = 640;

export function clampSidebarWidth(
  width: number,
  fallback: number = SIDEBAR_DEFAULT_WIDTH,
): number {
  if (!Number.isFinite(width)) return fallback;
  return Math.min(
    SIDEBAR_MAX_WIDTH,
    Math.max(SIDEBAR_MIN_WIDTH, Math.round(width)),
  );
}

export function readSidebarWidth(
  key: string,
  fallback: number = SIDEBAR_DEFAULT_WIDTH,
): number {
  if (!key) return fallback;
  try {
    const raw = localStorage.getItem(SIDEBAR_WIDTH_KEY_PREFIX + key);
    if (raw === null) return fallback;
    const n = Number(raw);
    if (!Number.isFinite(n)) return fallback;
    return clampSidebarWidth(n, fallback);
  } catch {
    return fallback;
  }
}

export function writeSidebarWidth(key: string, width: number): void {
  if (!key) return;
  try {
    localStorage.setItem(
      SIDEBAR_WIDTH_KEY_PREFIX + key,
      String(clampSidebarWidth(width)),
    );
  } catch {
    // 静默：私有模式 / 配额满之类。
  }
}
