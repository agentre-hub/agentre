// frontend/src/components/agentre/remote-devices/desktop-device-row.tsx
//
// 账号设备清单里 kind=desktop 的行的展开区（R19，决策 13）：正在运行的桌面端可
// 展开出它的会话列表；未运行时行上按 R2 说明「Agentre 未运行」（与 agentred 的
// 「离线」区分）。点击一条会话 → 切到聊天页并打开 Peer Tab。

import { useState } from "react";
import {
  ChevronDown,
  ChevronRight,
  MonitorUp,
  MessageSquare,
  Loader2,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

import {
  Badge,
  Button,
  lifecycleToAgentStatus,
  SessionLifecycle,
} from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

import { PeerListSessions } from "../../../../wailsjs/go/app/App";
import type { peer_svc, server_svc, wire } from "../../../../wailsjs/go/models";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { relativeTime } from "./format";
import { splitErrorDetail } from "@/lib/error-detail";

type Props = {
  device: server_svc.Device;
  now: number;
};

/**
 * 展开一台远端桌面端时一页要几条。
 *
 * 与索引里那些「先摆几条」的窗口不同，这里是一段可以一直往下翻的清单，所以一页给得
 * 宽一些；剩下的走行尾那颗「加载更多」。
 */
export const DESKTOP_SESSIONS_PAGE_SIZE = 20;

export function DesktopDeviceRow({ device, now }: Props) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const openPeerTab = useChatTabsStore((s) => s.openPeerTab);
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [sessions, setSessions] = useState<wire.SessionSummary[] | null>(null);
  /** 接着往下翻的游标；空 = 没有下一页（也包括「还没展开」那一刻）。 */
  const [cursor, setCursor] = useState("");
  const [error, setError] = useState<string | null>(null);

  const isThis = device.isThisDevice;
  const running = device.online;

  /**
   * 取一页。`from` 是上一页给的游标（空 = 第一页，替换而不是追加）。
   *
   * 按页要而不是整份：那台机器上可能有几千条对话，整份过 Wails 桥既撑住主进程，
   * 也让展开那一下一直空着等解码。
   */
  const loadPage = async (from: string) => {
    setLoading(true);
    setError(null);
    try {
      const result = await PeerListSessions({
        fingerprint: device.fingerprint,
        cursor: from,
        limit: DESKTOP_SESSIONS_PAGE_SIZE,
      } as peer_svc.ListSessionsRequest);
      const page = result?.sessions ?? [];
      setSessions((prev) => (from === "" ? page : [...(prev ?? []), ...page]));
      setCursor(result?.hasMore ? (result.cursor ?? "") : "");
    } catch (e) {
      const { msg } = splitErrorDetail(e);
      setError(msg);
      // 第一页失败时摆空态；翻到一半失败保留已经翻出来的那些，只把失败说出来。
      setSessions((prev) => (from === "" ? [] : prev));
    } finally {
      setLoading(false);
    }
  };

  const toggle = async () => {
    if (!running) return;
    if (open) {
      setOpen(false);
      return;
    }
    setOpen(true);
    if (sessions !== null) return;
    await loadPage("");
  };

  const openSession = (s: wire.SessionSummary) => {
    openPeerTab({
      fingerprint: device.fingerprint,
      conversationId: s.conversationId,
      title: s.title || t("remoteDevices.desktop.untitledSession"),
      deviceName: device.name,
    });
    navigate("/chat");
  };

  return (
    <div
      data-testid="desktop-device-row"
      className="flex flex-col gap-1 rounded-md border border-border bg-card p-3"
    >
      <div className="flex items-center gap-3">
        <span
          aria-label={
            running
              ? t("remoteDevices.desktop.running")
              : t("remoteDevices.desktop.notRunning")
          }
          className={cn(
            "h-2 w-2 rounded-full",
            running ? "bg-status-running" : "bg-muted-foreground",
          )}
        />
        <div className="flex h-7 w-7 items-center justify-center rounded-md bg-secondary">
          <MonitorUp className="h-4 w-4" aria-hidden="true" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="truncate font-medium">
              {device.name || device.fingerprint}
            </span>
            <Badge variant="outline">
              {t("remoteDevices.desktop.kindBadge")}
            </Badge>
            {isThis ? (
              <Badge variant="secondary">
                {t("remoteDevices.desktop.thisDevice")}
              </Badge>
            ) : null}
          </div>
          <div className="truncate text-xs text-muted-foreground">
            {running ? (
              t("remoteDevices.desktop.running", {
                time:
                  device.lastSeenAt > 0
                    ? relativeTime(device.lastSeenAt, now, t)
                    : "",
              })
            ) : (
              <span data-testid="desktop-not-running">
                {t("remoteDevices.desktop.notRunning")}
              </span>
            )}
            {device.lastSeenAt > 0 ? (
              <span className="ml-2">
                {t("remoteDevices.desktop.lastSeen", {
                  time: relativeTime(device.lastSeenAt, now, t),
                })}
              </span>
            ) : null}
          </div>
        </div>
        {running ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            aria-expanded={open}
            onClick={() => void toggle()}
            aria-label={t("remoteDevices.desktop.sessionsToggleAria")}
          >
            {open ? (
              <ChevronDown className="size-4" aria-hidden="true" />
            ) : (
              <ChevronRight className="size-4" aria-hidden="true" />
            )}
          </Button>
        ) : null}
      </div>

      {open ? (
        <div
          data-testid="desktop-session-list"
          className="mt-2 flex flex-col gap-1 border-t border-border pt-2"
        >
          {/* 翻到一半时已经列出来的那些留在原地：把它们换成一个转圈，等于每翻一页
              就把读者刚看的位置抹掉一次。 */}
          {sessions?.map((s) => (
            <button
              key={s.conversationId}
              type="button"
              onClick={() => openSession(s)}
              className="flex items-center gap-2 rounded px-2 py-1.5 text-left text-xs hover:bg-accent"
            >
              <MessageSquare
                className="size-3.5 shrink-0 text-muted-foreground"
                aria-hidden="true"
              />
              <span className="min-w-0 flex-1 truncate">
                {s.title || t("remoteDevices.desktop.untitledSession")}
              </span>
              <SessionStateBadge
                lifecycle={s.lifecycleState}
                waiting={!!s.waitingForInput}
              />
            </button>
          ))}
          {loading ? (
            <div className="flex items-center gap-1 px-1 text-xs text-muted-foreground">
              <Loader2 className="size-3 animate-spin" aria-hidden="true" />
              {t("remoteDevices.desktop.loadingSessions")}
            </div>
          ) : error ? (
            <div className="px-1 text-xs text-destructive">{error}</div>
          ) : sessions && sessions.length === 0 ? (
            <div className="px-1 text-xs text-muted-foreground">
              {t("remoteDevices.desktop.noSessions")}
            </div>
          ) : cursor ? (
            <button
              type="button"
              data-testid="peer-sessions-load-more"
              onClick={() => void loadPage(cursor)}
              className="rounded px-2 py-1.5 text-left text-xs text-muted-foreground hover:bg-accent"
            >
              {t("remoteDevices.desktop.loadMoreSessions")}
            </button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

/**
 * 认得出的生命周期各自的说法。`idle` 不在表里 —— 它与不认识的旧取值共用下面那条
 * 退路（次级徽标 + 「闲置」），列进来只会让同一件事有两个出口。
 *
 * failed 与 interrupted 各占一行而不是合并：前者只是「上一轮的结局是错误」，会话
 * 照旧接得上；后者是自锁终态。少了 failed 这一行它就会落进退路被冒充成「闲着」，
 * 失败静默消失。
 */
const SESSION_STATE_LABEL_KEY: Record<string, string> = {
  [SessionLifecycle.running]: "remoteDevices.desktop.session.running",
  [SessionLifecycle.interrupted]: "remoteDevices.desktop.session.interrupted",
  [SessionLifecycle.failed]: "remoteDevices.desktop.session.failed",
};

function SessionStateBadge({
  lifecycle,
  waiting,
}: {
  lifecycle: string;
  waiting: boolean;
}) {
  const { t } = useTranslation();
  // 「这一档要不要紧」由共享包判（`lifecycleToAgentStatus`），本行只负责画。此前
  // 这里是一份自己的 switch，控制台那边另有一份 —— 2026-09-04 它们真的分了叉：
  // 那一份把 interrupted 也算成出错，于是 agentred 一重启，控制台整列永久红着。
  const status = lifecycleToAgentStatus({ lifecycleState: lifecycle, waiting });
  if (status === "waiting") {
    return (
      <Badge
        variant="outline"
        className="border-status-waiting text-status-waiting"
      >
        {t("remoteDevices.desktop.session.waiting")}
      </Badge>
    );
  }
  const labelKey = SESSION_STATE_LABEL_KEY[lifecycle];
  if (!labelKey) {
    return (
      <Badge variant="secondary">
        {t("remoteDevices.desktop.session.idle")}
      </Badge>
    );
  }
  return (
    <Badge
      variant="outline"
      className={
        status === "error" ? "border-status-error text-status-error" : undefined
      }
    >
      {t(labelKey)}
    </Badge>
  );
}
