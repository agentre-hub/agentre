import * as React from "react";
import { useTranslation } from "react-i18next";
import {
  SessionGroupOverflow,
  isOpenInNewTabModifier,
} from "@agentre-hub/agentre-ui";

import { useEffectiveSessionStatus } from "@/hooks/use-live-session-status";
import { relativeTime } from "@/lib/relative-time";
import { cn } from "@/lib/utils";

import { AgentAvatar, StatusDot } from "./primitives";
import type { AgentColor, AgentStatus } from "./types";
import { statusConfig } from "./types";

const PAGE_SIZE = 20;

// SessionsPopoverItem —— popover 内列表行需要的最小字段。
// chat 用 ChatSessionLite（status 字段）直接满足；项目侧用 ProjectSessionItem 时
// 由调用方先在 loader 出口把 agentStatus 拷到 status 上即可。
export type SessionsPopoverItem = {
  id: number;
  title: string;
  status: string;
  lastMessageAt: number;
};

export type SessionsPopoverPage = {
  sessions: SessionsPopoverItem[];
  total: number;
  hasMore: boolean;
};

export type SessionsPopoverLoader = (opts: {
  offset: number;
  limit: number;
}) => Promise<SessionsPopoverPage>;

type HeaderInfo = {
  name: string;
  avatarColor?: string;
  avatarIcon?: string;
  avatarDataUrl?: string;
};

type SessionsPopoverProps = {
  header: HeaderInfo;
  // loader：每次需要拉一页就调一次；offset 是「已经加载的条数」，limit=PAGE_SIZE。
  // 调用方负责把 chat / project 的后端 binding 适配到 SessionsPopoverPage 形状。
  loader: SessionsPopoverLoader;
  onClose: () => void;
  onSelectSession: (sessionId: number, opts?: { newTab?: boolean }) => void;
};

function statusOf(s: SessionsPopoverItem): AgentStatus {
  if (s.status === "running") return "running";
  if (s.status === "error") return "error";
  if (s.status === "waiting") return "waiting";
  return "idle";
}

// SessionRow 把单行渲染抽出来,这样可以在循环里给每条 session 用 hook 订阅
// 自己的 live status — turn 进行中后端推 session_status 时,列表行的 status
// pill 实时跟着翻成 waiting / 审批 而不必等下次 loader 拉。
function SessionRow({
  session,
  onSelect,
}: {
  session: SessionsPopoverItem;
  onSelect: (id: number, opts?: { newTab?: boolean }) => void;
}) {
  const { t } = useTranslation();
  const effective = useEffectiveSessionStatus(session.id, {
    agentStatus: session.status,
    needsAttention: false,
  });
  const status = statusOf({ ...session, status: effective.agentStatus });
  const config = statusConfig[status];
  return (
    <button
      type="button"
      className="flex w-full cursor-pointer items-start gap-2.5 rounded-md px-2 py-1.5 text-left outline-none transition-colors hover:bg-sidebar-active-bg focus-visible:ring-[3px] focus-visible:ring-ring/50"
      onClick={(e) =>
        onSelect(session.id, { newTab: isOpenInNewTabModifier(e) })
      }
    >
      <StatusDot status={status} size="xs" className="mt-1.5" />
      <div className="min-w-0 flex-1">
        <div className="truncate text-xs font-medium text-foreground">
          {session.title || t("sessionsPopover.untitled")}
        </div>
        <div className="mt-0.5 truncate font-mono text-2xs text-muted-foreground">
          {relativeTime(session.lastMessageAt)}
        </div>
      </div>
      <span
        className={cn(
          "shrink-0 self-start pt-0.5 font-mono text-2xs",
          status === "running" || status === "error"
            ? config.textClassName
            : "text-muted-foreground",
        )}
      >
        {config.label}
      </span>
    </button>
  );
}

function SessionsPopover({
  header,
  loader,
  onClose,
  onSelectSession,
}: SessionsPopoverProps) {
  const loaderRef = React.useRef(loader);
  React.useEffect(() => {
    loaderRef.current = loader;
  }, [loader]);

  // The group scope is fixed for this mounted popover. Parent rerenders may allocate a
  // new adapter function, but only close/reopen should reset the shared paging cycle.
  const loadPage = React.useCallback(async (cursor: string | null) => {
    const offset = cursor === null ? 0 : Number(cursor);
    if (!Number.isSafeInteger(offset) || offset < 0) {
      throw new Error("invalid desktop session offset cursor");
    }

    const page = await loaderRef.current({ offset, limit: PAGE_SIZE });
    return {
      rows: page.sessions,
      total: page.total,
      nextCursor: page.hasMore ? String(offset + page.sessions.length) : null,
    };
  }, []);

  return (
    <SessionGroupOverflow
      title={header.name}
      avatar={
        <AgentAvatar
          name={header.name}
          initials={header.name.charAt(0)}
          color={(header.avatarColor as AgentColor) || "agent-1"}
          avatarDataUrl={header.avatarDataUrl}
          avatarIcon={header.avatarIcon}
          size="sm"
        />
      }
      loadPage={loadPage}
      getRowKey={(session) => session.id}
      renderRow={(session, close) => (
        <SessionRow
          session={session}
          onSelect={(id, opts) => {
            onSelectSession(id, opts);
            close();
          }}
        />
      )}
      onClose={onClose}
      side="right"
      align="start"
      sideOffset={8}
    />
  );
}

export { SessionsPopover };
