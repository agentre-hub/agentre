import * as React from "react";
import { ChevronDown, Loader2, X } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { Button } from "../ui/button";
import { PopoverContent } from "../ui/popover";

import { SessionRowSkeleton } from "./session-row-skeleton";

const NEAR_BOTTOM_PX = 96;

type SessionGroupOverflowPage<Row> = {
  rows: Row[];
  total: number;
  nextCursor: string | null;
};

type SessionGroupOverflowProps<Row> = {
  title: string;
  avatar?: React.ReactNode;
  loadPage: (cursor: string | null) => Promise<SessionGroupOverflowPage<Row>>;
  getRowKey: (row: Row) => React.Key;
  renderRow: (row: Row, close: () => void) => React.ReactNode;
  onClose: () => void;
  side?: React.ComponentProps<typeof PopoverContent>["side"];
  align?: React.ComponentProps<typeof PopoverContent>["align"];
  sideOffset?: number;
  collisionPadding?: React.ComponentProps<
    typeof PopoverContent
  >["collisionPadding"];
  className?: string;
};

type RequestToken = {
  cycle: number;
};

function SessionGroupOverflow<Row>({
  title,
  avatar,
  loadPage,
  getRowKey,
  renderRow,
  onClose,
  side = "right",
  align = "start",
  sideOffset = 8,
  collisionPadding = 12,
  className,
}: SessionGroupOverflowProps<Row>) {
  const { t } = useUiTranslation();
  const [rows, setRows] = React.useState<Row[]>([]);
  const [total, setTotal] = React.useState(0);
  const [nextCursor, setNextCursor] = React.useState<string | null>(null);
  const [failedCursor, setFailedCursor] = React.useState<string | null>(null);
  const [failed, setFailed] = React.useState(false);
  const [loading, setLoading] = React.useState(true);
  const cycleRef = React.useRef(0);
  const inFlightRef = React.useRef<RequestToken | null>(null);
  const listRef = React.useRef<HTMLDivElement>(null);

  const requestPage = React.useCallback(
    async (cursor: string | null, replace: boolean, cycle: number) => {
      if (cycle !== cycleRef.current || inFlightRef.current) return;

      const token = { cycle };
      inFlightRef.current = token;
      setLoading(true);
      setFailed(false);

      try {
        const page = await loadPage(cursor);
        if (cycle !== cycleRef.current) return;

        setRows((current) =>
          replace ? page.rows : [...current, ...page.rows],
        );
        setTotal(page.total);
        setNextCursor(page.nextCursor);
        setFailedCursor(null);
      } catch {
        if (cycle !== cycleRef.current) return;
        setFailedCursor(cursor);
        setFailed(true);
      } finally {
        if (cycle === cycleRef.current && inFlightRef.current === token) {
          inFlightRef.current = null;
          setLoading(false);
        }
      }
    },
    [loadPage],
  );

  React.useEffect(() => {
    const cycle = cycleRef.current + 1;
    cycleRef.current = cycle;
    inFlightRef.current = null;
    setRows([]);
    setTotal(0);
    setNextCursor(null);
    setFailedCursor(null);
    setFailed(false);
    setLoading(true);
    void requestPage(null, true, cycle);

    return () => {
      if (cycleRef.current === cycle) cycleRef.current += 1;
      inFlightRef.current = null;
    };
  }, [requestPage]);

  const loadNext = React.useCallback(() => {
    if (nextCursor === null) return;
    void requestPage(nextCursor, false, cycleRef.current);
  }, [nextCursor, requestPage]);

  const retry = React.useCallback(() => {
    void requestPage(failedCursor, rows.length === 0, cycleRef.current);
  }, [failedCursor, requestPage, rows.length]);

  const handleScroll = React.useCallback(() => {
    const list = listRef.current;
    if (!list || nextCursor === null) return;
    if (list.scrollHeight - list.scrollTop - list.clientHeight > NEAR_BOTTOM_PX)
      return;
    loadNext();
  }, [loadNext, nextCursor]);

  return (
    <PopoverContent
      side={side}
      align={align}
      sideOffset={sideOffset}
      collisionPadding={collisionPadding}
      className={cn(
        "flex h-[480px] max-h-[min(480px,var(--radix-popover-content-available-height))] w-[360px] max-w-[calc(100vw-1.5rem)] flex-col gap-0 p-0",
        className,
      )}
      aria-label={title}
      onEscapeKeyDown={onClose}
      onInteractOutside={onClose}
    >
      <header className="flex shrink-0 items-center gap-2 border-b border-border px-3 py-2.5">
        {avatar}
        <div className="min-w-0 flex-1">
          <div className="truncate text-xs font-semibold">{title}</div>
          <div className="mt-0.5 font-mono text-2xs text-muted-foreground">
            {t("sessionGroup.overflow.total", { count: total })}
          </div>
        </div>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={t("common.close")}
          onClick={onClose}
        >
          <X data-icon="only" aria-hidden="true" />
        </Button>
      </header>

      <div
        ref={listRef}
        data-testid="session-group-overflow-list"
        aria-busy={loading ? "true" : undefined}
        onScroll={handleScroll}
        className="min-h-0 flex-1 overflow-auto px-1.5 py-1.5"
      >
        {loading && rows.length === 0 && !failed ? (
          <SessionRowSkeleton rows={3} />
        ) : null}
        {rows.map((row) => (
          <React.Fragment key={getRowKey(row)}>
            {renderRow(row, onClose)}
          </React.Fragment>
        ))}
        {!loading && !failed && rows.length === 0 ? (
          <div className="px-3 py-6 text-center text-2xs text-muted-foreground">
            {t("sessionGroup.overflow.empty")}
          </div>
        ) : null}
        {failed ? (
          <div
            role="alert"
            className="flex flex-col items-center gap-2 px-3 py-4 text-center text-2xs text-status-error"
          >
            <span>{t("sessionGroup.overflow.loadFailed")}</span>
            <Button type="button" variant="outline" size="sm" onClick={retry}>
              {t("sessionGroup.overflow.retry")}
            </Button>
          </div>
        ) : null}
      </div>

      <footer className="flex shrink-0 items-center justify-center gap-2 border-t border-border bg-muted/40 px-3 py-1.5">
        <span className="font-mono text-2xs text-muted-foreground">
          {t("sessionGroup.overflow.loaded", {
            loaded: rows.length,
            total,
          })}
        </span>
        {nextCursor !== null && !failed ? (
          <>
            <span className="font-mono text-2xs text-border-strong">·</span>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-6 px-2 text-2xs"
              disabled={loading}
              onClick={loadNext}
            >
              {loading ? (
                <Loader2 className="size-3 animate-spin" aria-hidden="true" />
              ) : (
                <ChevronDown className="size-3" aria-hidden="true" />
              )}
              {t("sessionGroup.overflow.loadMore")}
            </Button>
          </>
        ) : null}
      </footer>
    </PopoverContent>
  );
}

export { SessionGroupOverflow };
export type { SessionGroupOverflowPage, SessionGroupOverflowProps };
