import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";

type SessionFilterValue = "all" | "running" | "unread";

type SessionFilterChipsProps = {
  value: SessionFilterValue;
  unreadCount: number;
  onChange: (value: SessionFilterValue) => void;
  className?: string;
};

const FILTERS = [
  { value: "all", label: "sessionIndex.filter.all" },
  { value: "running", label: "sessionIndex.filter.running" },
  { value: "unread", label: "sessionIndex.filter.unread" },
] as const satisfies readonly {
  value: SessionFilterValue;
  label: string;
}[];

function SessionFilterChips({
  value,
  unreadCount,
  onChange,
  className,
}: SessionFilterChipsProps) {
  const { t } = useUiTranslation();

  return (
    <div
      role="group"
      aria-label={t("sessionIndex.filter.aria")}
      className={cn("flex items-center gap-1.5", className)}
    >
      {FILTERS.map(({ value: filter, label }) => {
        const active = value === filter;
        const next = filter !== "all" && active ? "all" : filter;

        return (
          <button
            key={filter}
            type="button"
            data-testid={`filter-chip-${filter}`}
            aria-pressed={active}
            onClick={() => onChange(next)}
            className={cn(
              "inline-flex cursor-pointer items-center gap-1 rounded-full px-2.5 py-1 text-2xs outline-none transition-colors focus-visible:ring-[3px] focus-visible:ring-ring/50 motion-reduce:transition-none",
              active
                ? "bg-primary-soft font-medium text-primary-text"
                : "bg-sidebar-active-bg text-muted-foreground hover:text-foreground",
            )}
          >
            {t(label)}
            {filter === "unread" && unreadCount > 0 ? (
              <span
                data-slot="unread-count"
                className="rounded-full bg-status-waiting-bg px-1 font-medium text-status-waiting-text"
              >
                {unreadCount}
              </span>
            ) : null}
          </button>
        );
      })}
    </div>
  );
}

export { SessionFilterChips };
export type { SessionFilterChipsProps, SessionFilterValue };
