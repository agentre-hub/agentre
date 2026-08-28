import type * as React from "react";
import { Plus, Search, UserPlus, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
  AxisPicker,
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Input,
} from "@agentre-hub/agentre-ui";

import { INDEX_AXES, type IndexAxis } from "@/lib/session-axis";
import { cn } from "@/lib/utils";

import type { StatusFilter } from "./use-index-filter";

type IndexToolbarProps = {
  query: string;
  setQuery: React.Dispatch<React.SetStateAction<string>>;
  axis: IndexAxis;
  setAxis: (axis: IndexAxis) => void;
  statusFilter: StatusFilter;
  setStatusFilter: React.Dispatch<React.SetStateAction<StatusFilter>>;
  unreadCount: number;
  onOpenCommandPalette: () => void;
  onCreateProject: () => void;
  onNewAgent: () => void;
};

// IndexToolbar:左栏顶部那一格 —— 搜索框、＋ 菜单、轴选择器、状态筛选 chips。
function IndexToolbar({
  query,
  setQuery,
  axis,
  setAxis,
  statusFilter,
  setStatusFilter,
  unreadCount,
  onOpenCommandPalette,
  onCreateProject,
  onNewAgent,
}: IndexToolbarProps) {
  const { t } = useTranslation();
  return (
    <div className="border-b border-border px-4 py-3">
      <div className="flex items-center gap-2">
        <div className="relative min-w-0 flex-1">
          <Search
            className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"
            aria-hidden="true"
          />
          <Input
            aria-label={t("sessionIndex.search.aria")}
            placeholder={t("sessionIndex.search.placeholder")}
            className="h-[30px] bg-background pl-8 pr-7 text-xs"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
          {query ? (
            <button
              type="button"
              aria-label={t("sessionIndex.search.clear")}
              title={t("sessionIndex.search.clear")}
              className="absolute right-1.5 top-1/2 inline-flex size-5 -translate-y-1/2 cursor-pointer items-center justify-center rounded text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
              onClick={() => setQuery("")}
            >
              <X className="size-3" aria-hidden="true" />
            </button>
          ) : null}
        </div>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              data-testid="new-chat-button"
              variant="secondary"
              size="icon-sm"
              aria-label={t("sessionIndex.add.aria")}
              title={t("sessionIndex.add.aria")}
              className="size-[30px] bg-primary-soft text-primary-text hover:bg-primary-soft/80"
            >
              <Plus data-icon="only" aria-hidden="true" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {/* 副标题不是装饰：这一项开的是命令面板，而「在面板里按 Tab 能换
                项目」是这条路唯一能开出「不挂项目」的会话的办法（决策 6 的第二
                条路），不写出来没人会去按。 */}
            <DropdownMenuItem
              data-testid="new-agent-chat-item"
              onSelect={onOpenCommandPalette}
            >
              <div className="flex min-w-0 flex-col gap-0.5">
                <span>{t("sessionIndex.add.newChat")}</span>
                <span className="text-2xs text-muted-foreground">
                  {t("sessionIndex.add.newChatHint")}
                </span>
              </div>
            </DropdownMenuItem>
            {/* 决策 11：「新建项目」降为次级项 —— ＋ 只有一种含义（开一条对话）。 */}
            <DropdownMenuItem
              data-testid="project-create-trigger"
              onSelect={onCreateProject}
            >
              {t("sessionIndex.add.newProject")}
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              data-testid="new-agent-item"
              onSelect={onNewAgent}
            >
              <UserPlus className="size-4" aria-hidden="true" />
              <div className="flex min-w-0 flex-col gap-0.5">
                <span>{t("sessionIndex.add.newAgent")}</span>
                <span className="text-2xs text-muted-foreground">
                  {t("sessionIndex.add.newAgentHint")}
                </span>
              </div>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      <div className="mt-2 flex items-center gap-1.5">
        <AxisPicker
          value={axis}
          axes={INDEX_AXES}
          // 包的词汇表比桌面端 offer 的多一档（machine）。选择器只摆得出
          // `axes` 里那几档，所以回来的一定是其中之一 —— 这里把它收回宿主
          // 的窄类型，而不是把窄类型放宽到包的全集。
          onChange={(next) => setAxis(next as IndexAxis)}
        />
        <Chip
          testID="filter-chip-all"
          active={statusFilter === null}
          onClick={() => setStatusFilter(null)}
        >
          {t("sessionIndex.filter.all")}
        </Chip>
        <Chip
          testID="filter-chip-running"
          active={statusFilter === "running"}
          onClick={() =>
            setStatusFilter((p) => (p === "running" ? null : "running"))
          }
        >
          {t("sessionIndex.filter.running")}
        </Chip>
        <Chip
          testID="filter-chip-unread"
          active={statusFilter === "unread"}
          onClick={() =>
            setStatusFilter((p) => (p === "unread" ? null : "unread"))
          }
        >
          {t("sessionIndex.filter.unread")}
          {unreadCount > 0 ? (
            <span className="rounded-full bg-status-waiting-bg px-1 font-medium text-status-waiting">
              {unreadCount}
            </span>
          ) : null}
        </Chip>
      </div>
    </div>
  );
}

function Chip({
  testID,
  active,
  onClick,
  children,
}: {
  testID: string;
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      data-testid={testID}
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        "inline-flex cursor-pointer items-center gap-1 rounded-full px-2.5 py-1 text-2xs outline-none transition-colors focus-visible:ring-[3px] focus-visible:ring-ring/50",
        active
          ? "bg-primary-soft font-medium text-primary-text"
          : "bg-sidebar-active-bg text-muted-foreground hover:text-foreground",
      )}
    >
      {children}
    </button>
  );
}

export { IndexToolbar };
export type { IndexToolbarProps };
