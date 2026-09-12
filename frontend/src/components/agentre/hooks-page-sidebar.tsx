import type { TFunction } from "i18next";
import { Plus, Search } from "lucide-react";

import { Button } from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

import { hookStatus, interpMeta, type HookItem } from "./hooks-page-model";

/** 左栏：计划任务列表 + 搜索 + 新建入口。 */
export function HooksSidebar({
  hooks,
  filtered,
  query,
  selectedId,
  onQueryChange,
  onSelect,
  onCreate,
  t,
}: {
  hooks: HookItem[];
  filtered: HookItem[];
  query: string;
  selectedId: number | null;
  onQueryChange: (value: string) => void;
  onSelect: (hook: HookItem) => void;
  onCreate: () => void;
  t: TFunction;
}) {
  return (
    <aside className="flex w-64 shrink-0 flex-col border-r border-border bg-sidebar">
      <div className="flex flex-col gap-2.5 border-b border-border p-3.5">
        <div className="flex items-center gap-2">
          <span className="text-sm font-semibold text-foreground">
            {t("hooks.title")}
          </span>
          <span className="font-mono text-2xs text-muted-foreground">
            {hooks.length}
          </span>
          <span className="flex-1" />
          <Button
            type="button"
            size="icon"
            className="h-6 w-6"
            aria-label={t("hooks.list.addAria")}
            data-testid="hook-create"
            onClick={onCreate}
          >
            <Plus className="h-3.5 w-3.5" />
          </Button>
        </div>
        <div className="flex items-center gap-2 rounded-md border border-input bg-input-bg px-2.5">
          <Search className="h-3.5 w-3.5 text-muted-foreground" />
          <input
            value={query}
            onChange={(e) => onQueryChange(e.target.value)}
            placeholder={t("hooks.list.search")}
            aria-label={t("hooks.list.search")}
            className="h-8 flex-1 bg-transparent text-xs outline-none placeholder:text-muted-foreground"
          />
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-2">
        {filtered.length === 0 ? (
          <div className="flex flex-col items-center gap-1 px-3 py-10 text-center">
            <p className="text-xs text-muted-foreground">
              {t("hooks.list.empty")}
            </p>
            <p className="text-2xs text-muted-foreground/70">
              {t("hooks.list.emptyHint")}
            </p>
          </div>
        ) : (
          <div className="flex flex-col gap-0.5">
            <span className="px-1.5 pb-1 pt-2 text-3xs font-semibold uppercase text-muted-foreground">
              {t("hooks.list.groupScheduled")}
            </span>
            {filtered.map((hook) => (
              <HookListItem
                key={hook.id}
                hook={hook}
                selected={hook.id === selectedId}
                onSelect={() => onSelect(hook)}
                t={t}
              />
            ))}
          </div>
        )}
      </div>
    </aside>
  );
}

function HookListItem({
  hook,
  selected,
  onSelect,
  t,
}: {
  hook: HookItem;
  selected: boolean;
  onSelect: () => void;
  t: TFunction;
}) {
  const meta = interpMeta(hook.interpreter);
  const Icon = meta.icon;
  const status = hookStatus(hook);
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-current={selected ? "true" : undefined}
      className={cn(
        "flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-left transition-colors",
        selected
          ? "border-l-2 border-primary bg-primary/5"
          : "hover:bg-muted/60",
      )}
    >
      <span
        className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-agent-foreground"
        style={{ backgroundColor: `var(--color-${meta.color})` }}
      >
        <Icon className="h-[15px] w-[15px]" />
      </span>
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="truncate text-xs font-semibold text-foreground">
          {hook.name}
        </span>
        <span className="truncate font-mono text-3xs text-muted-foreground">
          {meta.abbrev} · {hook.scheduleExpr}
        </span>
      </span>
      {status === "disabled" ? (
        <span className="font-mono text-3xs font-bold text-muted-foreground/70">
          {t("hooks.list.disabled")}
        </span>
      ) : (
        <span
          className={cn(
            "h-[7px] w-[7px] shrink-0 rounded-full",
            status === "failed"
              ? "bg-status-error"
              : status === "idle"
                ? "bg-muted-foreground/40"
                : "bg-status-running",
          )}
        />
      )}
    </button>
  );
}
