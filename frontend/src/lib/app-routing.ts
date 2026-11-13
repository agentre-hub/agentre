// 应用外壳的路由词汇:页面路径的取值域、导航项配置、以及在 localStorage 里
// 记住/恢复「上次在哪一页」。
//
// 纯函数与常量,不认识任何组件 —— AppLayout 与 App 都从这一份读。

import type { IconifyIcon } from "@iconify/types";
import brainIcon from "@iconify-icons/tabler/brain";
import buildingCommunityIcon from "@iconify-icons/tabler/building-community";
import layoutKanbanIcon from "@iconify-icons/tabler/layout-kanban";
import messageCircleIcon from "@iconify-icons/tabler/message-circle";
import settingsIcon from "@iconify-icons/tabler/settings";
import webhookIcon from "@iconify-icons/tabler/webhook";

import type { MemoryOwnerKind, MemoryOwnerRef } from "@agentre-hub/agentre-ui";

import { getBrowserStorage } from "./platform";

/** 记忆页：侧栏在 Hooks 之后（spec 2026-11-11「记忆页」）。 */
const memoryPath = "/memory";

export type NavItem = {
  icon: IconifyIcon;
  labelKey: string;
  path?: string;
};

export const navItems: NavItem[] = [
  {
    path: "/chat",
    labelKey: "nav.chat",
    icon: messageCircleIcon,
  },
  {
    path: "/issues",
    labelKey: "nav.issues",
    icon: layoutKanbanIcon,
  },
  {
    path: "/org",
    labelKey: "nav.org",
    icon: buildingCommunityIcon,
  },
  {
    path: "/hooks",
    labelKey: "nav.hooks",
    icon: webhookIcon,
  },
  {
    path: memoryPath,
    labelKey: "memoryPage.nav",
    icon: brainIcon,
  },
];

// 设置不是页面：齿轮打开浮在当前页面上的设置对话框（stores/settings-dialog-store），
// 所以它没有 path，也就不进 getKnownPaths / lastPath / 面包屑。旧版本存下的
// `agentre.lastPath=/settings` 因此按未知路径处理，启动落到默认页。
export const settingsNavItem: NavItem = {
  labelKey: "nav.settings",
  icon: settingsIcon,
};

export const pageBreadcrumbKeys: Record<string, string> = {
  "/chat": "nav.chat",
  "/hooks": "nav.hooks",
  "/issues": "nav.issues",
  "/org": "nav.org",
  [memoryPath]: "memoryPage.nav",
};

export const lastPathStorageKey = "agentre.lastPath";

export const defaultPath = "/chat";

export function getKnownPaths(): Set<string> {
  const paths = new Set<string>();
  for (const item of navItems) {
    if (item.path) {
      paths.add(item.path);
    }
  }
  return paths;
}

export function isKnownPath(path: string | null): path is string {
  return typeof path === "string" && getKnownPaths().has(path);
}

export function readStoredLastPath(): string | null {
  const storage = getBrowserStorage();

  if (typeof storage?.getItem !== "function") {
    return null;
  }

  try {
    const value = storage.getItem(lastPathStorageKey);

    return isKnownPath(value) ? value : null;
  } catch {
    return null;
  }
}

export function writeStoredLastPath(path: string) {
  const storage = getBrowserStorage();

  if (typeof storage?.setItem !== "function" || !isKnownPath(path)) {
    return;
  }

  try {
    storage.setItem(lastPathStorageKey, path);
  } catch {
    // Some embedded previews may block localStorage.
  }
}

export function getInitialPath(): string {
  return readStoredLastPath() ?? defaultPath;
}

export function isNavItemActive(
  pathname: string,
  itemPath: string | undefined,
) {
  if (!itemPath) {
    return false;
  }

  return pathname === itemPath || pathname.startsWith(`${itemPath}/`);
}

// ── 记忆页的选中项 ──────────────────────────────────────────────────────
//
// 记忆页右侧显示什么由 query 决定，不放组件状态：左列点一下与别处的深链
// （agent / 部门 / 项目页上的「记忆 N 条」）走同一条路，前进后退也还在原处。
//
//   /memory?owner=global            全局
//   /memory?owner=<kind>:<id>       kind ∈ department | agent | project，id > 0
//   /memory?view=recent             最近变更
//
// 没有参数或参数不合法时落到全局。

export type MemorySelection =
  | { view: "owner"; owner: MemoryOwnerRef }
  | { view: "recent" };

const MEMORY_OWNER_KINDS: readonly MemoryOwnerKind[] = [
  "department",
  "agent",
  "project",
];

const GLOBAL_SELECTION: MemorySelection = {
  view: "owner",
  owner: { kind: "global", id: 0 },
};

/** 选中某个归属的记忆页链接。 */
export function memoryOwnerPath(owner: MemoryOwnerRef): string {
  return owner.kind === "global"
    ? `${memoryPath}?owner=global`
    : `${memoryPath}?owner=${owner.kind}:${owner.id}`;
}

/** 记忆页「最近变更」的链接。 */
export function memoryRecentPath(): string {
  return `${memoryPath}?view=recent`;
}

/** 读回 `memoryOwnerPath` / `memoryRecentPath` 写下的选中项（入参是 location.search）。 */
export function parseMemorySelection(search: string): MemorySelection {
  const params = new URLSearchParams(search);
  if (params.get("view") === "recent") return { view: "recent" };
  const raw = params.get("owner");
  if (!raw || raw === "global") return GLOBAL_SELECTION;
  const match = /^([a-z]+):(\d+)$/.exec(raw);
  if (!match) return GLOBAL_SELECTION;
  const kind = match[1] as MemoryOwnerKind;
  const id = Number(match[2]);
  if (!MEMORY_OWNER_KINDS.includes(kind) || !(id > 0)) return GLOBAL_SELECTION;
  return { view: "owner", owner: { kind, id } };
}
