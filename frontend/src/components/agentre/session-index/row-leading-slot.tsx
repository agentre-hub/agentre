// row-leading-slot.tsx —— 会话行行首那 14px 槽位。
//
// 规格 docs/specs/2026-08-16-unified-chat-index.md 决策 4：槽位里放的是**分组
// 没说的那一维** —— 按项目分组时放 agent 头像，按 Agent 分组时放项目字形（项目
// 自己的图标 + 项目色，与项目组头同源，见 project-glyph.tsx）。同位置同尺寸，
// 切换分组时列表不跳动。
//
// 自由会话（`project === null`）在「按 Agent」档下**槽位保留、字形置灰**，
// 不是不渲染：少画一个字形会让这一行的左缘比邻居往左缩，整列参差。
//
// 「按时间」是两行行式（决策 5），两维都以行内文字给出，不占这个槽 —— 返回 null。
import type { IndexAxis } from "@/lib/session-axis";

import { AgentAvatar } from "../primitives";
import { type AgentColor } from "../types";

import { ProjectGlyph, type ProjectGlyphInfo } from "./project-glyph";

export type RowLeadingSlotProps = {
  axis: IndexAxis;
  agentName: string;
  /** 颜色 token，如 "agent-1"。 */
  agentColor: string;
  /** 会话所属项目；`null` = 自由会话（不属于任何项目）。 */
  project: ProjectGlyphInfo | null;
};

/** 槽位宽高锁死 14px：两档换着渲染的东西不同，占位必须完全一样。 */
const SLOT_CLASS_NAME =
  "inline-flex size-3.5 shrink-0 items-center justify-center";

/** 两维共用的字形尺寸：同一个方块，只是里面的身份不同。 */
const GLYPH_CLASS_NAME = "size-full rounded-sm text-[8px]";

export function RowLeadingSlot({
  axis,
  agentName,
  agentColor,
  project,
}: RowLeadingSlotProps) {
  if (axis === "time") return null;

  if (axis === "project") {
    return (
      <span
        data-testid="row-leading-slot"
        data-kind="agent-avatar"
        className={SLOT_CLASS_NAME}
      >
        <AgentAvatar
          name={agentName}
          initials={agentName.trim().slice(0, 1).toUpperCase() || undefined}
          color={(agentColor as AgentColor) || "agent-1"}
          size="sm"
          className={GLYPH_CLASS_NAME}
        />
      </span>
    );
  }

  return (
    <span
      data-testid="row-leading-slot"
      data-kind={project ? "project-avatar" : "free-glyph"}
      className={SLOT_CLASS_NAME}
    >
      <ProjectGlyph project={project} className={GLYPH_CLASS_NAME} />
    </span>
  );
}
