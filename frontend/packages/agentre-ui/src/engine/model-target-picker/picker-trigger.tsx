/**
 * ModelTargetPicker 的触发按钮（mockup `.trigger`）。
 *
 * 主行既可能是纯文本（既有消费方），也可能是一排节点（后端编辑器的「品牌标识 +
 * 供应商名 + 跟随/固定徽标」）。两者的无障碍名算法必须不同：节点里的 `role="img"`
 * 品牌标识自带名字，让内容参与名字计算会念出重复且不稳定的一串——所以节点形态下
 * 名字只认显式字符串（`aria-label` 优先，其次目录解析出的标签）。
 */
import * as React from "react";
import { AlertTriangle, ChevronDown } from "lucide-react";

import { cn } from "../../lib/utils";

export interface PickerTriggerProps {
  open: boolean;
  disabled: boolean;
  invalid: boolean;
  compact: boolean;
  className?: string;
  title?: string;
  testId?: string;
  ariaLabel?: string;
  /** 目录解析出的标签：节点形态下当作无障碍名的兜底。 */
  selectedLabel: string;
  triggerText: React.ReactNode;
  triggerSub?: React.ReactNode;
}

export const PickerTrigger = React.forwardRef<
  HTMLButtonElement,
  PickerTriggerProps & React.ComponentPropsWithoutRef<"button">
>(function PickerTrigger(
  {
    open,
    disabled,
    invalid,
    compact,
    className,
    title,
    testId,
    ariaLabel,
    selectedLabel,
    triggerText,
    triggerSub,
    ...rest
  },
  ref,
) {
  const isTextTriggerLabel =
    typeof triggerText === "string" || typeof triggerText === "number";
  const triggerAriaLabel =
    ariaLabel ?? (isTextTriggerLabel ? undefined : selectedLabel);

  return (
    <button
      {...rest}
      ref={ref}
      type="button"
      disabled={disabled}
      data-testid={testId}
      title={title}
      aria-label={triggerAriaLabel}
      aria-expanded={open}
      aria-haspopup="listbox"
      className={cn(
        // mockup .trigger：input 描边 + input-bg 底 + 8px 圆角 + 手形光标。
        "inline-flex w-full cursor-pointer items-center justify-between gap-2 rounded-lg border border-input bg-input-bg text-xs transition-colors",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
        "disabled:cursor-not-allowed disabled:opacity-60",
        triggerSub
          ? compact
            ? "min-h-9 px-2 py-1"
            : "min-h-11 px-2.5 py-1.5"
          : compact
            ? "h-7 px-2"
            : "h-9 px-2.5",
        invalid
          ? "border-status-waiting bg-status-waiting-bg"
          : "hover:bg-secondary/60",
        className,
      )}
    >
      <span className="flex min-w-0 items-center gap-1.5">
        {invalid ? (
          <AlertTriangle
            className={cn("size-3.5 shrink-0 text-status-waiting")}
            aria-hidden="true"
          />
        ) : null}
        <span className="flex min-w-0 flex-col items-start gap-0.5">
          <span
            className={cn(
              "max-w-full",
              // 节点主行自己是一排图标 + 文字 + 徽标，得撑成 flex 行；纯文本主行
              // 保持既有的单行截断。
              isTextTriggerLabel
                ? "truncate"
                : "flex min-w-0 items-center gap-1.5",
              invalid ? "text-status-waiting" : "text-foreground",
            )}
          >
            {triggerText}
          </span>
          {triggerSub ? (
            <span
              data-testid="model-target-trigger-sub"
              className="max-w-full truncate text-2xs text-muted-foreground"
            >
              {triggerSub}
            </span>
          ) : null}
        </span>
      </span>
      <ChevronDown
        className="size-3.5 shrink-0 text-muted-foreground"
        aria-hidden="true"
      />
    </button>
  );
});
