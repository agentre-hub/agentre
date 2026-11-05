import * as React from "react";
import {
  ArrowRight,
  ArrowUp,
  CircleAlert,
  CircleCheck,
  CircleHelp,
  Download,
  X,
  type LucideIcon,
} from "lucide-react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";

import { cn, formatRelativeTime } from "@agentre-hub/agentre-ui";
import type { NotifyKind } from "@/lib/turn-notify";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import {
  useNotificationToastStore,
  type NotificationToast,
} from "@/stores/notification-toast-store";
import { useSessionMetaStore } from "@/stores/session-meta-store";
import { pendingAnnouncement, useUpdateStore } from "@/stores/update-store";

import { avatarFromMeta } from "./session-avatar";
import { AgentAvatar } from "./primitives";
import { useAgentIdentity } from "./use-agent-identity";

// done 自动消失时长（ms）。error / waiting 需要用户处理，不自动消失。
const AUTO_DISMISS_MS = 6000;

// 相对时间的刷新间隔（ms）；formatRelativeTime 最细到分钟。
const RELATIVE_TIME_TICK_MS = 30_000;

// useNow 每隔一段时间刷新当前时间，让相对时间自己走。
function useNow(): number {
  const [now, setNow] = React.useState(() => Date.now());
  React.useEffect(() => {
    const timer = window.setInterval(
      () => setNow(Date.now()),
      RELATIVE_TIME_TICK_MS,
    );
    return () => window.clearInterval(timer);
  }, []);
  return now;
}

// 新版本到达提示的自动消失时长（ms）。比 done 长一点，但不常驻：
// 状态栏胶囊已经兜住了「错过」，让它一直占着右下角只是重复。
const UPDATE_AUTO_DISMISS_MS = 8000;

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
  // meta 只有名字 / 颜色；图标与上传图片在 agents 列表里（规格 2026-11-02 决策 17）。
  const identity = useAgentIdentity(meta?.agentId);
  const avatar = avatarFromMeta(meta, identity);
  const now = useNow();
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
      className="pointer-events-auto flex w-[360px] max-w-[calc(100vw-2rem)] overflow-hidden rounded-lg bg-popover shadow-lg"
    >
      <span
        className={cn("w-[3px] self-stretch", style.accent)}
        aria-hidden="true"
      />
      <div className="flex min-w-0 flex-1 flex-col gap-1.5 px-3 py-2.5">
        <div className="flex items-center gap-2">
          <AgentAvatar
            name={identity?.name || meta?.agentName || ""}
            initials={avatar.letter}
            size="sm"
            // 沿用这一枚原来的尺寸与圆角（不跟着身份档改形）。
            className="rounded-full"
            style={{ backgroundColor: avatar.color }}
            avatarDataUrl={avatar.avatarDataUrl}
            avatarIcon={avatar.avatarIcon}
          />
          <span className="min-w-0 flex-1 truncate text-aux font-semibold text-foreground">
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
            className="inline-flex items-center gap-1 rounded-md bg-button-fill px-2 py-1 text-2xs font-medium text-foreground hover:bg-button-fill-hover"
          >
            {t("notify.openSession")}
            <ArrowRight
              className="size-3 text-muted-foreground"
              aria-hidden="true"
            />
          </button>
          <span className="font-mono text-3xs text-muted-foreground">
            {formatRelativeTime(
              toast.createdAt,
              Math.max(now, toast.createdAt),
              t,
            )}
          </span>
        </div>
      </div>
    </div>
  );
}

/**
 * UpdateToastCard 新版本到达提示。复用会话通知卡的形状（360px + 3px 侧条），
 * 不引入第二套通知视觉。
 *
 * 它不进 notification-toast-store：那里的 toast 是会话绑定的（sessionId 必填，
 * 用于头像与「跳转到会话」），为一条版本公告把它变成可选会污染既有契约。
 */
function UpdateToastCard() {
  const { t } = useTranslation();
  const info = useUpdateStore(pendingAnnouncement);
  const markAnnounced = useUpdateStore((s) => s.markAnnounced);
  const setPanelOpen = useUpdateStore((s) => s.setPanelOpen);

  const version = info?.latestVersion ?? "";
  React.useEffect(() => {
    if (!version) return;
    const timer = window.setTimeout(markAnnounced, UPDATE_AUTO_DISMISS_MS);
    return () => window.clearTimeout(timer);
  }, [version, markAnnounced]);

  if (info === null) return null;

  return (
    <div
      role="status"
      aria-live="polite"
      className="pointer-events-auto flex w-[360px] max-w-[calc(100vw-2rem)] overflow-hidden rounded-lg bg-popover shadow-lg"
    >
      <span className="w-[3px] self-stretch bg-primary" aria-hidden="true" />
      <div className="flex min-w-0 flex-1 flex-col gap-1.5 px-3 py-2.5">
        <div className="flex items-center gap-2">
          <span className="inline-flex size-6 shrink-0 items-center justify-center rounded-full bg-primary text-primary-foreground">
            <ArrowUp className="size-3.5" aria-hidden="true" />
          </span>
          <span className="min-w-0 flex-1 truncate text-aux font-semibold text-foreground">
            {t("update.toast.title", { version: info.latestVersion })}
          </span>
          <button
            type="button"
            aria-label={t("update.toast.dismiss")}
            onClick={markAnnounced}
            className="inline-flex size-5 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            <X className="size-3.5" aria-hidden="true" />
          </button>
        </div>
        <div className="flex items-center gap-1.5">
          <Download
            className="size-3.5 shrink-0 text-primary-text"
            aria-hidden="true"
          />
          <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground">
            {t("update.toast.body", { current: info.currentVersion })}
          </span>
        </div>
        <div className="flex items-center justify-between gap-2">
          <button
            type="button"
            onClick={() => {
              setPanelOpen(true);
              markAnnounced();
            }}
            className="inline-flex items-center gap-1 rounded-md bg-button-fill px-2 py-1 text-2xs font-medium text-foreground hover:bg-button-fill-hover"
          >
            {t("update.toast.action")}
            <ArrowRight
              className="size-3 text-muted-foreground"
              aria-hidden="true"
            />
          </button>
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
  const announcement = useUpdateStore(pendingAnnouncement);

  if (toasts.length === 0 && announcement === null) return null;

  // portal 到 body 末尾：层级由 DOM/portal 顺序决定（design.md 层级规范），不手写 z-index。
  return createPortal(
    <div className="pointer-events-none fixed bottom-4 right-4 flex flex-col gap-2">
      <UpdateToastCard />
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
    </div>,
    document.body,
  );
}
