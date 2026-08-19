// update-api.ts —— 桌面端"检查更新 / 下载安装 / 通道-镜像设置"的 Wails 调用封装。
//
// 没有直接从 wailsjs/go/app/App 里 import，是因为这些方法是新加的，需要 wails generate
// 重新跑一遍才会出现。这里走 window.go.app.App.<name>(...) 直接路径，与 wailsjs 生成文件
// 内部实现完全一致；类型本地定义，保证前端编辑器不依赖未生成的声明。

declare global {
  interface Window {
    go?: {
      app?: {
        App?: Record<string, (...args: unknown[]) => Promise<unknown>>;
      };
    };
  }
}

export type UpdateChannel = "stable" | "beta" | "nightly";

export type UpdateInfo = {
  hasUpdate: boolean;
  currentVersion: string;
  latestVersion: string;
  releaseNotes: string;
  releaseURL: string;
  publishedAt: string;
};

export type MirrorInfo = {
  id: string;
  name: string;
  url: string;
};

// BugReportInfo 与后端 app.BugReportInfo 对齐，用于预填 GitHub Bug 反馈模板。
export type BugReportInfo = {
  version: string;
  commit: string;
  os: string;
  arch: string;
  osLabel: string;
};

// 校验文件下载失败的错误前缀，与后端 update_svc.ChecksumFetchError 一致。
export const CHECKSUM_FETCH_ERROR_PREFIX = "CHECKSUM_FETCH_FAILED:";

function call<T>(name: string, ...args: unknown[]): Promise<T> {
  const fn = window.go?.app?.App?.[name];
  if (typeof fn !== "function") {
    return Promise.reject(new Error(`Wails binding ${name} not available`));
  }
  return fn(...args) as Promise<T>;
}

export function checkForUpdate(): Promise<UpdateInfo> {
  return call<UpdateInfo>("CheckForUpdate");
}

// maybeCheckForUpdate 受 24h 节流约束的检查（窗口重新获得焦点时调用）。
// 距上次检查太近时后端什么都不做并返回 null —— 那不是「已是最新」。
export function maybeCheckForUpdate(): Promise<UpdateInfo | null> {
  return call<UpdateInfo | null>("MaybeCheckForUpdate");
}

export function downloadAndInstallUpdate(skipChecksum: boolean): Promise<void> {
  return call<void>("DownloadAndInstallUpdate", skipChecksum);
}

export function getAvailableMirrors(): Promise<MirrorInfo[]> {
  return call<MirrorInfo[]>("GetAvailableMirrors");
}

export function getUpdateChannel(): Promise<UpdateChannel> {
  return call<UpdateChannel>("GetUpdateChannel");
}

export function setUpdateChannel(channel: UpdateChannel): Promise<void> {
  return call<void>("SetUpdateChannel", channel);
}

export function getDownloadMirror(): Promise<string> {
  return call<string>("GetDownloadMirror");
}

export function setDownloadMirror(mirror: string): Promise<void> {
  return call<void>("SetDownloadMirror", mirror);
}

export function restartApp(): Promise<void> {
  return call<void>("RestartApp");
}

export function getBugReportInfo(): Promise<BugReportInfo> {
  return call<BugReportInfo>("GetBugReportInfo");
}

export function openLogsDir(): Promise<void> {
  return call<void>("OpenLogsDir");
}

export function getDebugLogging(): Promise<boolean> {
  return call<boolean>("GetDebugLogging");
}

export function setDebugLogging(enabled: boolean): Promise<void> {
  return call<void>("SetDebugLogging", enabled);
}
