import * as React from "react";
import {
  ArrowRight,
  CircleAlert,
  CircleCheck,
  CircleHelp,
  X,
  type LucideIcon,
} from "lucide-react";
import { useTranslation } from "react-i18next";

import { cn } from "@/lib/utils";
import type { NotifyKind } from "@/lib/turn-notify";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import {
  useNotificationToastStore,
  type NotificationToast,
} from "@/stores/notification-toast-store";
import { useSessionMetaStore } from "@/stores/session-meta-store";

import { avatarFromMeta } from "./session-avatar";

// done 自动消失时长（ms）。error / waiting 需要用户处理，不自动消失。
const AUTO_DISMISS_MS = 6000;

type KindStyle = {
  Icon: LucideIcon;
  accent: string;
  iconColor: string;
};

const KIND_STYLE: Record<NotifyKind, KindStyle> = {
  done: {
    Icon: CircleCheck,
    accent: "bg-status-running",
    iconColor: "text-status-running",
  },
  error: {
    Icon: CircleAlert,
    accent: "bg-status-error",
    iconColor: "text-status-error",
  },
  waiting: {
    Icon: CircleHelp,
    accent: "bg-status-waiting",
    iconColor: "text-status-waiting",
  },
};

function NotificationToastCard({
  toast,
  onJump,
  onClose,
}: {
  toast: NotificationToast;
  onJump: () => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const meta = useSessionMetaStore((s) => s.metas.get(toast.sessionId));
  const avatar = avatarFromMeta(meta);
  const style = KIND_STYLE[toast.kind];
  const { Icon } = style;

  // 用 ref 持有最新 onClose（在 effect 里更新，不在 render 期写 ref），
  // 让自动消失计时只在 kind/id 变化时重置，避免相邻卡片重渲染续命已计时的 done toast。
  const onCloseRef = React.useRef(onClose);
  React.useEffect(() => {
    onCloseRef.current = onClose;
  });
  React.useEffect(() => {
    if (toast.kind !== "done") return;
    const timer = window.setTimeout(
      () => onCloseRef.current(),
      AUTO_DISMISS_MS,
    );
    return () => window.clearTimeout(timer);
  }, [toast.kind, toast.id]);

  return (
    <div
      role="status"
      aria-live="polite"
      className="pointer-events-auto flex w-[360px] max-w-[calc(100vw-2rem)] overflow-hidden rounded-lg border border-border bg-card shadow-lg"
    >
      <span
        className={cn("w-[3px] self-stretch", style.accent)}
        aria-hidden="true"
      />
      <div className="flex min-w-0 flex-1 flex-col gap-1.5 px-3 py-2.5">
        <div className="flex items-center gap-2">
          <span
            className="inline-flex size-6 shrink-0 items-center justify-center rounded-full"
            style={{ backgroundColor: avatar.color }}
          >
            <span className="text-[11px] font-semibold text-white">
              {avatar.letter}
            </span>
          </span>
          <span className="min-w-0 flex-1 truncate text-[13px] font-semibold text-foreground">
            {toast.title}
          </span>
          <button
            type="button"
            aria-label={t("notify.dismiss")}
            onClick={onClose}
            className="inline-flex size-5 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            <X className="size-3.5" aria-hidden="true" />
          </button>
        </div>
        <div className="flex items-center gap-1.5">
          <Icon
            className={cn("size-3.5 shrink-0", style.iconColor)}
            aria-hidden="true"
          />
          <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground">
            {toast.body}
          </span>
        </div>
        <div className="flex items-center justify-between gap-2">
          <button
            type="button"
            onClick={onJump}
            className="inline-flex items-center gap-1 rounded-md border border-border-strong px-2 py-1 text-[11px] font-medium text-foreground hover:bg-accent"
          >
            {t("notify.openSession")}
            <ArrowRight
              className="size-3 text-muted-foreground"
              aria-hidden="true"
            />
          </button>
          <span className="font-mono text-[10px] text-muted-foreground">
            {t("notify.justNow")}
          </span>
        </div>
      </div>
    </div>
  );
}

// NotificationToastViewport 右下角常驻浮层；订阅 toast store 渲染 bespoke 通知卡。
// 「跳转到会话」打开对应会话 tab 并移除该条；✕ 仅移除。容器本身不拦截下层点击。
export function NotificationToastViewport() {
  const toasts = useNotificationToastStore((s) => s.toasts);
  const dismiss = useNotificationToastStore((s) => s.dismiss);

  if (toasts.length === 0) return null;

  return (
    <div className="pointer-events-none fixed bottom-4 right-4 z-[100] flex flex-col gap-2">
      {toasts.map((toast) => (
        <NotificationToastCard
          key={toast.id}
          toast={toast}
          onClose={() => dismiss(toast.id)}
          onJump={() => {
            useChatTabsStore.getState().openSession(toast.sessionId);
            dismiss(toast.id);
          }}
        />
      ))}
    </div>
  );
}
