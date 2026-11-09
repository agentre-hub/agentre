import { Tags } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { Input } from "../ui/input";

import { toneClass } from "./tones";
import type {
  BoardQuery,
  DoneRetention,
  LabelUsageView,
  TimePreset,
  TimeRange,
} from "./query-types";

const TIME_PRESETS: { preset: TimePreset; labelKey: string }[] = [
  { preset: "any", labelKey: "board.filter.time.any" },
  { preset: "today", labelKey: "board.filter.time.today" },
  { preset: "7d", labelKey: "board.filter.time.d7" },
  { preset: "30d", labelKey: "board.filter.time.d30" },
  { preset: "custom", labelKey: "board.filter.time.custom" },
];

const RETENTIONS: { value: DoneRetention; labelKey: string }[] = [
  { value: "30d", labelKey: "board.filter.retention.d30" },
  { value: "90d", labelKey: "board.filter.retention.d90" },
  { value: "all", labelKey: "board.filter.retention.all" },
];

/** 面板里那种小方按钮：选中就是实心，没有第二种形状。 */
function OptionButton({
  active,
  testId,
  onClick,
  children,
  sheet,
}: {
  active: boolean;
  testId: string;
  onClick: () => void;
  children: React.ReactNode;
  sheet?: boolean;
}) {
  return (
    <button
      type="button"
      data-testid={testId}
      data-active={active ? "true" : undefined}
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        "cursor-pointer rounded-md px-2 py-1 text-2xs transition-colors",
        sheet && "inline-flex min-h-11 items-center rounded-full px-4 text-sm",
        "focus-visible:focus-outline",
        active
          ? "bg-primary-soft text-primary-text"
          : "text-muted-foreground hover:bg-accent/60 hover:on-accent",
      )}
    >
      {children}
    </button>
  );
}

function Section({
  title,
  children,
  sheet,
}: {
  title: string;
  children: React.ReactNode;
  sheet?: boolean;
}) {
  return (
    <section
      className={cn(
        "flex flex-col gap-1.5 px-3 py-2.5",
        sheet && "gap-2 px-4 py-3",
      )}
    >
      <h3
        className={cn(
          "text-2xs font-semibold text-muted-foreground",
          sheet && "text-xs",
        )}
      >
        {title}
      </h3>
      {children}
    </section>
  );
}

/** 一段时间条件：五档 + 自定义时那两个日期端点。 */
function TimeSection({
  title,
  field,
  range,
  onChange,
  sheet,
}: {
  title: string;
  field: "updated" | "created";
  range: TimeRange;
  onChange: (next: TimeRange) => void;
  sheet?: boolean;
}) {
  const { t } = useUiTranslation();

  return (
    <Section title={title} sheet={sheet}>
      <div
        className={cn("flex flex-wrap items-center gap-1", sheet && "gap-2")}
      >
        {TIME_PRESETS.map(({ preset, labelKey }) => (
          <OptionButton
            key={preset}
            active={range.preset === preset}
            testId={`filter-${field}-${preset}`}
            sheet={sheet}
            onClick={() => onChange({ preset })}
          >
            {t(labelKey)}
          </OptionButton>
        ))}
      </div>
      {range.preset === "custom" ? (
        <div className="flex items-center gap-1.5">
          <Input
            type="date"
            data-testid={`filter-${field}-from`}
            aria-label={t("board.filter.from")}
            value={toDateInput(range.from)}
            onChange={(event) =>
              onChange({ ...range, from: fromDateInput(event.target.value) })
            }
            className={sheet ? "h-11 text-base" : "h-7 text-2xs"}
          />
          <span className="text-2xs text-muted-foreground">
            {t("board.filter.to")}
          </span>
          <Input
            type="date"
            data-testid={`filter-${field}-to`}
            aria-label={t("board.filter.to")}
            value={toDateInput(range.to)}
            onChange={(event) =>
              onChange({
                ...range,
                to: endOfDay(fromDateInput(event.target.value)),
              })
            }
            className={sheet ? "h-11 text-base" : "h-7 text-2xs"}
          />
        </div>
      ) : null}
    </Section>
  );
}

/** 毫秒 epoch ↔ `<input type="date">` 的 `YYYY-MM-DD`（本地日历日）。 */
function toDateInput(at?: number): string {
  if (!at) return "";
  const date = new Date(at);
  const month = `${date.getMonth() + 1}`.padStart(2, "0");
  const day = `${date.getDate()}`.padStart(2, "0");
  return `${date.getFullYear()}-${month}-${day}`;
}

function fromDateInput(value: string): number | undefined {
  if (!value) return undefined;
  const at = new Date(`${value}T00:00:00`).getTime();
  return Number.isNaN(at) ? undefined : at;
}

