// 供应商面板的左栏：搜索框 + 按 Provider 类型分组的清单。
// 分组次序按 providerTypeOrder，目录里出现的未知类型排在其后。
import * as React from "react";
import { useUiTranslation as useTranslation } from "../../i18n";

import { SearchInput } from "../../ui/search-input";
import { cn } from "../../lib/utils";
import { LlmProviderLogo } from "../ai-brand-logo";

import {
  type Provider,
  endpointFor,
  isProviderType,
  providerTypeOrder,
} from ".";

export function ProviderNav({
  providers,
  selectedId,
  onSelect,
}: {
  providers: Provider[];
  selectedId: number | null;
  onSelect: (id: number) => void;
}) {
  const { t } = useTranslation();
  const [navSearch, setNavSearch] = React.useState("");

  const trimmed = navSearch.trim().toLowerCase();
  const filtered = trimmed
    ? providers.filter(
        (p) =>
          p.name.toLowerCase().includes(trimmed) ||
          endpointFor(p).toLowerCase().includes(trimmed) ||
          p.providerKey.toLowerCase().includes(trimmed),
      )
    : providers;

  const groups = React.useMemo(() => {
    const map = new Map<string, Provider[]>();
    for (const p of filtered) {
      const list = map.get(p.type) ?? [];
      list.push(p);
      map.set(p.type, list);
    }
    const extra = Array.from(map.keys()).filter(
      (k) =>
        !providerTypeOrder.includes(k as (typeof providerTypeOrder)[number]),
    );
    const order = [...providerTypeOrder, ...extra];
    return order
      .map((type) => ({ type, items: map.get(type) ?? [] }))
      .filter((g) => g.items.length > 0);
  }, [filtered]);
  return (
    <aside
      role="complementary"
      aria-label={t("llmProviders.nav.ariaLabel")}
      className="flex w-60 shrink-0 flex-col border-r border-border"
    >
      <div className="border-b border-border p-2">
        <SearchInput
          value={navSearch}
          onChange={setNavSearch}
          placeholder={t("llmProviders.nav.searchPlaceholder")}
          aria-label={t("llmProviders.nav.searchAria")}
        />
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-2">
        {groups.map((group) => (
          <div key={group.type} className="mb-2">
            <div className="px-1.5 pb-1 font-mono text-2xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
              {isProviderType(group.type)
                ? t(`llmProviders.providerType.${group.type}.label`)
                : group.type}
              <span className="ml-1 text-muted-foreground">
                {group.items.length}
              </span>
            </div>
            <div className="flex flex-col gap-0.5">
              {group.items.map((p) => {
                const active = p.id === selectedId;
                return (
                  <button
                    key={p.id}
                    type="button"
                    aria-current={active}
                    onClick={() => onSelect(p.id)}
                    className={cn(
                      "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
                      active
                        ? "bg-primary-soft text-primary-text"
                        : "hover:bg-accent",
                    )}
                  >
                    <LlmProviderLogo
                      providerType={p.type}
                      providerName={p.name}
                      baseUrl={p.baseUrl}
                      className="size-5 shrink-0 rounded-sm"
                    />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-xs font-medium">
                        {p.name}
                      </span>
                      {/* endpoint 独占一行：截断只吃 endpoint 自己 */}
                      <span className="block truncate font-mono text-2xs text-muted-foreground">
                        {endpointFor(p)}
                      </span>
                    </span>
                    {/* 模型数是独立元素，不参与 endpoint 的截断；停用时让位给下面的徽标 */}
                    {p.enabled ? (
                      <span className="shrink-0 font-mono text-2xs text-muted-foreground">
                        {t("llmProviders.nav.modelCount", {
                          count: p.modelCount,
                        })}
                      </span>
                    ) : (
                      <span className="shrink-0 font-mono text-2xs text-status-waiting">
                        {t("llmProviders.nav.disabled")}
                      </span>
                    )}
                  </button>
                );
              })}
            </div>
          </div>
        ))}
        {filtered.length === 0 ? (
          <p className="px-1.5 py-2 text-2xs text-muted-foreground">
            {t("llmProviders.nav.noMatch")}
          </p>
        ) : null}
      </div>
    </aside>
  );
}
