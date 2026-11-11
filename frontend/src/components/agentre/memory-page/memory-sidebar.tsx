import { History } from "lucide-react";
import { useTranslation } from "react-i18next";

import { SearchInput, cn } from "@agentre-hub/agentre-ui";

import type { MemoryDirectory, MemoryDirectoryEntry } from "./directory";
import { OwnerMark } from "./owner-mark";

/** 左列：搜索、最近变更、全局，以及部门（按组织树缩进）、Agent、项目三组。 */
export function MemorySidebar({
  directory,
  counts,
  recentCount,
  selectedKey,
  query,
  onQueryChange,
  onSelectOwner,
  onSelectRecent,
}: {
  directory: MemoryDirectory;
  counts: Map<string, number>;
  recentCount: number;
  /** 选中的归属键；选中「最近变更」时为 "recent"。 */
  selectedKey: string;
  query: string;
  onQueryChange: (value: string) => void;
  onSelectOwner: (entry: MemoryDirectoryEntry) => void;
  onSelectRecent: () => void;
}) {
  const { t } = useTranslation();
  const ownerRow = (entry: MemoryDirectoryEntry) => (
    <SidebarRow
      key={entry.key}
      label={entry.label}
      count={counts.get(entry.key) ?? 0}
      depth={entry.depth}
      selected={selectedKey === entry.key}
      icon={<OwnerMark entry={entry} size="xs" />}
      onClick={() => onSelectOwner(entry)}
    />
  );
  const groups: Array<[string, MemoryDirectoryEntry[]]> = [
    [t("memoryPage.sidebar.departments"), directory.departments],
    [t("memoryPage.sidebar.agents"), directory.agents],
    [t("memoryPage.sidebar.projects"), directory.projects],
  ];

  return (
    <nav
      aria-label={t("memoryPage.sidebar.label")}
      className="flex w-64 shrink-0 flex-col bg-chrome"
    >
      <div className="p-3">
        <SearchInput
          value={query}
          onChange={onQueryChange}
          placeholder={t("memoryPage.sidebar.search")}
          aria-label={t("memoryPage.sidebar.search")}
        />
      </div>
      <div className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto px-2 pb-3">
        <SidebarRow
          label={t("memoryPage.sidebar.recent")}
          count={recentCount}
          depth={0}
          selected={selectedKey === "recent"}
          icon={
            <History
              aria-hidden="true"
              className="size-4 text-muted-foreground"
            />
          }
          onClick={onSelectRecent}
        />
        {ownerRow(directory.global)}
        {groups.map(([label, entries]) =>
          entries.length === 0 ? null : (
            <section key={label} className="flex flex-col gap-0.5 pt-3">
              <h2 className="px-2.5 pb-1 text-2xs font-medium text-muted-foreground">
                {label}
              </h2>
              {entries.map(ownerRow)}
            </section>
          ),
        )}
      </div>
    </nav>
  );
}

function SidebarRow({
  label,
  count,
  depth,
  selected,
  icon,
  onClick,
}: {
  label: string;
  count: number;
  depth: number;
  selected: boolean;
  icon: React.ReactNode;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      data-depth={depth}
      aria-current={selected ? "true" : undefined}
      onClick={onClick}
      className={cn(
        "flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-sm text-foreground transition-colors motion-reduce:transition-none",
        selected ? "bg-raised" : "hover:bg-field-fill-hover",
      )}
      // 按树缩进是逐层计算出来的值，Tailwind 写不出任意层数。
      style={depth > 0 ? { paddingLeft: `${0.625 + depth * 1}rem` } : undefined}
    >
      {icon}
      <span className="min-w-0 flex-1 truncate">{label}</span>
      <span className="font-mono text-2xs text-muted-foreground">{count}</span>
    </button>
  );
}
