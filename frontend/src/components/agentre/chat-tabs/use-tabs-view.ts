// frontend/src/components/agentre/chat-tabs/use-tabs-view.ts
import * as React from "react";
import { useTranslation } from "react-i18next";
import { tokenToCssColor } from "@agentre-ai/agentre-ui";

import { useProjectTree } from "@/hooks/use-project-tree";
import { reasonToPillText } from "@/lib/attention-display";
import { findProjectColorToken, projectChain } from "@/lib/project-chain";
import {
  useSessionAttentionList,
  type AttentionReason,
} from "@/stores/attention-store";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useSessionMetaStore } from "@/stores/session-meta-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { avatarFromMeta } from "../session-avatar";
import type { TabStatus } from "./tab";

export type TabView = {
  id: string;
  title: string;
  kind: "session" | "new" | "terminal" | "peer";
  avatar: { letter: string; color: string };
  isPreview: boolean;
  isPinned: boolean;
  status: TabStatus;
  projectColor: string | null;
  worktree: boolean;
  pillText: string | null;
  sessionId: number;
  projectChain: string[] | null;
  worktreeBranch: string | null;
  lastMessageAt: number;
};

export function useTabsView(): TabView[] {
  const { t } = useTranslation();
  const tabs = useChatTabsStore((s) => s.tabs);
  const statuses = useSessionStatusStore((s) => s.statuses);
  const metas = useSessionMetaStore((s) => s.metas);
  const { tree } = useProjectTree();

  const sessionTabIds = React.useMemo(
    () => tabs.map((t) => sessionIdOf(t.meta)).filter((sid) => sid > 0),
    [tabs],
  );
  const attentionItems = useSessionAttentionList(sessionTabIds);
  const attentionBySid = React.useMemo(() => {
    const m = new Map<number, AttentionReason>();
    for (const x of attentionItems) m.set(x.sessionId, x.reason);
    return m;
  }, [attentionItems]);

  return tabs.map((tab) => {
    const sid = sessionIdOf(tab.meta);
    const live = sid ? statuses.get(sid) : undefined;
    const reason = sid ? (attentionBySid.get(sid) ?? null) : null;
    const agentStatus = live?.agentStatus;
    const status: TabStatus =
      agentStatus === "running"
        ? "running"
        : agentStatus === "waiting"
          ? "waiting"
          : agentStatus === "error"
            ? "error"
            : "idle";
    const pillText =
      status === "error"
        ? t("chatTabs.status.errorLabel")
        : reasonToPillText(reason);
    const meta = sid ? (metas.get(sid) ?? null) : null;
    const pid = meta?.projectId ?? 0;
    // Peer Tab 不落本地 session/meta store：标题与状态都从 tab 自带元数据取，
    // 项目色 / 来源 / 注意力一律不参与（R22：单端零变化）。
    const peerActive =
      tab.meta.kind === "peer"
        ? {
            title: tab.title ?? t("chatTabs.peer.title"),
            status: "idle" as TabStatus,
            projectColor: null as string | null,
            chain: null as string[] | null,
            lastMessageAt: 0,
          }
        : null;
    if (peerActive) {
      return {
        id: tab.id,
        title: peerActive.title,
        kind: "peer",
        avatar: { letter: "⌁", color: "var(--status-waiting)" },
        isPreview: tab.isPreview,
        isPinned: tab.isPinned,
        status: peerActive.status,
        projectColor: peerActive.projectColor,
        worktree: false,
        pillText: null,
        sessionId: 0,
        projectChain: peerActive.chain,
        worktreeBranch: null,
        lastMessageAt: peerActive.lastMessageAt,
      };
    }
    const projectColor =
      pid > 0 ? tokenToCssColor(findProjectColorToken(tree, pid)) : null;
    const chain = pid > 0 ? projectChain(tree, pid) : [];
    return {
      id: tab.id,
      title: meta?.title ?? tab.title ?? t("chatTabs.fallbackSession"),
      kind: tab.meta.kind,
      avatar: avatarFromMeta(meta),
      isPreview: tab.isPreview,
      isPinned: tab.isPinned,
      status,
      projectColor,
      worktree: false,
      pillText,
      sessionId: sid,
      projectChain: chain.length > 0 ? chain : null,
      worktreeBranch: null,
      lastMessageAt: meta?.lastMessageAt ?? 0,
    };
  });
}

function sessionIdOf(meta: { kind: string; sessionId?: number }): number {
  if (meta.kind !== "session") return 0;
  return meta.sessionId ?? 0;
}
