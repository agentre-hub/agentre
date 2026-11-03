import { Check } from "lucide-react";

import { agentColorOrder, tokenToCssColor } from "../lib/agent-color";
import { cn } from "../lib/utils";

// 颜色 token 是持久化值；旧项目入口保留测试标记，新弹层复用同一套选中态。
export function ColorPalette({
  color,
  onPickColor,
  testIdPrefix,
}: {
  color?: string;
  onPickColor(color: string): void;
  testIdPrefix?: string;
}) {
  return (
    <div
      data-testid={testIdPrefix && `${testIdPrefix}-colors`}
      className="mt-2 grid grid-cols-8 gap-1.5"
    >
      {agentColorOrder.map((c) => (
        <button
          key={c}
          type="button"
          aria-label={c}
          aria-pressed={color === c}
          data-testid={testIdPrefix && `${testIdPrefix}-color-${c}`}
          onClick={() => onPickColor(c)}
          style={{ backgroundColor: tokenToCssColor(c) ?? undefined }}
          className={cn(
            "inline-flex size-6 items-center justify-center rounded-full text-agent-foreground focus-visible:focus-outline",
            color === c &&
              "outline outline-2 outline-offset-2 outline-foreground",
          )}
        >
          {color === c ? (
            <Check className="size-3.5" aria-hidden="true" />
          ) : null}
        </button>
      ))}
    </div>
  );
}
