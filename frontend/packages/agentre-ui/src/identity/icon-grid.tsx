import * as React from "react";
import { Search } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { iconForKey, iconsByCategory, searchIcons } from "../org/icon-registry";
import { searchPresets, type PresetGroup } from "../presets/preset-registry";
import { Input } from "../ui/input";
import { PresetGrid } from "./preset-grid";

// 统一记号弹层的图标搜索；预设只进搜索结果，不改图标词表。
export function IconGrid({
  value,
  onPick,
  presetGroups,
}: {
  value: string;
  onPick(key: string): void;
  presetGroups: readonly PresetGroup[];
}) {
  const { t } = useUiTranslation();
  const [query, setQuery] = React.useState("");
  const trimmed = query.trim();
  const searchLabel = t("identityMark.search");
  const groups = React.useMemo(
    () =>
      trimmed
        ? [
            {
              category: "search",
              label: t("projectSettings.identity.iconResults"),
              items: searchIcons(trimmed, t),
            },
          ]
        : iconsByCategory(t),
    [t, trimmed],
  );
  const presets = trimmed ? searchPresets(trimmed, t, presetGroups) : [];

  return (
    <div data-testid="project-glyph-icons">
      <div className="relative px-3 py-2">
        <Search
          className="pointer-events-none absolute left-5 top-1/2 size-3 -translate-y-1/2 text-muted-foreground"
          aria-hidden="true"
        />
        <Input
          data-testid="project-glyph-icon-search"
          aria-label={searchLabel}
          placeholder={searchLabel}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          className="h-8 pl-7 text-xs"
        />
      </div>
      <div className="max-h-[220px] overflow-y-auto px-3 pb-3">
        {presets.length > 0 && (
          <div className="pb-3">
            <PresetGrid
              items={presets}
              value={value}
              onPick={onPick}
              label={t("identityMark.preset")}
            />
          </div>
        )}
        {groups.map((g, i) => (
          <div key={g.category} className={cn(i > 0 && "mt-3")}>
            <p className="text-2xs text-muted-foreground">{g.label}</p>
            <div className="mt-1.5 grid grid-cols-8 gap-1">
              {g.items.map((meta) => {
                const Icon = iconForKey(meta.key);
                const active = meta.key === value;
                return (
                  <button
                    key={meta.key}
                    type="button"
                    title={meta.label}
                    aria-label={meta.label}
                    aria-pressed={active}
                    data-testid={`project-glyph-icon-${meta.key}`}
                    onClick={() => onPick(meta.key)}
                    className={cn(
                      "inline-flex size-7 items-center justify-center rounded-md text-muted-foreground outline-none hover:bg-accent hover:text-foreground focus-visible:focus-outline",
                      active && "bg-primary-soft text-primary-text",
                    )}
                  >
                    <Icon className="size-4" aria-hidden="true" />
                  </button>
                );
              })}
            </div>
          </div>
        ))}
        {presets.length === 0 && groups.every((g) => g.items.length === 0) && (
          <p
            data-testid="project-glyph-icon-none"
            className="py-2 text-2xs text-muted-foreground"
          >
            {t("identityMark.noMatches")}
          </p>
        )}
      </div>
    </div>
  );
}
