import * as React from "react";
import { Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";

import { MemorySearch } from "../../../../wailsjs/go/app/App";
import type { memory_svc } from "../../../../wailsjs/go/models";

import {
  ownerKey,
  type MemoryDirectory,
  type MemoryDirectoryEntry,
} from "./directory";
import { OwnerMark } from "./owner-mark";

const SEARCH_DEBOUNCE_MS = 200;

type SearchState =
  | { status: "loading" }
  | { status: "error" }
  | { status: "ready"; items: memory_svc.MemoryItem[] };

/**
 * 搜索：按摘要和详情过滤所有层，结果按归属分组（组的先后按结果里第一次出现的顺序，
 * 即后端给的相关顺序）。点一条跳到它所在的归属。
 */
export function MemorySearchPanel({
  query,
  directory,
  refreshSignal,
  onOpenOwner,
}: {
  query: string;
  directory: MemoryDirectory;
  refreshSignal: number;
  onOpenOwner: (entry: MemoryDirectoryEntry) => void;
}) {
  const { t } = useTranslation();
  const [state, setState] = React.useState<SearchState>({ status: "loading" });
  const keyword = query.trim();

  React.useEffect(() => {
    let cancelled = false;
    const timer = window.setTimeout(() => {
      MemorySearch(keyword)
        .then((items) => {
          if (!cancelled) setState({ status: "ready", items: items ?? [] });
        })
        .catch(() => {
          if (!cancelled) setState({ status: "error" });
        });
    }, SEARCH_DEBOUNCE_MS);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [keyword, refreshSignal]);

  const groups = React.useMemo(() => {
    if (state.status !== "ready") return [];
    const byOwner = new Map<string, memory_svc.MemoryItem[]>();
    for (const item of state.items) {
      const key = ownerKey(item.owner);
      byOwner.set(key, [...(byOwner.get(key) ?? []), item]);
    }
    return [...byOwner.entries()];
  }, [state]);

  return (
    <section
      aria-label={t("memoryPage.search.results")}
      className="min-h-0 flex-1 overflow-y-auto px-4 py-5"
      data-selectable-text="true"
    >
      {state.status === "loading" ? (
        <div
          role="status"
          aria-label={t("memoryPage.loading")}
          className="flex justify-center pt-16 text-muted-foreground"
        >
          <Loader2 className="size-4 animate-spin motion-reduce:animate-none" />
        </div>
      ) : state.status === "error" ? (
        <p className="pt-16 text-center text-sm text-destructive-text">
          {t("memoryPage.loadFailed")}
        </p>
      ) : groups.length === 0 ? (
        <p className="pt-16 text-center text-xs text-muted-foreground">
          {t("memoryPage.search.noResults", { query: keyword })}
        </p>
      ) : (
        <div className="flex flex-col gap-5">
          {groups.map(([key, items]) => {
            const entry = directory.byKey.get(key);
            return (
              <div key={key} className="flex flex-col gap-1">
                <div className="flex items-center gap-2 px-2">
                  {entry ? <OwnerMark entry={entry} size="xs" /> : null}
                  <h3 className="text-xs font-semibold text-foreground">
                    {entry?.label ?? t("memoryPage.owner.unknown")}
                  </h3>
                </div>
                {items.map((item) => (
                  <button
                    key={item.id}
                    type="button"
                    disabled={!entry}
                    className="rounded-lg px-2 py-1.5 text-left text-sm text-foreground hover:bg-accent/60 hover:on-accent"
                    onClick={() => entry && onOpenOwner(entry)}
                  >
                    {item.summary}
                  </button>
                ))}
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}
