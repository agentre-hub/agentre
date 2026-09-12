import type { ReactNode } from "react";

import { Button } from "../ui/button";

export type Choice<T extends string> = {
  value: T;
  label: string;
};

/**
 * 引导里那几排「选一个」的按钮（安装方式 / 系统 / 运行方式）。
 *
 * 选中态由 `aria-pressed` 表达，不靠颜色——这排按钮决定下面给出哪条命令，选错了
 * 就是照着一条不适用的命令执行。刻意不用 ToggleGroup：它对 `type="single"` 渲染的
 * 是 radio 语义，而这里每一格就是一个独立的开关，宿主的用例也按 `aria-pressed` 断言。
 */
export function ChoiceGroup<T extends string>({
  label,
  hint,
  value,
  choices,
  onChange,
}: {
  label: ReactNode;
  hint?: ReactNode;
  value: T;
  choices: readonly Choice<T>[];
  onChange: (next: T) => void;
}) {
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <span className="text-sm font-medium">{label}</span>
        {hint ? (
          <span className="text-xs text-muted-foreground">{hint}</span>
        ) : null}
      </div>
      <div className="flex flex-wrap gap-2">
        {choices.map((choice) => (
          <Button
            key={choice.value}
            type="button"
            size="sm"
            variant={choice.value === value ? "default" : "outline"}
            aria-pressed={choice.value === value}
            onClick={() => onChange(choice.value)}
          >
            {choice.label}
          </Button>
        ))}
      </div>
    </div>
  );
}
