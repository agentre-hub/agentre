// 应用外壳的路由词汇:页面路径的取值域、导航项配置、以及在 localStorage 里
// 记住/恢复「上次在哪一页」。
//
// 纯函数与常量,不认识任何组件 —— AppLayout 与 App 都从这一份读。

import type { IconifyIcon } from "@iconify/types";
import buildingCommunityIcon from "@iconify-icons/tabler/building-community";
import layoutKanbanIcon from "@iconify-icons/tabler/layout-kanban";
import messageCircleIcon from "@iconify-icons/tabler/message-circle";
import settingsIcon from "@iconify-icons/tabler/settings";
import webhookIcon from "@iconify-icons/tabler/webhook";

import { getBrowserStorage } from "./platform";

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
];

export const settingsNavItem: NavItem = {
  path: "/settings",
  labelKey: "nav.settings",
  icon: settingsIcon,
};

export const pageBreadcrumbKeys: Record<string, string> = {
  "/chat": "nav.chat",
  "/hooks": "nav.hooks",
  "/issues": "nav.issues",
  "/org": "nav.org",
  "/settings": "nav.settings",
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
  if (settingsNavItem.path) {
    paths.add(settingsNavItem.path);
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
