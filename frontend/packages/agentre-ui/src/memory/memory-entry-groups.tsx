import * as React from "react";
import { ChevronRight } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";

import { MemoryRow } from "./memory-row";
import type { MemoryEntryGroupsProps, MemoryEntryView } from "./types";

/**
 * 一个归属下的条目分组（spec「记忆页 · 分组」）：正常条目（置顶在前）；「超出预算，
 * 本次不带」单独成组并变淡；「已归档」单独成组、默认收起；agent 归属最后是「继承的」，
 * 按来源层列出条数，点开跳到那一层。空组不画。
 */
export function MemoryEntryGroups({
  normalLabel,
  normal,
  overBudget,
  archived,
  inherited,
  editingId,
  onEditingChange,
  onSave,
  onAction,
  moveTargetsFor,
}: MemoryEntryGroupsProps) {
  const { t } = useUiTranslation();
  const [archivedOpen, setArchivedOpen] = React.useState(false);
  const archivedBodyId = React.useId();

  const rows = (entries: MemoryEntryView[], dimmed: boolean) =>
    entries.map((entry) => (
      <MemoryRow
        key={entry.id}
        entry={entry}
        dimmed={dimmed}
        editing={editingId === entry.id}
        onEditingChange={(editing) =>
          onEditingChange(editing ? entry.id : null)
        }
        onSave={(draft) => onSave(entry.id, draft)}
        onAction={(action) => onAction(entry.id, action)}
        moveTargets={moveTargetsFor(entry)}
      />
    ));

  return (
    <div className="flex flex-col gap-5" data-slot="memory-entry-groups">
      {normal.length > 0 ? (
        <Group label={normalLabel} count={normal.length}>
          {rows(normal, false)}
        </Group>
      ) : null}
      {overBudget.length > 0 ? (
        <Group label={t("memory.groups.overBudget")} count={overBudget.length}>
          {rows(overBudget, true)}
        </Group>
      ) : null}
      {archived.length > 0 ? (
        <section
          aria-label={t("memory.groups.archived")}
          className="flex flex-col gap-1"
        >
          <h3 className="px-2">
            <button
              type="button"
              aria-expanded={archivedOpen}
              aria-controls={archivedBodyId}
              className="flex items-center gap-1 rounded-sm text-xs font-semibold text-foreground"
              onClick={() => setArchivedOpen((open) => !open)}
            >
              <ChevronRight
                aria-hidden="true"
                className={cn(
                  "size-3.5 text-muted-foreground transition-transform duration-150 motion-reduce:transition-none",
                  archivedOpen && "rotate-90",
                )}
              />
              {t("memory.groups.archived")}
              <span className="ml-1 font-mono font-normal text-muted-foreground">
                {archived.length}
              </span>
            </button>
          </h3>
          <div id={archivedBodyId} hidden={!archivedOpen}>
            {archivedOpen ? rows(archived, false) : null}
          </div>
        </section>
      ) : null}
      {inherited && inherited.length > 0 ? (
        <Group label={t("memory.groups.inherited")}>
          {inherited.map((layer) => (
            <button
              key={layer.key}
              type="button"
              aria-label={t("memory.groups.inheritedOpen", {
                label: layer.label,
                count: layer.count,
              })}
              className="flex items-center gap-2 rounded-lg px-2 py-1.5 text-left text-sm text-foreground hover:bg-accent/60 hover:on-accent"
              onClick={layer.onOpen}
            >
              <span className="min-w-0 flex-1 truncate">{layer.label}</span>
              <span className="font-mono text-2xs text-muted-foreground">
                {layer.count}
              </span>
              <ChevronRight
                aria-hidden="true"
                className="size-3.5 text-decorative-foreground"
              />
            </button>
          ))}
        </Group>
      ) : null}
    </div>
  );
}

function Group({
  label,
  count,
  children,
}: {
  label: string;
  count?: number;
  children: React.ReactNode;
}) {
  return (
    <section aria-label={label} className="flex flex-col gap-1">
      <h3 className="px-2 text-xs font-semibold text-foreground">
        {label}
        {count !== undefined ? (
          <span className="ml-1.5 font-mono font-normal text-muted-foreground">
            {count}
          </span>
        ) : null}
      </h3>
      {children}
    </section>
  );
}
