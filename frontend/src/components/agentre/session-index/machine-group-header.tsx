// machine-group-header.tsx —— 「按机器」轴的组头（规格 2026-08-21）。
//
// 沿用桌面端组头的形（同高、同折叠记号、同 attention 记号位），只把项目那一格字形
// 换成机器的在线状态：这一维的身份是「哪台机器」，不是一个有颜色的方块。
//
// **离线只置灰，不禁用**：会话本体在本地库里，机器离线影响的是能不能在那台机器上
// 继续跑，不影响读。把组头做成不可展开会让一台机器一离线，它上面的历史就全部够不着。
//
// 与「随手对话」组头一样**没有 ⋮** —— 一台机器上没有设置 / 合并 / 删除可言。
import { ChevronDown } from "lucide-react";

import { cn } from "@/lib/utils";
import type { AgentStatus } from "@/stores/types";

import { statusConfig } from "../types";
import type { MachineRosterEntry } from "./machine-roster";

export type MachineGroupHeaderProps = {
  machine: MachineRosterEntry;
  expanded: boolean;
  onToggle: () => void;
  /** 需要关注的条数；0 时不渲染那枚记号。组内总条数不在组头上（下面列着）。 */
  attentionCount: number;
  /**
   * 那枚记号的档位（`error > waiting > running`）。`null` = 没有需要关注的会话。
   * 与项目组头、随手对话组头同一套 —— 同一枚记号不能一处说真话、一处写死颜色。
   */
  attentionTone: AgentStatus | null;
};

export function MachineGroupHeader({
  machine,
  expanded,
  onToggle,
  attentionCount,
  attentionTone,
}: MachineGroupHeaderProps) {
  return (
    <div className="flex items-center gap-1.5 rounded-md px-2 py-1.5 text-xs hover:bg-sidebar-active-bg">
      <button
        type="button"
        className="flex min-w-0 flex-1 cursor-pointer items-center gap-1.5 outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
        onClick={onToggle}
        aria-expanded={expanded}
      >
        <ChevronDown
          className={cn(
            "size-3 text-muted-foreground transition-transform duration-150 ease-out motion-reduce:transition-none",
            !expanded && "-rotate-90",
          )}
          aria-hidden="true"
        />
        {/* 尺寸与项目组头的字形一致，好让几种组头的名称起始 x 对齐。 */}
        <span
          data-testid="machine-group-status"
          data-online={machine.online}
          className="inline-flex size-6 shrink-0 items-center justify-center rounded-md bg-secondary"
          aria-hidden="true"
        >
          <span
            className={cn(
              "inline-block size-1.5 rounded-full",
              machine.online ? "bg-status-running" : "bg-status-idle",
            )}
          />
        </span>
        <span
          className={cn(
            "min-w-0 flex-1 truncate text-left text-prose font-semibold",
            // 离线只是颜色上退一档，不改变它能不能被打开。
            !machine.online && "text-muted-foreground",
          )}
        >
          {machine.name}
        </span>
        {attentionCount > 0 && attentionTone ? (
          <span
            data-testid="machine-attention-mark"
            className={cn(
              "inline-flex shrink-0 items-center gap-1 font-mono text-2xs",
              statusConfig[attentionTone].textClassName,
            )}
          >
            <span
              aria-hidden="true"
              className={cn(
                "inline-block size-1.5 rounded-full",
                statusConfig[attentionTone].dotClassName,
              )}
            />
            {attentionCount}
          </span>
        ) : null}
      </button>
    </div>
  );
}
