import { create } from "zustand";

import type { NotifyKind } from "../lib/turn-notify";

// NotificationToast 一条应用内提示。title 为会话名（已由通知逻辑按 i18n 取好），
// body 为状态文案，sessionId 供「跳转到会话」与头像查找用。
export type NotificationToast = {
  id: number;
  sessionId: number;
  kind: NotifyKind;
  title: string;
  body: string;
  // push 时刻（ms epoch），供通知卡显示相对时间。
  createdAt: number;
};

// 同时最多保留几条，超出丢弃最旧，避免会话密集完成时无限堆叠。
const MAX_TOASTS = 5;

type State = {
  toasts: NotificationToast[];
  push: (toast: Omit<NotificationToast, "id" | "createdAt">) => number;
  dismiss: (id: number) => void;
  // 会话的 attention 被清除（审批已处理 / 会话被打开 / 新一轮开始）时移除其
  // waiting / error 通知；done 卡不是 attention，按自己的计时收。
  dismissBySession: (sessionId: number) => void;
  clear: () => void;
};

let seq = 0;

export const useNotificationToastStore = create<State>((set) => ({
  toasts: [],
  push: (toast) => {
    const id = ++seq;
    set((s) => {
      // 同一会话只留最新一张。
      const kept = s.toasts.filter((x) => x.sessionId !== toast.sessionId);
      const next = [...kept, { ...toast, id, createdAt: Date.now() }];
      return {
        toasts:
          next.length > MAX_TOASTS
            ? next.slice(next.length - MAX_TOASTS)
            : next,
      };
    });
    return id;
  },
  dismiss: (id) =>
    set((s) => ({ toasts: s.toasts.filter((x) => x.id !== id) })),
  dismissBySession: (sessionId) =>
    set((s) => ({
      toasts: s.toasts.filter(
        (x) => x.sessionId !== sessionId || x.kind === "done",
      ),
    })),
  clear: () => set({ toasts: [] }),
}));
