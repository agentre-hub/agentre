import * as React from "react";
import {
  Circle,
  CircleAlert,
  CircleCheck,
  CircleDot,
  Columns3,
  List,
  MoreHorizontal,
  Plus,
  SlidersHorizontal,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { cn } from "@/lib/utils";
import { useIssues } from "@/hooks/use-issues";
import { useProjectList } from "@/hooks/use-project-list";

import { IssueNewDialog } from "./issue-new-dialog";
import { IssueLabels } from "./issue-labels";
import { toneClass } from "./issue-tones";
import { IssuesBoard } from "./kanban/issues-board";
import { IssueDelete, IssueSetState } from "../../../wailsjs/go/app/App";
import type { app } from "../../../wailsjs/go/models";

type IssueView = "list" | "board";

const statusIconMeta: Record<string, { className: string; icon: LucideIcon }> =
  {
    error: { icon: CircleAlert, className: "text-status-error" },
    idle: { icon: Circle, className: "text-muted-foreground" },
    running: { icon: CircleDot, className: "text-status-running" },
    waiting: { icon: CircleDot, className: "text-status-waiting" },
  };

function statusIcon(agentStatus: string) {
  return statusIconMeta[agentStatus] ?? statusIconMeta.idle;
}

function IssuesPage() {
  const { t } = useTranslation();
  const [view, setView] = React.useState<IssueView>("board");
  const [tab, setTab] = React.useState<"open" | "closed">("open");
  const [labelIDs, setLabelIDs] = React.useState<number[]>([]);
  const [dialogOpen, setDialogOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<app.IssueItem | null>(null);

  const effectiveState = view === "board" ? "" : tab;
  const {
    issues,
    labels,
    openCount,
    closedCount,
    stageCounts,
    loading,
    error,
    reload,
    moveIssue,
  } = useIssues({
    state: effectiveState,
    projectID: 0,
    labelIDs,
    sort: view === "board" ? "position" : "updated",
  });
  const { projects } = useProjectList();

  const summary = t("issues.summary.counts", {
    open: openCount,
    closed: closedCount,
  });

  const openCreate = () => {
    setEditing(null);
    setDialogOpen(true);
  };
  const openEdit = (issue: app.IssueItem) => {
    setEditing(issue);
    setDialogOpen(true);
  };
  const setState = async (issue: app.IssueItem, state: string) => {
    await IssueSetState({ id: issue.id, state });
    void reload();
  };
  const remove = async (issue: app.IssueItem) => {
    await IssueDelete(issue.id);
    void reload();
  };

  return (
    <main
      className="flex min-h-0 min-w-0 flex-1 flex-col bg-background"
      data-slot="issues-page"
    >
      <IssuesHeader
        view={view}
        summary={summary}
        onViewChange={setView}
        onNewIssue={openCreate}
      />
      <IssueFilterBar
        view={view}
        tab={tab}
        onTabChange={setTab}
        openCount={openCount}
        closedCount={closedCount}
        labels={labels}
        selectedLabelIDs={labelIDs}
        onToggleLabel={(id) =>
          setLabelIDs((ids) =>
            ids.includes(id) ? ids.filter((x) => x !== id) : [...ids, id],
          )
        }
      />
      {loading && issues.length === 0 ? (
        <CenterNote text={t("issues.state.loading")} />
      ) : error ? (
        <CenterNote text={t("issues.state.error")} />
      ) : issues.length === 0 ? (
        <IssuesEmpty onNewIssue={openCreate} />
      ) : view === "list" ? (
        <IssuesList
          issues={issues}
          onEdit={openEdit}
          onSetState={setState}
          onDelete={remove}
        />
      ) : view === "board" ? (
        <IssuesBoard
          issues={issues}
          stageCounts={stageCounts}
          onEdit={openEdit}
          onMove={async (id, stage, afterID) => {
            try {
              await moveIssue(id, stage, afterID);
            } finally {
              void reload();
            }
          }}
        />
      ) : null}
      <IssueNewDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        projects={projects}
        labels={labels}
        editing={editing}
        onSaved={reload}
      />
    </main>
  );
}

function CenterNote({ text }: { text: string }) {
  return (
    <div className="flex min-h-0 flex-1 items-center justify-center text-xs text-muted-foreground">
      {text}
    </div>
  );
}

function IssuesEmpty({ onNewIssue }: { onNewIssue: () => void }) {
  const { t } = useTranslation();
  return (
    <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 px-6 text-center">
      <h2 className="text-sm font-semibold">{t("issues.empty.title")}</h2>
      <p className="max-w-sm text-xs text-muted-foreground">
        {t("issues.empty.desc")}
      </p>
      <Button type="button" size="sm" onClick={onNewIssue}>
        <Plus data-icon="inline-start" aria-hidden="true" />
        {t("issues.actions.newIssue")}
      </Button>
    </div>
  );
}

type IssuesHeaderProps = {
  onViewChange: (view: IssueView) => void;
  onNewIssue: () => void;
  summary: string;
  view: IssueView;
};

function IssuesHeader({
  onViewChange,
  onNewIssue,
  summary,
  view,
}: IssuesHeaderProps) {
  const { t } = useTranslation();
  return (
    <header className="flex min-h-[60px] shrink-0 flex-wrap items-center gap-3 border-b border-border bg-background px-5 py-3 lg:h-[60px] lg:flex-nowrap lg:py-0">
      <div className="flex min-w-0 flex-col gap-0.5">
        <h1 className="truncate text-base font-semibold tracking-normal">
          {t("issues.title")}
        </h1>
        <p className="truncate text-2xs text-muted-foreground">{summary}</p>
      </div>
      <div className="min-w-0 flex-1" />
      <div
        className="flex h-[30px] shrink-0 items-center gap-0.5 rounded-md border border-border bg-secondary p-0.5"
        aria-label={t("issues.view.aria")}
      >
        <IssueViewButton
          active={view === "list"}
          icon={List}
          label={t("issues.view.list")}
          onClick={() => onViewChange("list")}
        />
        <IssueViewButton
          active={view === "board"}
          icon={Columns3}
          label={t("issues.view.board")}
          onClick={() => onViewChange("board")}
        />
      </div>
      <Button type="button" size="sm" className="h-[30px]" onClick={onNewIssue}>
        <Plus data-icon="inline-start" aria-hidden="true" />
        {t("issues.actions.newIssue")}
      </Button>
    </header>
  );
}

type IssueViewButtonProps = {
  active: boolean;
  icon: LucideIcon;
  label: string;
  onClick: () => void;
};

function IssueViewButton({
  active,
  icon: Icon,
  label,
  onClick,
}: IssueViewButtonProps) {
  return (
    <button
      type="button"
      aria-pressed={active}
      className={cn(
        "inline-flex h-full cursor-pointer items-center justify-center gap-1.5 rounded-sm px-2.5 text-xs font-medium outline-none transition-colors focus-visible:ring-[3px] focus-visible:ring-ring/40",
        active
          ? "border border-border bg-card text-foreground shadow-xs"
          : "text-muted-foreground hover:text-foreground",
      )}
      onClick={onClick}
    >
      <Icon className="size-3.5" aria-hidden="true" />
      {label}
    </button>
  );
}

type IssueFilterBarProps = {
  view: IssueView;
  tab: "open" | "closed";
  onTabChange: (tab: "open" | "closed") => void;
  openCount: number;
  closedCount: number;
  labels: app.LabelItem[];
  selectedLabelIDs: number[];
  onToggleLabel: (id: number) => void;
};

function IssueFilterBar({
  view,
  tab,
  onTabChange,
  openCount,
  closedCount,
  labels,
  selectedLabelIDs,
  onToggleLabel,
}: IssueFilterBarProps) {
  const { t } = useTranslation();
  return (
    <div className="flex min-h-12 shrink-0 items-center gap-2 overflow-x-auto border-b border-border bg-sidebar px-5 py-2">
      {view === "list" ? (
        <div className="flex shrink-0 items-center gap-0.5">
          <FilterTab
            active={tab === "open"}
            icon={CircleDot}
            label={t("issues.filters.open")}
            count={openCount}
            onClick={() => onTabChange("open")}
          />
          <FilterTab
            active={tab === "closed"}
            icon={CircleCheck}
            label={t("issues.filters.closed")}
            count={closedCount}
            onClick={() => onTabChange("closed")}
          />
        </div>
      ) : null}
      <div className="min-w-0 flex-1" />
      <LabelFilter
        labels={labels}
        selectedLabelIDs={selectedLabelIDs}
        onToggle={onToggleLabel}
      />
    </div>
  );
}

type FilterTabProps = {
  active: boolean;
  count: number;
  icon: LucideIcon;
  label: string;
  onClick: () => void;
};

function FilterTab({
  active,
  count,
  icon: Icon,
  label,
  onClick,
}: FilterTabProps) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        "inline-flex cursor-pointer items-center gap-1.5 rounded-md px-2.5 py-1.5 text-sm font-medium outline-none transition-colors focus-visible:ring-[3px] focus-visible:ring-ring/40",
        active
          ? "border border-primary bg-primary-soft text-primary-text"
          : "text-muted-foreground hover:bg-accent hover:text-foreground",
      )}
    >
      <Icon className="size-3.5" aria-hidden="true" />
      {label}
      <span className="font-mono text-2xs text-muted-foreground">{count}</span>
    </button>
  );
}

