import { Brain, ChevronRight } from "lucide-react";
import { useTranslation } from "react-i18next";

import type { MemoryOwnerRef } from "@agentre-hub/agentre-ui";

import { useMemoryOwnerEntry } from "./use-memory-owner-entry";

/** 详情页里的「记忆 N 条」：点一下去记忆页并选中这个归属。读不到就不画。 */
export function OrgMemoryRow({ owner }: { owner: MemoryOwnerRef }) {
  const { t } = useTranslation();
  const entry = useMemoryOwnerEntry(owner);
  if (!entry) return null;
  return (
    <button
      type="button"
      data-slot="org-memory-row"
      onClick={entry.open}
      className="flex w-full items-center gap-2 rounded-md bg-button-fill px-3 py-2 text-left text-sm text-foreground hover:bg-accent on-accent focus-visible:focus-outline"
    >
      <Brain className="size-4 shrink-0 text-muted-foreground" aria-hidden />
      <span className="min-w-0 flex-1 truncate">
        {t("org.memory.row", { count: entry.count })}
      </span>
      <ChevronRight
        className="size-4 shrink-0 text-muted-foreground"
        aria-hidden
      />
    </button>
  );
}
