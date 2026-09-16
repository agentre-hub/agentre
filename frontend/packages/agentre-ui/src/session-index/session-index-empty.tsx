import { Inbox } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { Button } from "../ui/button";

import type { SessionFilterValue } from "./session-filter-chips";

type SessionIndexEmptyProps = {
  filter: SessionFilterValue;
  searching?: boolean;
  /** Account-wide count in the host's current query scope; omitted means unknown. */
  total?: number;
  onShowAll?: () => void;
  onClearSearch?: () => void;
  className?: string;
};

function SessionIndexEmpty({
  filter,
  searching = false,
  total,
  onShowAll,
  onClearSearch,
  className,
}: SessionIndexEmptyProps) {
  const { t } = useUiTranslation();
  const filtered = filter !== "all";
  const title = filtered
    ? t(
        filter === "running"
          ? "sessionIndex.empty.running"
          : "sessionIndex.empty.unread",
      )
    : searching
      ? t("sessionIndex.empty.search")
      : t("sessionIndex.empty.true");
  const description =
    filtered && total !== undefined && total > 0
      ? t("sessionIndex.empty.outside", { count: total })
      : null;
  const canShowAll = total === undefined || total > 0;
  const showAll = filtered && canShowAll ? onShowAll : undefined;
  const clearSearch = !filtered && searching && onClearSearch;

  return (
    <div
      data-slot="session-index-empty"
      className={cn(
        "flex flex-col items-center px-4 py-8 text-center",
        className,
      )}
    >
      <div
        role="status"
        aria-label={title}
        className="flex flex-col items-center"
      >
        <Inbox
          className="mb-2 size-5 text-decorative-foreground"
          aria-hidden="true"
        />
        <p className="text-sm font-semibold text-foreground">{title}</p>
        {description ? (
          <p className="mt-1 text-xs text-muted-foreground">{description}</p>
        ) : null}
      </div>
      {showAll ? (
        <Button
          type="button"
          variant="outline"
          size="xs"
          className="mt-3"
          onClick={onShowAll}
        >
          {t("sessionIndex.empty.showAll")}
        </Button>
      ) : null}
      {clearSearch ? (
        <Button
          type="button"
          variant="outline"
          size="xs"
          className="mt-3"
          onClick={onClearSearch}
        >
          {t("sessionIndex.empty.clearSearch")}
        </Button>
      ) : null}
    </div>
  );
}

export { SessionIndexEmpty };
export type { SessionIndexEmptyProps };
