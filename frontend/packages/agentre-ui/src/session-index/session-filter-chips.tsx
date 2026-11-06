import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";

type SessionFilterValue = "all" | "running" | "unread";

/** 筛选项。`label` 不给就用包内该值的文案；`count` 大于 0 才显示。 */
type SessionFilterItem<V extends string = SessionFilterValue> = {
  value: V;
  label?: string;
  count?: number;
};

type SessionFilterChipsProps<V extends string = SessionFilterValue> = {
  value: V;
  /** 不传 `items` 时「未读」胶囊上的计数；传了 `items` 以各项的 `count` 为准。 */
  unreadCount?: number;
  /** 自定义筛选项列表（含「需要你」）。不传就是今天的全部 / 运行中 / 未读三枚。 */
  items?: readonly SessionFilterItem<V>[];
  /** `touch`：44px 命中区，给手机用。 */
  size?: "default" | "touch";
  onChange: (value: V) => void;
  className?: string;
};

const FILTER_LABELS: Record<string, string> = {
  all: "sessionIndex.filter.all",
  attention: "sessionIndex.filter.attention",
  running: "sessionIndex.filter.running",
  unread: "sessionIndex.filter.unread",
};

const DEFAULT_VALUES = ["all", "running", "unread"] as const;

function SessionFilterChips<V extends string = SessionFilterValue>({
  value,
  unreadCount = 0,
  items,
  size = "default",
  onChange,
  className,
}: SessionFilterChipsProps<V>) {
  const { t } = useUiTranslation();
  const list: readonly SessionFilterItem<V>[] =
    items ??
    DEFAULT_VALUES.map((v) => ({
      value: v as unknown as V,
      count: v === "unread" ? unreadCount : 0,
    }));

  return (
    <div
      role="group"
      aria-label={t("sessionIndex.filter.aria")}
      className={cn("flex items-center gap-1.5", className)}
    >
      {list.map(({ value: filter, label, count }) => {
        const active = value === filter;
        const next = filter !== "all" && active ? ("all" as V) : filter;
        const text = label ?? t(FILTER_LABELS[filter] ?? filter);

        return (
          <button
            key={filter}
            type="button"
            data-testid={`filter-chip-${filter}`}
            aria-pressed={active}
            onClick={() => onChange(next)}
            className={cn(
              "inline-flex cursor-pointer items-center gap-1 rounded-full outline-none transition-colors focus-visible:focus-outline motion-reduce:transition-none",
              size === "touch"
                ? "min-h-11 px-3.5 text-sm"
                : "px-2.5 py-1 text-2xs",
              active
                ? "bg-primary-soft font-medium text-primary-text"
                : "bg-field-fill text-muted-foreground hover:bg-field-fill-hover hover:text-foreground",
            )}
          >
            {text}
            {count !== undefined && count > 0 ? (
              <span
                data-slot="unread-count"
                className="rounded-full bg-status-waiting-bg px-1 font-medium text-status-waiting-text"
              >
                {count}
              </span>
            ) : null}
          </button>
        );
      })}
    </div>
  );
}

export { SessionFilterChips };
export type { SessionFilterChipsProps, SessionFilterItem, SessionFilterValue };
