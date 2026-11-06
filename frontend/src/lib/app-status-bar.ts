import type { AgentStatus } from "@/stores/types";

type StatusBarSession = {
  id?: number;
  title?: string;
  lastMessageAt?: number;
  lastReadAt?: number;
  needsAttention?: boolean;
  status?: string;
};

export type StatusBarAgent = {
  name?: string;
  avatarColor?: string;
  avatarIcon?: string;
  avatarDataUrl?: string;
  sessions?: StatusBarSession[];
};

export type StatusBarSessionStatus = {
  agentStatus: AgentStatus;
  needsAttention: boolean;
};

export type StatusBarSessionMeta = {
  lastMessageAt?: number;
  lastReadAt?: number;
};

/** 状态栏「待处理」里的一行：哪个会话、属于哪个 agent（头像用）。 */
export type AttentionItem = {
  id: number;
  title: string;
  agentName: string;
  avatarColor?: string;
  avatarIcon?: string;
  avatarDataUrl?: string;
};

export type AppStatusBarState = {
  agentCount: number;
  runningCount: number;
  approvalIds: number[];
  unreadIds: number[];
  approvals: AttentionItem[];
  unread: AttentionItem[];
  indicatorStatus: AgentStatus;
};

function normalizeAgentStatus(status: string | undefined): AgentStatus {
  switch (status) {
    case "running":
    case "waiting":
    case "error":
    case "idle":
      return status;
    default:
      return "idle";
  }
}

export function deriveAppStatusBarState(
  agents: readonly StatusBarAgent[],
  statuses: ReadonlyMap<number, StatusBarSessionStatus>,
  metas: ReadonlyMap<number, StatusBarSessionMeta>,
  readOverrides: ReadonlyMap<number, number>,
): AppStatusBarState {
  const sessionsById = new Map<number, StatusBarSession>();
  const ownerById = new Map<number, StatusBarAgent>();

  for (const agent of agents) {
    for (const session of agent.sessions ?? []) {
      const sessionId = session.id ?? 0;
      if (sessionId > 0) {
        sessionsById.set(sessionId, session);
        ownerById.set(sessionId, agent);
      }
    }
  }

  let runningCount = 0;
  const approvalIds: number[] = [];
  const unreadIds: number[] = [];
  const approvals: AttentionItem[] = [];
  const unread: AttentionItem[] = [];
  const toItem = (sessionId: number, session: StatusBarSession) => {
    const owner = ownerById.get(sessionId);
    return {
      id: sessionId,
      title: session.title ?? "",
      agentName: owner?.name ?? "",
      avatarColor: owner?.avatarColor,
      avatarIcon: owner?.avatarIcon,
      avatarDataUrl: owner?.avatarDataUrl,
    };
  };

  for (const [sessionId, session] of sessionsById) {
    const liveStatus = statuses.get(sessionId);
    const meta = metas.get(sessionId);
    const agentStatus =
      liveStatus?.agentStatus ?? normalizeAgentStatus(session.status);
    const needsAttention =
      liveStatus?.needsAttention ?? session.needsAttention ?? false;
    const lastMessageAt = meta?.lastMessageAt ?? session.lastMessageAt ?? 0;
    const lastReadAt = Math.max(
      meta?.lastReadAt ?? session.lastReadAt ?? 0,
      readOverrides.get(sessionId) ?? 0,
    );
    const isWaitingForUser = needsAttention || agentStatus === "waiting";

    if (agentStatus === "running") {
      runningCount += 1;
    }

    if (isWaitingForUser) {
      approvalIds.push(sessionId);
      approvals.push(toItem(sessionId, session));
      continue;
    }

    if (agentStatus !== "running" && lastMessageAt > lastReadAt) {
      unreadIds.push(sessionId);
      unread.push(toItem(sessionId, session));
    }
  }

  return {
    agentCount: agents.length,
    runningCount,
    approvalIds,
    unreadIds,
    approvals,
    unread,
    // 只有待审批才染琥珀：未读不是「有人在等你」，保持 running / idle。
    indicatorStatus:
      approvalIds.length > 0
        ? "waiting"
        : runningCount > 0
          ? "running"
          : "idle",
  };
}

/** 不在对话页时，图标栏「对话」按钮才需要提示待审批；在对话页里 tab 条已经在说了。 */
export function railApprovalCount(
  approvalCount: number,
  onChatPage: boolean,
): number {
  return onChatPage ? 0 : approvalCount;
}
