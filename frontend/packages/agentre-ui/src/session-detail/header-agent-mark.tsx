import type * as React from "react";

import type { AgentStatus } from "../transcript/agent-status";
import { AgentAvatar } from "../ui/agent-avatar";
import { StatusDot } from "../ui/status-dot";

export type SessionHeaderAgentMarkProps = {
  /** Agent 名：可及名，也取它的首字母。 */
  name: string;
  /** 身份色（agent-1…16 / agent-neutral，或宿主已解好的 CSS 颜色）。 */
  color?: string;
  /** 宿主已经解好的图标节点；不给就画首字母。图标词表是宿主的事，包不认 key。 */
  icon?: React.ReactNode;
  /** 这一轮的状态；空闲或说不出来（null）时角上不挂点。 */
  status: AgentStatus | null;
  badgeTestId?: string;
};

/**
 * 对话页顶带 meta 行里 Agent 那一段打头的身份记号：一枚 14px 小头像，状态点挂在它
 * 右下角，描一圈顶带色（`ring-sheet-header`）把点和头像分开。
 *
 * 顶带不再有 32px 身份方块（2026-10-20）：那副「方块 + 粗体一行 + 灰字一行」和转录里
 * 每条消息一模一样，读起来就是第一条消息。身份缩到这里，两端（桌面端对话页、
 * agentre-server 的会话详情与新对话草稿）共用这一份。空闲维持「没有记号」。
 */
export function SessionHeaderAgentMark({
  name,
  color,
  icon,
  status,
  badgeTestId,
}: SessionHeaderAgentMarkProps) {
  return (
    <span className="relative inline-flex shrink-0">
      <AgentAvatar
        name={name}
        // 首字母原样取（不大写）：中文名没有大小写，拉丁名也与会话行一致。
        initials={name.charAt(0)}
        color={color}
        icon={icon}
        size="xs"
      />
      {status && status !== "idle" ? (
        <StatusDot
          data-testid={badgeTestId}
          status={status}
          size="xs"
          className="absolute -right-[3px] -bottom-[3px] ring-[1.5px] ring-sheet-header"
        />
      ) : null}
    </span>
  );
}