/**
 * 区间的上界要停在那一天的**末尾**。
 *
 * 日期输入给出的是本地零点，而两端的比较都是闭区间（`updatetime <= to`）——照零点
 * 发出去的话「8 月 1 日到 8 月 27 日」会把 27 日当天改过的每一张卡都排除在外，
 * 也就是最常选的那一天（今天）一张都看不见。
 */
function endOfDay(at?: number): number | undefined {
  if (at === undefined) return undefined;
  return at + 24 * 60 * 60 * 1000 - 1;
}

export interface BoardFilterPanelProps {
  query: BoardQuery;
  labels: LabelUsageView[];
  patch: (partial: Partial<BoardQuery>) => void;
  onManageLabels?: () => void;
  /** 弹层档：选项成 44px 的 chip、日期输入 16px；不传时与桌面一致。 */
  sheet?: boolean;
}

/**
 * 筛选面板：标签、更新时间、创建时间、已完成保留多久。
 *
 * 「项目」与「关键词」是另外两条条件，各有自己的触发器（标题栏的范围选择器、
 * 搜索框），所以不在这块面板里 —— 但它们同样计进「筛选」按钮上的那个数字。
 */
export function BoardFilterPanel({
  query,
  labels,
  patch,
  onManageLabels,
  sheet,
}: BoardFilterPanelProps) {
  const { t } = useUiTranslation();

  return (
    <div
      className={cn(
        "flex flex-col overflow-y-auto",
        sheet ? "max-h-[60vh]" : "max-h-[28rem]",
      )}
    >
      <Section title={t("board.filter.labels")} sheet={sheet}>
        <div
          className={cn("flex flex-wrap items-center gap-1", sheet && "gap-2")}
        >
          {labels.map((label) => {
            const active = query.labelIds.includes(label.id);
            return (
              <button
                key={label.id}
                type="button"
                data-testid={`filter-label-${label.id}`}
                aria-pressed={active}
                onClick={() =>
                  patch({
                    labelIds: active
                      ? query.labelIds.filter((id) => id !== label.id)
                      : [...query.labelIds, label.id],
                    // 「只看没有标签的」与选标签是互斥的两件事。
                    noLabelOnly: false,
                  })
                }
                className={cn(
                  "cursor-pointer rounded-full px-2 py-0.5 text-2xs transition-opacity",
                  sheet && "inline-flex min-h-11 items-center px-4 text-sm",
                  "focus-visible:focus-outline",
                  toneClass(label.tone),
                  active
                    ? "ring-1 ring-primary"
                    : "opacity-70 hover:opacity-100",
                )}
              >
                {label.name}
              </button>
            );
          })}
        </div>
        <div className={cn("flex items-center gap-1", sheet && "gap-2")}>
          <OptionButton
            sheet={sheet}
            active={query.labelMatch === "any"}
            testId="filter-match-any"
            onClick={() => patch({ labelMatch: "any" })}
          >
            {t("board.filter.matchAny")}
          </OptionButton>
          <OptionButton
            sheet={sheet}
            active={query.labelMatch === "all"}
            testId="filter-match-all"
            onClick={() => patch({ labelMatch: "all" })}
          >
            {t("board.filter.matchAll")}
          </OptionButton>
          <OptionButton
            sheet={sheet}
            active={query.noLabelOnly}
            testId="filter-no-label"
            onClick={() =>
              patch({ noLabelOnly: !query.noLabelOnly, labelIds: [] })
            }
          >
            {t("board.filter.noLabel")}
          </OptionButton>
        </div>
      </Section>
      <TimeSection
        title={t("board.filter.updated")}
        field="updated"
        range={query.updated}
        sheet={sheet}
        onChange={(updated) => patch({ updated })}
      />
      <TimeSection
        title={t("board.filter.created")}
        field="created"
        range={query.created}
        sheet={sheet}
        onChange={(created) => patch({ created })}
      />
      <Section title={t("board.filter.done")} sheet={sheet}>
        <div
          className={cn("flex flex-wrap items-center gap-1", sheet && "gap-2")}
        >
          {RETENTIONS.map(({ value, labelKey }) => (
            <OptionButton
              sheet={sheet}
              key={value}
              active={query.doneRetention === value}
              testId={`filter-done-${value}`}
              onClick={() => patch({ doneRetention: value })}
            >
              {t(labelKey)}
            </OptionButton>
          ))}
        </div>
      </Section>
      {onManageLabels ? (
        <div className="p-1">
          <button
            type="button"
            data-testid="filter-manage-labels"
            onClick={onManageLabels}
            className={cn(
              "flex w-full cursor-pointer items-center gap-1.5 rounded-md px-2 py-1.5 text-2xs text-muted-foreground transition-colors hover:bg-accent/60 hover:on-accent hover:text-foreground focus-visible:focus-outline",
              sheet && "min-h-11 px-3 text-sm",
            )}
          >
            <Tags className="size-3.5" aria-hidden="true" />
            {t("board.filter.manageLabels")}
          </button>
        </div>
      ) : null}
    </div>
  );
}
