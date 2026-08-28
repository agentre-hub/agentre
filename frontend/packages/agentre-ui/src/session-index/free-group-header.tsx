import type * as React from "react";
import { MessagesSquare, Plus } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import type { AgentStatus } from "../transcript/agent-status";

import { IndexGroupHeader } from "./group-header";

/**
 * 「按项目」档里那个虚拟的「随手对话」组头（不属于任何项目的那些会话）。
 *
 * 决策 6/7：这个组**常驻**，一条自由会话都没有时也渲染 —— 否则在项目轴下用户看不到
 * 任何「不属于项目」的入口（功能一直在，入口不在）。命名用「随手对话」而不是
 * 「未归项目」：前者是一个正当的去处，后者读起来像分类失败的残留。
 *
 * 组头有 `＋`（直接开一条不带项目上下文的会话）。设置 / 子项目 / 合并 / 删除这几样
 * 它一样都没有 —— 虚拟组上摆那份菜单是骗人。宿主真的有别的动作要挂（规格
 * 2026-08-26 的「导入本地会话…」就是一条，四条轴共用同一份定义），从 `actions`
 * 递进来，与 `＋` 并排。
 *
 * 外壳走 `IndexGroupHeader`（2026-08-22「组头归一」）：此前 agentre-server 那边照着
 * 这一件手画了一份，画丢了那格 24px 的中性面，于是同一个组头在两端一个有底框一个
 * 光秃一枚图标。
 */
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
  /**
   * 开一条不带项目上下文的会话。**可以不给**：决策 6 说的是「按项目轴下必须有这个
   * 入口」，而 agentre-server 的控制台把发起收在页面自己的发起区里，组头上再挂一枚
   * ＋ 是第二个入口。不给就不摆——不摆一个点了没反应的按钮。
   */
  onNewSession?: () => void;
  /** 宿主自己的角标（跟着名字走）。 */
  badges?: React.ReactNode;
  /**
   * 折叠按钮之外、`＋` 之后的动作。与 `＋` 并排而不是替换它 —— 决策 6 的那个入口
   * 不因为多挂了一件东西就消失。
   */
  actions?: React.ReactNode;
  /** 宿主查组头用的 id。不给就是这一件自己的名字。 */
  testId?: string;
} & Omit<React.ComponentProps<"div">, "onToggle">;

export function FreeGroupHeader({
  expanded,
  onToggle,
  attentionCount,
  attentionTone,
  onNewSession,
  badges,
  actions,
  testId = "free-group-header",
  ...props
}: FreeGroupHeaderProps) {
  const { t } = useUiTranslation();

  return (
    <IndexGroupHeader
      testId={testId}
      expanded={expanded}
      onToggle={onToggle}
      glyph={(className) => (
        // 尺寸与项目组头的字形一致，好让两种组头的名称起始 x 对齐；但这是个虚拟组，
        // 没有身份色，用中性面。
        <span
          className={cn(
            "inline-flex items-center justify-center bg-secondary text-muted-foreground",
            className,
          )}
          aria-hidden="true"
        >
          <MessagesSquare className="size-[60%]" />
        </span>
      )}
      label={t("sessionIndex.free.name")}
      attentionCount={attentionCount}
      attentionTone={attentionTone}
      attentionTestId="free-attention-mark"
      attentionTitle={t("sessionIndex.free.attention", {
        count: attentionCount,
      })}
      badges={badges}
      actions={
        onNewSession || actions ? (
          <>
            {onNewSession ? (
              // 常驻可见（不像项目组头那样 hover 才现身）：决策 6 要的正是
              // 「入口一直在」。
              <button
                type="button"
                data-testid={`${testId}-plus`}
                aria-label={t("sessionIndex.free.newSession")}
                title={t("sessionIndex.free.newSession")}
                onClick={onNewSession}
                className="inline-flex size-5 shrink-0 cursor-pointer items-center justify-center rounded text-muted-foreground transition-colors hover:bg-accent hover:text-foreground motion-reduce:transition-none"
              >
                <Plus className="size-3" aria-hidden="true" />
              </button>
            ) : null}
            {actions}
          </>
        ) : undefined
      }
      {...props}
    />
  );
}
