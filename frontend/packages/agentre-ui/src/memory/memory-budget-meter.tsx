import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";

export type MemoryBudgetMeterProps = {
  /** 全部正常条目的摘要字数，可以超过预算。 */
  used: number;
  budget: number;
  className?: string;
};

/**
 * 一层的预算用量：细条 + 「已用 / 预算 字」。超出时条与数字变提醒色
 * （`status-waiting`：需要留意，不是出错）。
 */
export function MemoryBudgetMeter({
  used,
  budget,
  className,
}: MemoryBudgetMeterProps) {
  const { t, i18n } = useUiTranslation();
  const over = used > budget;
  const format = new Intl.NumberFormat(i18n.language);
  const text = t("memory.budget.value", {
    used: format.format(used),
    budget: format.format(budget),
  });
  const ratio = budget > 0 ? Math.min(used / budget, 1) : 1;

  return (
    <div
      role="meter"
      aria-label={t("memory.budget.label")}
      aria-valuemin={0}
      aria-valuemax={budget}
      aria-valuenow={Math.min(used, budget)}
      aria-valuetext={text}
      data-over={over ? "true" : undefined}
      className={cn("flex items-center gap-2", className)}
    >
      <span className="h-1 w-16 overflow-hidden rounded-full bg-field-fill">
        <span
          data-meter-fill
          className={cn(
            "block h-full rounded-full",
            over ? "bg-status-waiting" : "bg-primary",
          )}
          style={{ width: `${ratio * 100}%` }}
        />
      </span>
      <span
        className={cn(
          "font-mono text-2xs",
          over ? "text-status-waiting-text" : "text-muted-foreground",
        )}
      >
        {text}
      </span>
    </div>
  );
}
