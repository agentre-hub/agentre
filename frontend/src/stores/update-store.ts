import * as React from "react";
import { create } from "zustand";

import { GetAppSetting, UpdateAppSettings } from "../../wailsjs/go/app/App";
import type { app_settings_svc } from "../../wailsjs/go/models";
import { EventsOff, EventsOn } from "../../wailsjs/runtime/runtime";
import {
  CHECKSUM_FETCH_ERROR_PREFIX,
  checkForUpdate,
  downloadAndInstallUpdate,
  maybeCheckForUpdate,
  restartApp,
  type UpdateInfo,
} from "../components/agentre/update-api";

// SKIPPED_VERSION_KEY 与后端 app_setting_entity.KeySkippedUpdateVersion 一致。
const SKIPPED_VERSION_KEY = "update.skipped_version";

// UpdateCheckOutcome 与后端 update_svc.CheckOutcome 对齐。它只出现在 wails 事件
// payload 里、不在任何绑定签名中，所以 wailsjs/go/models.ts 不会生成它 —— 与
// update-api.ts 里其它本地类型声明同一处理。
export type UpdateCheckOutcome = {
  trigger: string;
  info: UpdateInfo | null;
  error: string;
};

// CheckTrigger 与后端 update_svc.CheckTrigger 的字符串值一致。
export type CheckTrigger = "startup" | "tick" | "focus" | "manual";

/**
 * UpdatePhase 是「更新这件事现在处于哪一步」的唯一表示。
 *
 * idle 与 uptodate 是两回事：前者是「还没查过」，后者是「查过且已是最新」。
 * 状态栏胶囊对两者都退回灰色版本号，但更新面板必须能把它们说清楚。
 */
export type UpdatePhase =
  | { kind: "idle" }
  | { kind: "checking" }
  | { kind: "uptodate" }
  | { kind: "available"; info: UpdateInfo }
  | { kind: "downloading"; info: UpdateInfo; progress: number }
  | { kind: "installed"; info: UpdateInfo }
  | { kind: "error"; message: string };

export type ChecksumPrompt = { open: boolean; reason: string };

export type UpdateSnapshot = {
  phase: UpdatePhase;
  /** 最近一次结果的来源；决定「要不要主动通告」。 */
  lastTrigger: CheckTrigger | null;
  /** 最近一次检查完成的时刻（ms epoch），面板显示「N 分钟前检查」。 */
  lastCheckedAt: number | null;
  /** 被「跳过此版本」压制的版本号；空串表示没有跳过任何版本。 */
  skippedVersion: string;
  checksumPrompt: ChecksumPrompt;
};

export const INITIAL_UPDATE_STATE: UpdateSnapshot = {
  phase: { kind: "idle" },
  lastTrigger: null,
  lastCheckedAt: null,
  skippedVersion: "",
  checksumPrompt: { open: false, reason: "" },
};

/**
 * unskippedUpdate 返回「有一个用户还没跳过的新版本」时的版本信息。
 *
 * 状态栏胶囊**不**用它 —— 胶囊是事实陈述（确实有 v0.9.2 可用），跳过只表示
 * 「别再主动弹我」。设置入口红点用它。
 */
export function unskippedUpdate(s: UpdateSnapshot): UpdateInfo | null {
  if (s.phase.kind !== "available") return null;
  // 精确比对而不是版本序比较：跳过的是「这一个版本」，出现任何别的版本都重新计入。
  if (s.phase.info.latestVersion === s.skippedVersion) return null;
  return s.phase.info;
}

/**
 * pendingAnnouncement 返回「应该主动弹一次提示」的版本信息。
 *
 * 用户自己点「检查更新」发现的更新不在此列 —— 他正看着结果，再弹一张卡片是噪音。
 */
export function pendingAnnouncement(s: UpdateSnapshot): UpdateInfo | null {
  if (s.lastTrigger === null || s.lastTrigger === "manual") return null;
  return unskippedUpdate(s);
}

/**
 * acceptsCheckResult 判断当前阶段是否该被一次检查结果覆盖。
 *
 * 下载中与「已装好待重启」是用户正在推进的流程；后台 tick 恰好落在这中间时，
 * 把它们打回 available/uptodate 会让进度条凭空消失。
 */
function acceptsCheckResult(phase: UpdatePhase): boolean {
  return phase.kind !== "downloading" && phase.kind !== "installed";
}

