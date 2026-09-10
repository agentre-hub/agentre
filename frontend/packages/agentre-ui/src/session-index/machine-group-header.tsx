import type * as React from "react";

import { cn } from "../lib/utils";
import type { AgentStatus } from "../transcript/agent-status";

import { IndexGroupHeader } from "./group-header";

/**
 * 「按机器」轴的组头（规格 2026-08-21，2026-08-22 迁入共享包）。
 *
 * 实现由桌面端的 `session-index/machine-group-header.tsx` 迁入。与别的组头共用
 * `IndexGroupHeader` 的外壳，只把那一格字形换成机器的在线状态：这一维的身份是
 * 「哪台机器」，不是一个有颜色的方块。尺寸仍走同一条阶梯，几种组头的名字起始 x
 * 因此对齐。
 *
 * **离线只置灰，不禁用**：会话本体在库里，机器离线影响的是能不能在那台机器上继续
 * 跑，不影响读。把组头做成不可展开会让一台机器一离线，它上面的历史就全部够不着。
 *
 * 与「随手对话」组头一样**没有 ⋮** —— 一台机器上没有设置 / 合并 / 删除可言。
 * agentre-server 那边的「连不上 / 重试 / 需升级 / 条数」是它自己的产品决定，从
 * `badges` 与 `actions` 递进来。
 */
export type MachineGroupHeaderProps = {
  machine: { name: string; online: boolean };
  expanded: boolean;
  onToggle: () => void;
  attentionCount: number;
  attentionTone: AgentStatus | null;
  badges?: React.ReactNode;
  actions?: React.ReactNode;
  testId?: string;
} & Omit<React.ComponentProps<"div">, "onToggle" | "color">;

export function MachineGroupHeader({
  machine,
  expanded,
  onToggle,
  attentionCount,
  attentionTone,
  badges,
  actions,
  testId,
  ...props
}: MachineGroupHeaderProps) {
  return (
    <IndexGroupHeader
      testId={testId}
      expanded={expanded}
      onToggle={onToggle}
      glyph={(className) => (
        <span
          data-testid="machine-group-status"
          data-online={machine.online}
          className={cn(
            "inline-flex items-center justify-center bg-secondary",
            className,
          )}
          aria-hidden="true"
        >
          <span
            className={cn(
              "inline-block size-1.5 rounded-full",
              machine.online ? "bg-status-running" : "bg-status-idle",
            )}
          />
        </span>
      )}
      label={machine.name}
      labelMuted={!machine.online}
      attentionCount={attentionCount}
      attentionTone={attentionTone}
      attentionTestId="machine-attention-mark"
      badges={badges}
      actions={actions}
      {...props}
    />
  );
}
