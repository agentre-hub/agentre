import * as React from "react";
import { useTranslation } from "react-i18next";

import { useChatAgents, type AgentSlim } from "@/hooks/use-chat-agents";
import i18n from "@/i18n";
import { reasonToDisplayStatus } from "@/lib/attention-display";
import { relativeTime } from "@/lib/relative-time";
import { cn } from "@/lib/utils";
import {
  useSessionAttentionList,
  type AttentionReason,
} from "@/stores/attention-store";

import { AgentAvatar, StatusDot } from "../../primitives";
import type { AgentColor, AgentStatus } from "../../types";
import { scoreItem } from "../score";
import type { CommandSource, OnSelectCtx } from "../types";

const ATTENTION_SCORE_BOOST = 5;

export type ChatSessionItem = {
  key: string;
  sessionId: number;
  agentId: number;
  title: string;
  agentName: string;
  agentColor: AgentColor;
  agentAvatarIcon?: string;
  agentAvatarDataUrl?: string;
  status: AgentStatus;
  lastMessageAt: number;
  active: boolean;
  attentionReason: AttentionReason | null;
};

// 把所有 agent.sessions 拍平成基础列表（不做 attention 判断；attention 由 useItems 统一走 store）。
export function flattenSessions(agents: AgentSlim[]): ChatSessionItem[] {
  const flat: ChatSessionItem[] = [];
  for (const a of agents) {
    for (const s of a.sessions) {
      flat.push({
        key: `chat-session-${s.id}`,
        sessionId: s.id,
        agentId: a.id,
        title: s.title || i18n.t("commandPalette.chatSessions.untitled"),
        agentName: a.name,
        agentColor: (a.avatarColor as AgentColor) || "agent-1",
        agentAvatarIcon: a.avatarIcon || undefined,
        agentAvatarDataUrl: a.avatarDataUrl || undefined,
        status: (s.status as AgentStatus) || "idle",
        lastMessageAt: s.lastMessageAt,
        active: false,
        attentionReason: null,
      });
    }
  }
  return flat;
}

export function useItems(): { items: ChatSessionItem[]; loading: boolean } {
  const { agents, loading } = useChatAgents();
  const baseItems = React.useMemo(() => flattenSessions(agents), [agents]);
  const allIds = React.useMemo(
    () => baseItems.map((i) => i.sessionId),
    [baseItems],
  );
  const attentionList = useSessionAttentionList(allIds);
  const attentionMap = React.useMemo(() => {
    const m = new Map<number, AttentionReason>();
    for (const x of attentionList) m.set(x.sessionId, x.reason);
    return m;
  }, [attentionList]);

  const items = React.useMemo(() => {
    const decorated = baseItems.map((it) => {
      const reason = attentionMap.get(it.sessionId) ?? null;
      return { ...it, active: reason !== null, attentionReason: reason };
    });
    decorated.sort((a, b) => {
      if (a.active !== b.active) return a.active ? -1 : 1;
      return (b.lastMessageAt || 0) - (a.lastMessageAt || 0);
    });
    // 不再截断前 10 条：SourceGroup 会对全量列表评分并截到 50 条渲染，
    // 因此更旧的会话也能被精确查询命中。
    return decorated;
  }, [baseItems, attentionMap]);

  return { items, loading };
}

function getScore(query: string, item: ChatSessionItem): number {
  const base = scoreItem({
    query,
    title: item.title,
    subtitle: item.agentName,
  });
  if (base === 0) return 0;
  return item.active ? base + ATTENTION_SCORE_BOOST : base;
}

// 单行渲染。cmdk 用 data-selected 控制高亮，外层 CommandItem 已绑好。
// active=true 仅用于显示状态徽章 / ↵ kbd（active 是数据态，不是 cmdk 选中态）。
function renderItem(item: ChatSessionItem): React.ReactNode {
  return <SessionRow item={item} />;
}

type SessionRowProps = { item: ChatSessionItem };

function SessionRow({ item }: SessionRowProps) {
  return (
    <div className="flex w-full items-center gap-3">
      <AgentAvatar
        name={item.agentName}
        initials={item.agentName.charAt(0)}
        color={item.agentColor}
        avatarIcon={item.agentAvatarIcon}
        avatarDataUrl={item.agentAvatarDataUrl}
        size="md"
        className="size-7 rounded-md text-xs"
      />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <div
          className="truncate text-sm font-medium text-foreground"
          title={item.title}
        >
          {item.title}
        </div>
        <div className="flex items-center gap-1.5 text-2xs text-muted-foreground">
          <StatusDot status={item.status} size="xs" />
          <span className="truncate">{item.agentName}</span>
          {item.lastMessageAt > 0 ? (
            <>
              <span aria-hidden="true">·</span>
              <span className="font-mono text-2xs">
                {relativeTime(item.lastMessageAt)}
              </span>
            </>
          ) : null}
        </div>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        {item.active ? (
          <StatusBadge
            status={reasonToDisplayStatus(item.attentionReason, item.status)}
          />
        ) : null}
        <kbd
          className={cn(
            "rounded-sm border border-border bg-card px-1.5 py-0.5 font-mono text-2xs font-medium text-muted-foreground",
            // ↵ kbd 在 cmdk 高亮态下显示，非高亮时透明，避免每行都堆视觉噪音
            "opacity-0 group-data-[selected=true]/cmditem:opacity-100",
          )}
          aria-hidden="true"
        >
          ↵
        </kbd>
      </div>
    </div>
  );
}

function StatusBadge({ status }: { status: AgentStatus }) {
  const { t } = useTranslation();
  const tone =
    status === "running"
      ? "bg-status-running-bg text-status-running"
      : status === "waiting"
        ? "bg-status-waiting-bg text-status-waiting"
        : status === "error"
          ? "bg-destructive/15 text-destructive"
          : "bg-muted text-muted-foreground";
  const label =
    status === "running"
      ? t("commandPalette.chatSessions.status.running")
      : status === "waiting"
        ? t("commandPalette.chatSessions.status.waiting")
        : status === "error"
          ? t("commandPalette.chatSessions.status.error")
          : "";
  if (!label) return null;
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center rounded-full px-2 py-0.5 text-2xs font-medium",
        tone,
      )}
    >
      {label}
    </span>
  );
}

function onSelect(item: ChatSessionItem, ctx: OnSelectCtx): void {
  ctx.openSession(item.sessionId);
  ctx.close();
  try {
    ctx.navigate("/chat");
  } catch (err) {
    // navigate 失败不阻断面板关闭 / 状态写入；保留 console 提示。
    console.warn("[command-palette] navigate('/chat') failed", err);
  }
}

export const chatSessionsSource: CommandSource<ChatSessionItem> = {
  id: "chat-sessions",
  heading: i18n.t("commandPalette.chatSessions.heading"),
  modes: ["default"],
  useItems,
  getScore,
  renderItem,
  onSelect,
};
