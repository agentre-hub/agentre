import * as React from "react";
import { ChevronDown, Loader2, Search, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { isOpenInNewTabModifier } from "@agentre-ai/agentre-ui";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PopoverContent } from "@/components/ui/popover";
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
  // 头部右侧的「N 运行中」灯：可选。
  activeCount?: number;
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
  const { t } = useTranslation();
  const [sessions, setSessions] = React.useState<SessionsPopoverItem[]>([]);
  const [total, setTotal] = React.useState(0);
  const [hasMore, setHasMore] = React.useState(false);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [filter, setFilter] = React.useState("");

  const loadingRef = React.useRef(false);

  const fetchPage = React.useCallback(
    async (offset: number) => {
      if (loadingRef.current) return;
      loadingRef.current = true;
      setLoading(true);
      setError(null);
      try {
        const resp = await loader({ offset, limit: PAGE_SIZE });
        setSessions((prev) =>
          offset === 0 ? resp.sessions : [...prev, ...resp.sessions],
        );
        setTotal(resp.total);
        setHasMore(resp.hasMore);
      } catch (e: unknown) {
        setError(e instanceof Error ? e.message : String(e));
      } finally {
        setLoading(false);
        loadingRef.current = false;
      }
    },
    [loader],
  );

  // Radix Popover 通过 Portal 在打开时挂载、关闭时卸载；
  // 所以挂载即首次拉取，关闭后下次重开会重新拉（保持数据最新）。
  React.useEffect(() => {
    void fetchPage(0);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only fetch initial page on mount
  }, []);

  const filterValue = filter.trim().toLowerCase();
  const visibleSessions = filterValue
    ? sessions.filter((s) => s.title.toLowerCase().includes(filterValue))
    : sessions;

  return (
    <PopoverContent
      side="right"
      align="start"
      sideOffset={8}
      className="flex h-[480px] w-[340px] flex-col gap-0 p-0"
      onEscapeKeyDown={onClose}
      onInteractOutside={onClose}
    >
      <header className="flex items-center gap-2 border-b border-border px-3 py-2.5">
        <AgentAvatar
          name={header.name}
          initials={header.name.charAt(0)}
          color={(header.avatarColor as AgentColor) || "agent-1"}
          avatarDataUrl={header.avatarDataUrl}
          avatarIcon={header.avatarIcon}
          size="sm"
        />
        <div className="min-w-0 flex-1">
          <div className="truncate text-xs font-semibold">{header.name}</div>
          <div className="mt-0.5 flex items-center gap-1.5 font-mono text-2xs text-muted-foreground">
            <span>{t("sessionsPopover.total", { count: total })}</span>
            {(header.activeCount ?? 0) > 0 ? (
              <>
                <span className="text-border-strong">·</span>
                <StatusDot status="running" size="xs" />
                <span className="font-semibold text-status-running">
                  {t("sessionsPopover.running", {
                    count: header.activeCount,
                  })}
                </span>
              </>
            ) : null}
          </div>
        </div>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={t("common.close")}
          onClick={onClose}
        >
          <X data-icon="only" aria-hidden="true" />
        </Button>
      </header>

      <div className="border-b border-border px-3 py-2">
        <div className="relative">
          <Search
            className="pointer-events-none absolute left-2 top-1/2 size-3 -translate-y-1/2 text-muted-foreground"
            aria-hidden="true"
          />
          <Input
            aria-label={t("sessionsPopover.search.aria")}
            placeholder={t("sessionsPopover.search.placeholder")}
            className="h-7 bg-input-bg pl-7 pr-2 text-xs"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-auto px-1.5 py-1.5">
        {error ? (
          <div className="px-3 py-4 text-center text-2xs text-status-error">
            {t("sessionsPopover.loadFailed", { error })}
          </div>
        ) : null}
        {!error && sessions.length === 0 && !loading ? (
          <div className="px-3 py-6 text-center text-2xs text-muted-foreground">
            {t("sessionsPopover.empty")}
          </div>
        ) : null}
        {visibleSessions.map((s) => (
          <SessionRow
            key={s.id}
            session={s}
            onSelect={(id, opts) => {
              onSelectSession(id, opts);
              onClose();
            }}
          />
        ))}
        {filterValue && visibleSessions.length === 0 && sessions.length > 0 ? (
          <div className="px-3 py-4 text-center text-2xs text-muted-foreground">
            {t("sessionsPopover.noMatches", { query: filter.trim() })}
          </div>
        ) : null}
      </div>

      {hasMore || loading ? (
        <footer className="flex items-center justify-center gap-2 border-t border-border bg-muted/40 px-3 py-1.5">
          <span className="font-mono text-2xs text-muted-foreground">
            {t("sessionsPopover.loaded", {
              loaded: sessions.length,
              total,
            })}
          </span>
          {hasMore ? (
            <>
              <span className="font-mono text-2xs text-border-strong">·</span>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-6 px-2 text-2xs"
                disabled={loading}
                onClick={() => void fetchPage(sessions.length)}
              >
                {loading ? (
                  <Loader2 className="size-3 animate-spin" aria-hidden="true" />
                ) : (
                  <ChevronDown className="size-3" aria-hidden="true" />
                )}
                {t("sessionsPopover.loadMore")}
              </Button>
            </>
          ) : null}
        </footer>
      ) : null}
    </PopoverContent>
  );
}

export { SessionsPopover };
