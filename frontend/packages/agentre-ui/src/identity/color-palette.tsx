import { Check } from "lucide-react";

import { agentColorOrder, tokenToCssColor } from "../lib/agent-color";
import { cn } from "../lib/utils";

// 颜色 token 是持久化值；所有身份选择器复用同一套选中态。
export function ColorPalette({
  color,
  onPickColor,
}: {
  color?: string;
  onPickColor(color: string): void;
}) {
  return (
    <div className="mt-2 grid grid-cols-8 gap-1.5">
      {agentColorOrder.map((c) => (
        <button
          key={c}
          type="button"
          aria-label={c}
          aria-pressed={color === c}
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
