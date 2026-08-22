import type * as React from "react";

import type { AgentStatus } from "../transcript/agent-status";
import { AgentAvatar } from "../ui/agent-avatar";

import { IndexGroupHeader } from "./group-header";

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
   * 组头文案。不给就用 Agent 名 —— 没有名字的老会话那一组由宿主给一句兜底文案，
   * 因为那是要 i18n 的一句话，包里说不出来。
   */
  label?: React.ReactNode;
  expanded: boolean;
  onToggle: () => void;
  attentionCount: number;
  attentionTone: AgentStatus | null;
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
  badges,
  actions,
  testId,
  ...props
}: AgentGroupHeaderProps) {
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
      actions={actions}
      {...props}
    />
  );
}
