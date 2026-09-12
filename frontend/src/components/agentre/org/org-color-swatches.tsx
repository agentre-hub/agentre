import { cn } from "@/lib/utils";

import {
  agentColorClassNames,
  agentColorOrder,
  type AgentColor,
} from "../types";

/**
 * 调色板那一格：Agent 详情的头像色与部门详情的主题色是同一个控件（同一份
 * `agentColorOrder`、同一个 radiogroup 结构），只有圆点尺寸与选中态两端不同，
 * 所以按 className 参数化，文案 key 留在调用点仍是静态字面量。
 */
export function OrgColorSwatches({
  value,
  groupLabel,
  optionLabel,
  onPick,
  swatchClassName,
  selectedClassName,
}: {
  value: AgentColor;
  groupLabel: string;
  optionLabel: (color: AgentColor) => string;
  onPick: (color: AgentColor) => void;
  swatchClassName: string;
  selectedClassName: string;
}) {
  return (
    <div
      className="grid grid-cols-5 gap-2"
      role="radiogroup"
      aria-label={groupLabel}
    >
      {agentColorOrder.map((c) => (
        <button
          key={c}
          type="button"
          role="radio"
          aria-checked={value === c}
          aria-label={optionLabel(c)}
          onClick={() => onPick(c)}
          className={cn(
            swatchClassName,
            "rounded-full ring-offset-2 transition-all",
            agentColorClassNames[c],
            value === c && selectedClassName,
          )}
        />
      ))}
    </div>
  );
}
