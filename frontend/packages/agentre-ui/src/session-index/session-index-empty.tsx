import { Inbox } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { Button } from "../ui/button";
import { useTouch } from "../ui/touch";

import type { SessionFilterValue } from "./session-filter-chips";

type SessionIndexEmptyProps = {
  filter: SessionFilterValue | "attention";
  searching?: boolean;
  /** Account-wide count in the host's current query scope; omitted means unknown. */
  total?: number;
  onShowAll?: () => void;
  onClearSearch?: () => void;
  /** 触屏档：两枚恢复按钮点按区 44px。不传时跟 TouchProvider，再不然与桌面一致。 */
  touch?: boolean;
  className?: string;
};

function SessionIndexEmpty({
  filter,
  searching = false,
  total,
  onShowAll,
  onClearSearch,
  touch: touchProp,
  className,
}: SessionIndexEmptyProps) {
  const { t } = useUiTranslation();
  const touch = useTouch(touchProp);
  const actionSize = touch ? "min-h-11 px-4 text-sm" : undefined;
  const filtered = filter !== "all";
  const title = filtered
    ? t(
        filter === "running"
          ? "sessionIndex.empty.running"
          : filter === "attention"
            ? "sessionIndex.empty.attention"
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
          className={cn("mt-3", actionSize)}
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
          className={cn("mt-3", actionSize)}
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
