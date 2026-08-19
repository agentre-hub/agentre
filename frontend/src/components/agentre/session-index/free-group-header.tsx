// free-group-header.tsx —— 「按项目」档里那个虚拟的「随手对话」组头（project_id = 0）。
//
// 规格 docs/specs/2026-08-16-unified-chat-index.md 决策 6/7：这个组**常驻**，
// 一条自由会话都没有时也渲染 —— 否则在项目轴下用户看不到任何「不属于项目」的入口
// （功能一直在，入口不在）。命名用「随手对话」而不是「未归项目」：前者是一个正当的
// 去处，后者读起来像分类失败的残留。
//
// 组头有 `＋`（直接开一条不带项目上下文的会话），**绝对没有 `⋮`** —— 虚拟组没有
// 设置 / 子项目 / 合并 / 删除可言，挂一个菜单上去是骗人。
import { ChevronDown, MessagesSquare, Plus } from "lucide-react";
import { useTranslation } from "react-i18next";

import { cn } from "@/lib/utils";
import type { AgentStatus } from "@/stores/types";

import { statusConfig } from "../types";

export type FreeGroupHeaderProps = {
  expanded: boolean;
  onToggle: () => void;
  /**
   * 需要关注的条数；0 时不渲染那枚圆点。
   *
   * **组内总条数不在这里** —— 组头不报「有几条」：条数就在下面列着，写出来是复述；
   * 需要关注的那几条则可能在折叠态下看不见，那才值得一个记号。
   */
  attentionCount: number;
  /**
   * 那枚记号的档位（`error > waiting > running`）。`null` = 没有需要关注的会话。
   * 与项目组头同一套 —— 同一枚记号在两种组头上不能一个说真话一个写死绿色。
   */
  attentionTone: AgentStatus | null;
  onNewSession: () => void;
};

export function FreeGroupHeader({
  expanded,
  onToggle,
  attentionCount,
  attentionTone,
  onNewSession,
}: FreeGroupHeaderProps) {
  const { t } = useTranslation();

  return (
    <div
      data-testid="free-group-header"
      className="group/free flex items-center gap-1.5 rounded-md px-2 py-1.5 text-xs hover:bg-sidebar-active-bg"
    >
      <button
        type="button"
        className="flex min-w-0 flex-1 items-center gap-1.5 outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
        onClick={onToggle}
        aria-expanded={expanded}
      >
        <ChevronDown
          className={cn(
            "size-3.5 text-muted-foreground transition-transform duration-150 ease-out motion-reduce:transition-none",
            !expanded && "-rotate-90",
          )}
          aria-hidden="true"
        />
        {/* 尺寸与项目组头的 AgentAvatar（size-6 rounded-md）一致，好让两种组头的
            名称起始 x 对齐；但这是个虚拟组，没有身份色，用中性面。 */}
        <span
          className="inline-flex size-6 shrink-0 items-center justify-center rounded-md bg-secondary text-muted-foreground"
          aria-hidden="true"
        >
          <MessagesSquare className="size-3.5" />
        </span>
        <span className="min-w-0 flex-1 truncate text-left text-[15px] font-semibold">
          {t("sessionIndex.free.name")}
        </span>
        {attentionCount > 0 && attentionTone ? (
          <span
            data-testid="free-attention-mark"
            className={cn(
              "inline-flex shrink-0 items-center gap-1 font-mono text-2xs",
              // 数字用状态的文字角色、点用填充角色 —— 与项目组头、与行本身同一套投影。
              statusConfig[attentionTone].textClassName,
            )}
            title={t("sessionIndex.free.attention", { count: attentionCount })}
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
      {/* 常驻可见（不像项目组头那样 hover 才现身）：决策 6 要的正是「入口一直在」。 */}
      <button
        type="button"
        aria-label={t("sessionIndex.free.newSession")}
        title={t("sessionIndex.free.newSession")}
        onClick={onNewSession}
        className="inline-flex size-5 shrink-0 cursor-pointer items-center justify-center rounded text-muted-foreground transition-colors hover:bg-accent hover:text-foreground motion-reduce:transition-none"
      >
        <Plus className="size-3" aria-hidden="true" />
      </button>
    </div>
  );
}
