import type * as React from "react";
import { Plus } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import type { AgentStatus } from "../transcript/agent-status";
import { AgentAvatar } from "../ui/agent-avatar";

import {
  IndexGroupHeader,
  groupActionRevealTouchClassName,
} from "./group-header";

/**
 * 「按 Agent」轴的组头（规格 2026-08-22「组头归一」）。
 *
 * 这一档的身份是**那一枚头像** —— 与行首那一槽、与 Agent 名单里的那一枚同一个
 * `AgentAvatar`，只是尺寸档不同。agentre-server 此前在这里只摆一颗 8px 色点，于是
 * 同一个 Agent 在桌面端是一张脸、在控制台是一个点。
 *
 * 图标同样不在这里解：那张注册表是宿主的产品决定，收的是已经画好的节点。
 */
export type AgentGroupHeaderProps = {
  agent: { name: string; color?: string };
  /** 宿主图标注册表解出来的那枚图标。 */
  glyph?: React.ReactNode;
  /**
   * 组头文案。不给就用 Agent 名 —— 没有 Agent 名的那一组由宿主给一句兜底文案，
   * 因为那是要 i18n 的一句话，包里说不出来。
   */
  label?: React.ReactNode;
  expanded: boolean;
  onToggle: () => void;
  attentionCount: number;
  attentionTone: AgentStatus | null;
  /**
   * 与这个 Agent 开一条新对话。**可以不给**：没有这条通路的宿主不摆——不摆一个点了
   * 没反应的按钮；没有 Agent 身份的那一组（老会话）同理，那儿没有「哪个 Agent」可谈。
   *
   * 与「随手对话」那颗常驻的 ＋ 不同，这一颗**hover 才现身**（窄屏照常驻，触摸屏
   * 没有 hover）：随手对话全列只有一组，而 Agent 轴上每一组都是一个 Agent，N 颗常驻
   * 的 ＋ 会把这一列读成一排按钮而不是一份索引。与项目组头上那颗同一档现身方式。
   */
  onNewSession?: () => void;
  badges?: React.ReactNode;
  actions?: React.ReactNode;
  testId?: string;
} & Omit<React.ComponentProps<"div">, "onToggle" | "color">;

export function AgentGroupHeader({
  agent,
  glyph,
  label,
  expanded,
  onToggle,
  attentionCount,
  attentionTone,
  onNewSession,
  badges,
  actions,
  testId,
  ...props
}: AgentGroupHeaderProps) {
  const { t } = useUiTranslation();
  const newSessionLabel = t("sessionIndex.agent.newSession", {
    name: agent.name,
  });

  return (
    <IndexGroupHeader
      testId={testId}
      expanded={expanded}
      onToggle={onToggle}
      glyph={(className) => (
        <AgentAvatar
          testId="agent-group-avatar"
          name={agent.name}
          color={agent.color}
          icon={glyph}
          size="sm"
          className={className}
        />
      )}
      label={label ?? agent.name}
      attentionCount={attentionCount}
      attentionTone={attentionTone}
      attentionTestId="agent-attention-mark"
      badges={badges}
      actions={
        onNewSession || actions ? (
          <>
            {onNewSession ? (
              // ＋ 摆在折叠按钮**外面**（外壳把 actions 这一格留在按钮外正是为了
              // 这个）：`<button>` 不能嵌 `<button>`，点开对话也不该顺手把这一组收起来。
              <button
                type="button"
                data-testid={testId ? `${testId}-plus` : "agent-group-plus"}
                aria-label={newSessionLabel}
                title={newSessionLabel}
                onClick={onNewSession}
                className={cn(
                  "inline-flex size-5 shrink-0 cursor-pointer items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground motion-reduce:transition-none",
                  groupActionRevealTouchClassName,
                )}
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
