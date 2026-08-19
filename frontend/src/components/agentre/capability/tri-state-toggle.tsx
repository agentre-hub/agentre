import { cn } from "@/lib/utils";

import type { TriState } from "./catalog";

type Props = {
  value: TriState;
  labels: Record<TriState, string>; // 已 t() 解析
  onChange: (next: TriState) => void;
};

const order: TriState[] = ["inherit", "on", "off"];

const activeClass: Record<TriState, string> = {
  inherit: "bg-card text-foreground border border-border",
  on: "bg-primary text-primary-foreground",
  // 前景走 token 而不是硬编码 text-white(docs/design.md 约束 1);暗色压底同 critical。
  off: "bg-destructive text-destructive-foreground dark:bg-destructive/60",
};

export function TriStateToggle(props: Props) {
  return (
    <div className="inline-flex shrink-0 items-center gap-0.5 rounded-md bg-secondary p-0.5">
      {order.map((s) => {
        const active = props.value === s;
        return (
          <button
            key={s}
            type="button"
            aria-pressed={active}
            aria-label={props.labels[s]}
            onClick={() => props.onChange(s)}
            className={cn(
              "rounded-[5px] px-2.5 py-1 font-mono text-2xs transition-colors",
              active
                ? activeClass[s]
                : "text-muted-foreground hover:text-foreground",
              active && s !== "inherit" && "font-semibold",
            )}
          >
            {props.labels[s]}
          </button>
        );
      })}
    </div>
  );
}
