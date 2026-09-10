// 运行环境判定:这一份代码跑在 Wails 桌面壳里,还是在浏览器里(agentre-server 的
// web 前端共用同一套页面),以及平台标识怎么归一化。

import { type DesktopPlatform } from "@/components/agentre";

export type RuntimeMode = "interactive" | "headless" | "unknown";

export function normalizePlatform(platform: string): DesktopPlatform {
  if (platform === "darwin" || platform === "windows" || platform === "linux") {
    return platform;
  }

  return "unknown";
}

export function detectBrowserPlatform(): DesktopPlatform {
  if (typeof navigator === "undefined") {
    return "unknown";
  }

  const userAgent = navigator.userAgent.toLowerCase();
  if (userAgent.includes("mac")) {
    return "darwin";
  }
  if (userAgent.includes("win")) {
    return "windows";
  }
  if (userAgent.includes("linux")) {
    return "linux";
  }

  return "unknown";
}

export function hasWailsRuntime() {
  return (
    typeof window !== "undefined" &&
    typeof (window as Window & { runtime?: unknown }).runtime === "object" &&
    (window as Window & { runtime?: unknown }).runtime !== null
  );
}

export function getBrowserStorage() {
  if (typeof window === "undefined") {
    return null;
  }

  try {
    return window.localStorage;
  } catch {
    return null;
  }
}
