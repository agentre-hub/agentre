import { useCallback, useEffect, useState } from "react";

import {
  SyncDiscardLostChange,
  SyncListLostChanges,
  SyncNow,
  SyncRecreateFromLostChange,
  SyncRestoreLostChange,
  SyncStatus,
} from "../../../../wailsjs/go/app/App";
import type { sync_svc } from "../../../../wailsjs/go/models";

export type SyncStatusView = sync_svc.Status;
export type LostChangeView = sync_svc.LostChangeView;
export type RestoreOutcome = sync_svc.RestoreOutcome;

/**
 * 设置 → 同步页的数据源:一次拉回状态卡 + 「没能同步的改动」列表,并把 R5 / R5a
 * 的三个动作(恢复 / 按内容新建 / 丢弃)包成会自动 reload 的函数。
 *
 * `status` 为 `null` 时(初次加载中,或未登录 —— `Status()` 返回
 * `{enabled:false}` 而不是抛错)由调用方据 `status?.enabled` 判断整个「同步」
 * 入口存不存在(R12);这里不单独维护一个 loggedIn 布尔,避免两处判断登录态
 * 各自为政。
 */
export function useSyncStatus() {
  const [status, setStatus] = useState<SyncStatusView | null>(null);
  const [lostChanges, setLostChanges] = useState<LostChangeView[]>([]);
  const [loading, setLoading] = useState(true);

  const reload = useCallback(async () => {
    try {
      const [s, list] = await Promise.all([
        SyncStatus(),
        SyncListLostChanges(),
      ]);
      setStatus(s ?? null);
      setLostChanges(list ?? []);
    } catch {
      // 未登录 / 绑定不可用(单机构建):同步区据 status===null 视为不存在。
      setStatus(null);
      setLostChanges([]);
    }
  }, []);

  useEffect(() => {
    void reload().finally(() => setLoading(false));
  }, [reload]);

  useEffect(() => {
    const onFocus = () => void reload();
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, [reload]);

  return {
    status,
    lostChanges,
    loading,
    reload,
    // retryNow 触发一次立即同步(「立即重试」按钮);失败是同步失败重试的正常
    // 一部分,不在这里抛给调用方 —— reload() 之后 status.lastError 自然反映
    // 最新结果。
    retryNow: async () => {
      try {
        await SyncNow();
      } catch {
        // 忽略:退避重试允许失败,下面的 reload() 会把最新状态显示出来。
      } finally {
        await reload();
      }
    },
    restore: async (id: number): Promise<RestoreOutcome> => {
      const outcome = await SyncRestoreLostChange(id);
      await reload();
      return outcome;
    },
    recreate: async (id: number) => {
      await SyncRecreateFromLostChange(id);
      await reload();
    },
    discard: async (id: number) => {
      await SyncDiscardLostChange(id);
      await reload();
    },
  };
}