function LabelFilter({
  labels,
  selectedLabelIDs,
  onToggle,
}: {
  labels: app.LabelItem[];
  selectedLabelIDs: number[];
  onToggle: (id: number) => void;
}) {
  const { t } = useTranslation();
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button type="button" variant="outline" size="sm" className="h-7">
          <SlidersHorizontal data-icon="inline-start" aria-hidden="true" />
          {t("issues.filters.label")}
          {selectedLabelIDs.length > 0 ? (
            <span className="ml-1 font-mono text-2xs">
              {selectedLabelIDs.length}
            </span>
          ) : null}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-48">
        <div className="flex flex-wrap gap-1.5">
          {labels.map((l) => {
            const selected = selectedLabelIDs.includes(l.id);
            return (
              <button
                type="button"
                key={l.id}
                aria-pressed={selected}
                onClick={() => onToggle(l.id)}
                className={cn(
                  "cursor-pointer rounded-full border px-2 py-px font-mono text-2xs font-semibold transition-colors",
                  selected
                    ? cn(toneClass(l.tone), "border-transparent")
                    : "border-border text-muted-foreground hover:bg-accent",
                )}
              >
                {l.name}
              </button>
            );
          })}
        </div>
      </PopoverContent>
    </Popover>
  );
}

type RowActionsProps = {
  issue: app.IssueItem;
  onEdit: (issue: app.IssueItem) => void;
  onSetState: (issue: app.IssueItem, state: string) => void;
  onDelete: (issue: app.IssueItem) => void;
};

