import * as React from "react";
import { ChevronDown } from "lucide-react";

import { cn } from "../lib/utils";
import { statusConfig, type AgentStatus } from "../transcript/agent-status";

/**
 * 会话索引里**一切组头**的外壳（规格 2026-08-22「组头归一」）。
 *
 * 四种组头（项目 / Agent / 机器 / 随手对话）此前各画各的，于是同一条设计在两端长出
 * 七八个样子——最刺眼的是那一格字形：桌面端的项目组头 24px、机器与随手对话也 24px，
 * agentre-server 的却一律 16px，Agent 那一档干脆连字形都没有、只剩一枚 8px 色点。
 * 行首那一槽早就归了 `RowLeadingSlot`，组头却没有对应的一件——这就是那一处的由来。
 *
 * 所以这里收的是**所有组头共有的那部分**：一枚会转的 chevron、一格定尺寸的字形、
 * 一行会截断的名字、一枚 attention 记号。剩下的都进插槽，因为它们是宿主的产品决定：
 *
 * - `badges` 在折叠按钮**里**：它们说的是这一组本身的事（离线 / 需升级 / 未配置），
 *   跟着名字走。
 * - `actions` 在折叠按钮**外**：`<button>` 不能嵌 `<button>`，而且点 ＋ / ⋮ / 重试
 *   不该顺手把这一组折起来。
 *
 * 字形交出去的是**尺寸类**而不是画好的节点（`glyph` 是渲染函数）：几档字形的圆角
 * 各不相同（24px 用 rounded-md、往下用 rounded-sm），包一层定尺寸的 span 会让子元素
 * 的圆角与底色对不上边。
 */

/**
 * 那一格字形的尺寸档。**只有这一条阶梯**——名字的字号跟着它一起降，所以切换深度时
 * 名字的起始 x 仍然对齐。
 */
export function groupGlyphClassName(depth: number): string {
  if (depth >= 2) return "size-3.5 rounded-sm";
  if (depth === 1) return "size-4 rounded-sm";
  return "size-6 rounded-md";
}

/** 名字这一档的字号 / 字重。深一层就退成 mono 小标题，让树的层级读得出来。 */
function groupLabelClassName(depth: number): string {
  if (depth >= 2) {
    return "font-mono text-[9px] font-medium uppercase tracking-widest text-muted-foreground";
  }
  if (depth === 1) {
    return "font-mono text-2xs font-semibold uppercase tracking-wider text-muted-foreground";
  }
  return "text-prose font-semibold";
}

export type IndexGroupHeaderProps = Omit<
  React.ComponentProps<"div">,
  "onToggle"
> & {
  /** 树深度（0 = 根组）。项目轴之外的轴恒为 0。 */
  depth?: number;
  expanded: boolean;
  onToggle: () => void;
  /** 画那一格字形；`className` 是这一档的尺寸与圆角，照着用。 */
  glyph?: (className: string) => React.ReactNode;
  label: React.ReactNode;
  /**
   * 名字退一档颜色（离线的机器 / 整棵树都没配路径的项目）。**只是颜色**：组头照样
   * 展得开——会话本体读得到，把组头做成打不开会让一台机器一离线历史就全够不着。
   */
  labelMuted?: boolean;
  /**
   * 需要关注的条数；0 时连圆点都不摆。**组内总条数不在这里**——条数就在下面列着，
   * 写出来是复述；需要关注的那几条则可能折叠着看不见，那才值得一个记号。
   */
  attentionCount?: number;
  /** 记号的档位（`error > waiting > running`）。`null` = 没有需要关注的会话。 */
  attentionTone?: AgentStatus | null;
  /** 名字之后、仍在折叠按钮内的角标。 */
  badges?: React.ReactNode;
  /** 折叠按钮之外的动作（＋ / ⋮ / 重试）。 */
  actions?: React.ReactNode;
  /** 折叠按钮的无障碍名。不给就靠名字本身。 */
  toggleLabel?: string;
  testId?: string;
  /** attention 记号的 testId；各组头沿用自己原来那个，测试才认得出是哪一种组头。 */
  attentionTestId?: string;
  attentionTitle?: string;
};

export function IndexGroupHeader({
  depth = 0,
  expanded,
  onToggle,
  glyph,
  label,
  labelMuted,
  attentionCount = 0,
  attentionTone = null,
  badges,
  actions,
  toggleLabel,
  testId,
  attentionTestId,
  attentionTitle,
  className,
  ...props
}: IndexGroupHeaderProps) {
  const isSub = depth > 0;

  return (
    <div
      data-testid={testId}
      className={cn(
        "group/group-header flex items-center gap-1.5 rounded-md text-xs hover:bg-sidebar-active-bg",
        isSub ? "px-1.5 py-1" : "px-2 py-1.5",
        className,
      )}
      {...props}
    >
      <button
        type="button"
        className="flex min-w-0 flex-1 cursor-pointer items-center gap-1.5 outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
        onClick={onToggle}
        aria-expanded={expanded}
        aria-label={toggleLabel}
      >
        <ChevronDown
          data-testid={testId ? `${testId}-chevron` : undefined}
          className={cn(
            "shrink-0 text-muted-foreground transition-transform duration-150 ease-out motion-reduce:transition-none",
            isSub ? "size-3" : "size-3.5",
            !expanded && "-rotate-90",
          )}
          aria-hidden="true"
        />
        {glyph?.(cn("shrink-0", groupGlyphClassName(depth)))}
        <span
          data-testid={testId ? `${testId}-label` : undefined}
          className={cn(
            "min-w-0 flex-1 truncate text-left",
            groupLabelClassName(depth),
            labelMuted && "text-muted-foreground",
          )}
        >
          {label}
        </span>
        {attentionCount > 0 && attentionTone ? (
          <span
            data-testid={
              attentionTestId ?? (testId ? `${testId}-attention` : undefined)
            }
            title={attentionTitle}
            className={cn(
              "inline-flex shrink-0 items-center gap-1 font-mono text-2xs",
              // 数字用状态的**文字**角色，点用填充角色——与会话行逐字同一套投影。
              statusConfig[attentionTone].textClassName,
            )}
          >
            <span
              data-slot="group-attention-dot"
              aria-hidden="true"
              className={cn(
                "inline-block size-1.5 rounded-full",
                statusConfig[attentionTone].dotClassName,
              )}
            />
            {attentionCount}
          </span>
        ) : null}
        {badges}
      </button>
      {actions}
    </div>
  );
}
