import { X } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";

import { toneClass } from "./tones";
import type { FilterChip } from "./query-conditions";
import type {
  DoneRetention,
  LabelUsageView,
  ScopeProjectNode,
  TimePreset,
} from "./query-types";

const TIME_PRESET_KEY: Record<TimePreset, string> = {
  any: "board.filter.time.any",
  today: "board.filter.time.today",
  "7d": "board.filter.time.d7",
  "30d": "board.filter.time.d30",
  custom: "board.filter.time.custom",
};

const RETENTION_KEY: Record<DoneRetention, string> = {
  "30d": "board.filter.retention.d30",
  "90d": "board.filter.retention.d90",
  all: "board.filter.retention.all",
};

export interface BoardFilterChipsProps {
  chips: FilterChip[];
  labels: LabelUsageView[];
  /** 范围 chip 要说出项目名字；不给就只说「项目」。 */
  projects?: ScopeProjectNode[];
  onRemove: (chip: FilterChip) => void;
  className?: string;
}

/**
 * 生效条件的 chip 行。
 *
 * 放不下时**它自己横滚**，不挤压搜索框、也不把看板往下推：条件多是常态，让整块板
 * 跟着条件行长高会把卡片挤出视野。
 */
export function BoardFilterChips({
  chips,
  labels,
  projects,
  onRemove,
  className,
}: BoardFilterChipsProps) {
  const { t } = useUiTranslation();

  function textOf(chip: FilterChip): { text: string; tone?: string } {
    switch (chip.kind) {
      case "keyword":
        return { text: t("board.chip.keyword", { keyword: chip.keyword }) };
      case "scope": {
        const name =
          chip.scope.kind === "unassigned"
            ? t("board.scope.unassigned")
            : (projects?.find(
                (project) =>
                  chip.scope.kind === "project" &&
                  project.id === chip.scope.projectId,
              )?.name ?? "");
        return { text: t("board.chip.scope", { name }) };
      }
      case "label": {
        const label = labels.find((item) => item.id === chip.labelId);
        return { text: label?.name ?? "", tone: label?.tone };
      }
      case "noLabel":
        return { text: t("board.filter.noLabel") };
      case "time":
        return {
          text: t(
            chip.field === "updated"
              ? "board.chip.updated"
              : "board.chip.created",
            { value: t(TIME_PRESET_KEY[chip.range.preset]) },
          ),
        };
      case "doneRetention":
        return {
          text: t("board.chip.done", {
            value: t(RETENTION_KEY[chip.retention]),
          }),
        };
    }
  }

  if (chips.length === 0) return null;

  return (
    <div
      data-testid="filter-chips"
      className={cn(
        "flex min-w-0 flex-1 items-center gap-1.5 overflow-x-auto",
        className,
      )}
    >
      {chips.map((chip) => {
        const { text, tone } = textOf(chip);
        return (
          <span
            key={chip.key}
            data-testid={`filter-chip-${chip.key}`}
            className={cn(
              "inline-flex shrink-0 items-center gap-1 rounded-full px-2 py-0.5 text-2xs",
              tone
                ? toneClass(tone)
                : "border border-border-strong text-muted-foreground",
            )}
          >
            {text}
            <button
              type="button"
              data-testid={`filter-chip-remove-${chip.key}`}
              aria-label={t("board.chip.remove", { name: text })}
              onClick={() => onRemove(chip)}
              className="cursor-pointer rounded-full opacity-70 transition-opacity hover:opacity-100 focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40"
            >
              <X className="size-3" aria-hidden="true" />
            </button>
          </span>
        );
      })}
    </div>
  );
}
