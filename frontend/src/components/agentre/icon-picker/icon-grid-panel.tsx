// 图标网格:一格格可点的图标(含单选态与悬停提示)。

import * as React from "react";
import { Search, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button, Input } from "@agentre-hub/agentre-ui";

import { cn } from "@/lib/utils";

import { iconsByCategory, searchIcons, type IconMeta } from "../icon-registry";

export type IconGridPanelProps = {
  value: string;
  onSelect: (key: string) => void;
  allowClear?: boolean;
  onClear?: () => void;
};

export function IconGridPanel({
  value,
  onSelect,
  allowClear,
  onClear,
}: IconGridPanelProps) {
  const { t } = useTranslation();
  const [query, setQuery] = React.useState("");
  const trimmed = query.trim();
  const groups = React.useMemo(() => {
    if (trimmed) {
      const flat = searchIcons(trimmed);
      return [
        {
          category: "search",
          label: t("iconPicker.search.results"),
          items: flat,
        },
      ];
    }
    return iconsByCategory();
  }, [t, trimmed]);

  return (
    <div className="flex flex-col">
      <div className="border-b border-border px-3 py-2">
        <div className="relative">
          <Search
            className="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"
            aria-hidden="true"
          />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            aria-label={t("iconPicker.search.aria")}
            placeholder={t("iconPicker.search.placeholder")}
            className="h-8 pl-7 text-xs"
          />
        </div>
      </div>
      <div className="max-h-[280px] overflow-y-auto px-2 py-2">
        {groups.map((g, i) => (
          <div
            key={g.category}
            className={cn("space-y-1.5", i > 0 && "mt-3")}
            data-icon-group={g.category}
          >
            {!trimmed && (
              <div className="px-1 font-mono text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
                {g.label}
              </div>
            )}
            {g.items.length === 0 ? (
              <div className="px-1 py-2 text-2xs text-muted-foreground">
                {t("iconPicker.search.noMatches")}
              </div>
            ) : (
              <div className="grid grid-cols-6 gap-1.5">
                {g.items.map((m) => (
                  <IconCell
                    key={m.key}
                    meta={m}
                    active={value === m.key}
                    onSelect={() => onSelect(m.key)}
                  />
                ))}
              </div>
            )}
          </div>
        ))}
      </div>
      {allowClear && (
        <div className="border-t border-border px-3 py-2">
          <Button
            variant="ghost"
            size="sm"
            className="h-7 w-full justify-start gap-1.5 text-xs text-muted-foreground"
            onClick={() => onClear?.()}
          >
            <X className="size-3.5" aria-hidden="true" />
            {t("iconPicker.search.clearIcon")}
          </Button>
        </div>
      )}
    </div>
  );
}

export function IconCell({
  meta,
  active,
  onSelect,
}: {
  meta: IconMeta;
  active: boolean;
  onSelect: () => void;
}) {
  const Icon = meta.icon;
  return (
    <button
      type="button"
      role="radio"
      aria-checked={active}
      aria-label={`${meta.label} (${meta.key})`}
      title={meta.label}
      onClick={onSelect}
      className={cn(
        "inline-flex aspect-square items-center justify-center rounded-md border text-foreground transition-colors",
        active
          ? "border-primary bg-primary-soft text-primary-text"
          : "border-border bg-card hover:bg-accent",
      )}
    >
      <Icon className="size-4" aria-hidden="true" />
    </button>
  );
}

// ----------------------------------------------------------------------------
// 内部：模式切换 chip
// ----------------------------------------------------------------------------
