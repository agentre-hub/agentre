import * as React from "react";

import { cn } from "../lib/utils";

/**
 * 分段控件：替代下划线标签。语义仍是 tablist/tab（roving tabindex，方向键与
 * Home/End 移动并选中）；当前项是相对前景色 8% 的按钮填充，其余项透明、悬停出底，
 * 轨道本身不上底色，全程无边，焦点环走共享的 focus-outline。面板的 aria-controls
 * 由调用方经 item 透传。
 */
interface SegmentedControlContextValue {
  value: string;
  select: (value: string) => void;
}

const SegmentedControlContext =
  React.createContext<SegmentedControlContextValue | null>(null);

type SegmentedControlProps = Omit<
  React.ComponentProps<"div">,
  "role" | "onChange" | "value" | "defaultValue"
> & {
  value: string;
  onValueChange: (value: string) => void;
};

function SegmentedControl({
  value,
  onValueChange,
  className,
  onKeyDown,
  children,
  ...props
}: SegmentedControlProps) {
  const ctx = React.useMemo(
    () => ({ value, select: onValueChange }),
    [value, onValueChange],
  );

  const handleKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    onKeyDown?.(event);
    if (event.defaultPrevented) return;
    const tabs = Array.from(
      event.currentTarget.querySelectorAll<HTMLButtonElement>(
        '[role="tab"]:not(:disabled)',
      ),
    );
    if (tabs.length === 0) return;
    const current = tabs.indexOf(document.activeElement as HTMLButtonElement);
    let next: number;
    switch (event.key) {
      case "ArrowRight":
        next = current < 0 ? 0 : (current + 1) % tabs.length;
        break;
      case "ArrowLeft":
        next =
          current < 0
            ? tabs.length - 1
            : (current - 1 + tabs.length) % tabs.length;
        break;
      case "Home":
        next = 0;
        break;
      case "End":
        next = tabs.length - 1;
        break;
      default:
        return;
    }
    event.preventDefault();
    const target = tabs[next];
    target.focus();
    const nextValue = target.dataset.value;
    if (nextValue !== undefined) onValueChange(nextValue);
  };

  return (
    <SegmentedControlContext.Provider value={ctx}>
      <div
        role="tablist"
        data-slot="segmented-control"
        className={cn("inline-flex items-center gap-0.5", className)}
        onKeyDown={handleKeyDown}
        {...props}
      >
        {children}
      </div>
    </SegmentedControlContext.Provider>
  );
}

type SegmentedControlItemProps = Omit<
  React.ComponentProps<"button">,
  "role" | "value"
> & {
  value: string;
};

function SegmentedControlItem({
  value,
  className,
  onClick,
  ...props
}: SegmentedControlItemProps) {
  const ctx = React.useContext(SegmentedControlContext);
  if (!ctx) {
    throw new Error(
      "SegmentedControlItem must be used within SegmentedControl",
    );
  }
  const selected = ctx.value === value;
  return (
    <button
      type="button"
      role="tab"
      data-slot="segmented-control-item"
      data-state={selected ? "active" : "inactive"}
      data-value={value}
      aria-selected={selected}
      tabIndex={selected ? 0 : -1}
      className={cn(
        "inline-flex h-7 items-center justify-center gap-1.5 rounded-md px-3 text-sm font-medium whitespace-nowrap text-muted-foreground transition-colors outline-none hover:text-foreground focus-visible:focus-outline disabled:pointer-events-none disabled:opacity-50",
        "data-[state=inactive]:hover:bg-field-fill data-[state=active]:bg-button-fill data-[state=active]:text-foreground",
        className,
      )}
      onClick={(event) => {
        onClick?.(event);
        if (!event.defaultPrevented) ctx.select(value);
      }}
      {...props}
    />
  );
}

export { SegmentedControl, SegmentedControlItem };
export type { SegmentedControlProps, SegmentedControlItemProps };