function resultToPhase(
  info: UpdateInfo | null,
  error: string,
): UpdatePhase | null {
  if (error) return { kind: "error", message: error };
  if (!info) return null; // 被节流跳过：什么都没查，不是「已是最新」。
  return info.hasUpdate ? { kind: "available", info } : { kind: "uptodate" };
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

type UpdateStore = UpdateSnapshot & {
  /** 挂事件订阅并载入持久化的跳过版本；返回解绑函数。 */
  init: () => Promise<() => void>;
  check: (trigger: "manual" | "focus") => Promise<void>;
  download: (skipChecksum: boolean) => Promise<void>;
  dismissChecksumPrompt: () => void;
  skipCurrentVersion: () => Promise<void>;
  restart: () => Promise<void>;
};

export const useUpdateStore = create<UpdateStore>((set, get) => ({
  ...INITIAL_UPDATE_STATE,

  init: async () => {
    EventsOn("update:checked", (outcome: UpdateCheckOutcome) => {
      const next = resultToPhase(outcome.info ?? null, outcome.error ?? "");
      if (next === null) return;
      set((s) => ({
        lastCheckedAt: Date.now(),
        lastTrigger: (outcome.trigger as CheckTrigger) ?? null,
        phase: acceptsCheckResult(s.phase) ? next : s.phase,
      }));
    });

    EventsOn(
      "update:progress",
      (p: { downloaded?: number; total?: number }) => {
        if (!p || !p.total || p.total <= 0) return;
        const pct = Math.min(
          100,
          Math.round(((p.downloaded ?? 0) / p.total) * 100),
        );
        set((s) =>
          s.phase.kind === "downloading"
            ? { phase: { ...s.phase, progress: pct } }
            : {},
        );
      },
    );

    try {
      const item = await GetAppSetting({ key: SKIPPED_VERSION_KEY });
      set({ skippedVersion: item?.value ?? "" });
    } catch {
      // 从未跳过时后端返回 AppSettingNotFound，是常态而不是故障。
      set({ skippedVersion: "" });
    }

    return () => {
      EventsOff("update:checked");
      EventsOff("update:progress");
    };
  },

  check: async (trigger) => {
    const phase = get().phase;
    if (phase.kind === "checking" || !acceptsCheckResult(phase)) return;

    set({ phase: { kind: "checking" } });
    try {
      // manual 绕过节流（用户在等结果）；focus 走受节流入口，被跳过时返回 null。
      const info =
        trigger === "manual"
          ? await checkForUpdate()
          : await maybeCheckForUpdate();
      const next = resultToPhase(info, "");
      set({
        lastTrigger: trigger,
        lastCheckedAt: next === null ? get().lastCheckedAt : Date.now(),
        phase: next ?? phase,
      });
    } catch (err) {
      set({
        lastTrigger: trigger,
        phase: { kind: "error", message: errorMessage(err) },
      });
    }
  },

  download: async (skipChecksum) => {
    const phase = get().phase;
    if (phase.kind !== "available") return;
    const info = phase.info;

    set({
      phase: { kind: "downloading", info, progress: 0 },
      checksumPrompt: { open: false, reason: "" },
    });
    try {
      await downloadAndInstallUpdate(skipChecksum);
      set({ phase: { kind: "installed", info } });
    } catch (err) {
      const message = errorMessage(err);
      if (message.startsWith(CHECKSUM_FETCH_ERROR_PREFIX)) {
        set({
          phase: { kind: "available", info },
          checksumPrompt: {
            open: true,
            reason: message.slice(CHECKSUM_FETCH_ERROR_PREFIX.length),
          },
        });
        return;
      }
      set({ phase: { kind: "error", message } });
    }
  },

  dismissChecksumPrompt: () =>
    set({ checksumPrompt: { open: false, reason: "" } }),

  skipCurrentVersion: async () => {
    const phase = get().phase;
    if (phase.kind !== "available") return;
    const version = phase.info.latestVersion;

    await UpdateAppSettings({
      entries: [{ key: SKIPPED_VERSION_KEY, value: version }],
    } as app_settings_svc.UpdateRequest);
    // 只压制通告与红点：phase 不动，胶囊继续陈述「有 v0.9.2 可用」。
    set({ skippedVersion: version });
  },

  restart: async () => {
    try {
      await restartApp();
    } catch (err) {
      set({ phase: { kind: "error", message: errorMessage(err) } });
    }
  },
}));

/**
 * useUpdateWatch 把「更新这件事」接上宿主：挂事件订阅，并在窗口重新获得焦点时
 * 补一次受节流的检查。
 *
 * 挂在 App 层而不是胶囊里：胶囊只负责显示，订阅是宿主职责——否则每个渲染状态栏的
 * 测试都要先造一套 wails runtime。回窗刷新的写法与 use-remote-devices.ts 一致。
 */
export function useUpdateWatch(): void {
  const init = useUpdateStore((s) => s.init);
  const check = useUpdateStore((s) => s.check);

  React.useEffect(() => {
    let dispose: (() => void) | undefined;
    let unmounted = false;
    void init().then((d) => {
      if (unmounted) {
        d();
        return;
      }
      dispose = d;
    });
    return () => {
      unmounted = true;
      dispose?.();
    };
  }, [init]);

  React.useEffect(() => {
    const onFocus = () => void check("focus");
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, [check]);
}
