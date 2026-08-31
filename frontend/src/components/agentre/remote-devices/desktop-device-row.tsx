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

import { Badge, Button } from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

import { PeerListSessions } from "../../../../wailsjs/go/app/App";
import type { server_svc, wire } from "../../../../wailsjs/go/models";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { relativeTime } from "./format";
import { splitErrorDetail } from "@/lib/error-detail";

type Props = {
  device: server_svc.Device;
  now: number;
};

export function DesktopDeviceRow({ device, now }: Props) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const openPeerTab = useChatTabsStore((s) => s.openPeerTab);
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [sessions, setSessions] = useState<wire.SessionSummary[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const isThis = device.isThisDevice;
  const running = device.online;

  const toggle = async () => {
    if (!running) return;
    if (open) {
      setOpen(false);
      return;
    }
    setOpen(true);
    if (sessions !== null) return;
    setLoading(true);
    setError(null);
    try {
      const result = await PeerListSessions(device.fingerprint);
      setSessions(result?.sessions ?? []);
    } catch (e) {
      const { msg } = splitErrorDetail(e);
      setError(msg);
      setSessions([]);
    } finally {
      setLoading(false);
    }
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
          ) : (
            sessions?.map((s) => (
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
            ))
          )}
        </div>
      ) : null}
    </div>
  );
}

function SessionStateBadge({
  lifecycle,
  waiting,
}: {
  lifecycle: string;
  waiting: boolean;
}) {
  const { t } = useTranslation();
  if (waiting) {
    return (
      <Badge
        variant="outline"
        className="border-status-waiting text-status-waiting"
      >
        {t("remoteDevices.desktop.session.waiting")}
      </Badge>
    );
  }
  switch (lifecycle) {
    case "running":
      return (
        <Badge variant="outline">
          {t("remoteDevices.desktop.session.running")}
        </Badge>
      );
    case "interrupted":
      return (
        <Badge variant="outline">
          {t("remoteDevices.desktop.session.interrupted")}
        </Badge>
      );
    default:
      return (
        <Badge variant="secondary">
          {t("remoteDevices.desktop.session.idle")}
        </Badge>
      );
  }
}