function RowActions({ issue, onEdit, onSetState, onDelete }: RowActionsProps) {
  const { t } = useTranslation();
  const isOpen = issue.state === "open";
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={t("common.moreActions")}
        >
          <MoreHorizontal data-icon="only" aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onSelect={() => onEdit(issue)}>
          {t("issues.actions.edit")}
        </DropdownMenuItem>
        <DropdownMenuItem
          onSelect={() => onSetState(issue, isOpen ? "closed" : "open")}
        >
          {isOpen ? t("issues.actions.close") : t("issues.actions.reopen")}
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          variant="destructive"
          onSelect={() => onDelete(issue)}
        >
          {t("issues.actions.delete")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function IssuesList({
  issues,
  onEdit,
  onSetState,
  onDelete,
}: {
  issues: app.IssueItem[];
  onEdit: (issue: app.IssueItem) => void;
  onSetState: (issue: app.IssueItem, state: string) => void;
  onDelete: (issue: app.IssueItem) => void;
}) {
  const { t } = useTranslation();
  return (
    <section
      aria-label={t("issues.list.aria")}
      className="min-h-0 flex-1 overflow-auto bg-background"
    >
      <div role="list" className="min-w-[760px]">
        {issues.map((issue) => {
          const Icon = statusIcon(issue.agentStatus).icon;
          return (
            <article
              role="listitem"
              key={issue.id}
              className="flex min-h-[68px] items-center gap-3.5 border-b border-border px-5 py-3.5 transition-colors hover:bg-accent/40"
            >
              <Icon
                className={cn(
                  "size-4 shrink-0",
                  statusIcon(issue.agentStatus).className,
                )}
                aria-hidden="true"
              />
              <button
                type="button"
                onClick={() => onEdit(issue)}
                className="flex min-w-0 flex-1 cursor-pointer flex-col gap-1.5 text-left outline-none"
              >
                <div className="flex min-w-0 flex-wrap items-center gap-2">
                  <span className="truncate text-sm font-semibold">
                    {issue.title}
                  </span>
                  <IssueLabels labels={issue.labels} />
                </div>
                <div className="flex min-w-0 flex-wrap items-center gap-1.5 font-mono text-2xs">
                  <span className="font-medium text-primary-text">
                    #{issue.id}
                  </span>
                  <IssueUpdatedAt value={issue.updatetime} />
                </div>
              </button>
              <RowActions
                issue={issue}
                onEdit={onEdit}
                onSetState={onSetState}
                onDelete={onDelete}
              />
            </article>
          );
        })}
      </div>
    </section>
  );
}

function IssueUpdatedAt({ value }: { value?: number }) {
  if (!value || value <= 0) {
    return null;
  }
  return (
    <span className="truncate text-muted-foreground">
      · {new Date(value).toLocaleDateString()}
    </span>
  );
}

export { IssuesPage };
