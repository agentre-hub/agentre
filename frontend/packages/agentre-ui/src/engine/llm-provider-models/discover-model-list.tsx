/**
 * 「发现模型」读到清单之后那一块：全选条 + 按模型族分组的可勾选列表。
 *
 * 库里已有的那几条**照样列出来、但勾不动**：藏起来会让人以为远端少给了；
 * 摆在原位并标上「已存在」，才对得上「远端有 N 条、其中 M 条是新的」那行计数。
 */
import { useUiTranslation as useTranslation } from "../../i18n";
import { Checkbox } from "../../ui/checkbox";

import { LlmModelLogo } from "../ai-brand-logo";
import { formatTokens } from "./index";
import type { VendorGroup } from "./discover-failure";

export interface DiscoverModelListProps {
  providerType: string;
  groups: VendorGroup[];
  existingIds: Set<string>;
  selected: Set<string>;
  newCount: number;
  allNewChecked: boolean;
  onToggle(id: string): void;
  onToggleAllNew(checked: boolean): void;
}

export function DiscoverModelList({
  providerType,
  groups,
  existingIds,
  selected,
  newCount,
  allNewChecked,
  onToggle,
  onToggleAllNew,
}: DiscoverModelListProps) {
  const { t } = useTranslation();

  return (
    <div className="space-y-2">
      <label className="flex items-center gap-2 rounded-md border border-border bg-secondary px-2.5 py-2">
        <Checkbox
          checked={allNewChecked}
          onCheckedChange={(next) => onToggleAllNew(next === true)}
          disabled={newCount === 0}
          aria-label={t("llmProviders.discover.selectAllNew")}
        />
        <span className="text-2xs font-semibold">
          {t("llmProviders.discover.selectAllNew")}
        </span>
        <span className="ml-auto text-2xs text-muted-foreground">
          {t("llmProviders.discover.selectAllHint")}
        </span>
      </label>
      {groups.map((group) => (
        <div key={group.key} className="space-y-1">
          <div className="flex items-center gap-1.5 pt-1 text-2xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
            {group.key !== "__unknown__" ? (
              <LlmModelLogo
                providerType={providerType}
                model={group.items[0]?.id ?? ""}
                className="size-3.5"
              />
            ) : null}
            <span>{group.label}</span>
          </div>
          {group.items.map((m) => {
            const existing = existingIds.has(m.id);
            const checked = selected.has(m.id);
            return (
              <div
                key={m.id}
                className="flex items-center gap-2.5 rounded-md border border-border px-2.5 py-2"
              >
                <Checkbox
                  checked={existing ? false : checked}
                  disabled={existing}
                  onCheckedChange={() => onToggle(m.id)}
                  aria-label={m.id}
                />
                <LlmModelLogo
                  providerType={providerType}
                  model={m.id}
                  className="size-4"
                />
                <span className="min-w-0 flex-1 truncate font-mono text-xs">
                  {m.id}
                </span>
                {m.contextWindow > 0 || m.maxOutput > 0 ? (
                  <span className="shrink-0 font-mono text-2xs text-muted-foreground">
                    {formatTokens(m.contextWindow)} ctx ·{" "}
                    {formatTokens(m.maxOutput)} out
                  </span>
                ) : null}
                <span
                  className={
                    existing
                      ? "shrink-0 rounded-sm bg-muted px-1.5 py-0.5 text-2xs text-muted-foreground"
                      : "shrink-0 rounded-sm bg-primary-soft px-1.5 py-0.5 text-2xs text-primary-text"
                  }
                >
                  {existing
                    ? t("llmProviders.discover.existingSkip")
                    : t("llmProviders.discover.new")}
                </span>
              </div>
            );
          })}
        </div>
      ))}
    </div>
  );
}
