import * as React from "react";

import { AgentAvatar } from "../ui/agent-avatar";
import type { IndexAxis } from "./axis-groups";
import { ProjectGlyph, type ProjectGlyphInfo } from "./project-glyph";

/**
 * 会话行行首那 14px 槽位。
 *
 * 决策 4/8：槽位里放的是**分组没说的那一维** —— 按项目（或按机器）分组时放 Agent
 * 字形，按 Agent 分组时放项目字形（与项目组头同源，见 project-glyph.tsx）。
 * 同位置同尺寸，切换分组时列表不跳动。
 *
 * 这一维解析不出来时**槽位保留、字形置灰**，不是不渲染：少画一个字形会让这一行的
 * 左缘比邻居往左缩，整列参差。
 *
 * 「按时间」是两行行式（决策 5），三维都以行内文字给出，不占这个槽 —— 返回 null。
 */

/** 槽位宽高锁死 14px：几档换着渲染的东西不同，占位必须完全一样。 */
const SLOT_CLASS_NAME =
  "inline-flex size-3.5 shrink-0 items-center justify-center";

/** 两维共用的字形尺寸：同一个方块，只是里面的身份不同。 */
const GLYPH_CLASS_NAME = "size-full rounded-sm text-[8px]";

export type RowLeadingSlotProps = {
  axis: IndexAxis;
  /** 会话的 Agent；`null` = 发起端没报过 Agent 标识的会话。 */
  agent?: { name: string; color?: string } | null;
  /** 会话所属项目；`null` = 不属于任何项目。 */
  project?: ProjectGlyphInfo | null;
  /** 宿主自带的 Agent 头像（桌面端的 AgentAvatar）。不给就用包内字形。 */
  agentGlyph?: React.ReactNode;
  /** 宿主自带的项目字形内容（图标注册表解出来的图标）。 */
  projectGlyph?: React.ReactNode;
};

export function RowLeadingSlot({
  axis,
  agent,
  project,
  agentGlyph,
  projectGlyph,
}: RowLeadingSlotProps) {
  if (axis === "time") return null;

  // 组头说了 Agent，行首就补项目；其余的轴（项目 / 机器）行首补 Agent。
  if (axis === "agent") {
    return (
      <span
        data-testid="row-leading-slot"
        data-kind={project ? "project-avatar" : "free-glyph"}
        className={SLOT_CLASS_NAME}
      >
        <ProjectGlyph
          project={project}
          glyph={projectGlyph}
          className={GLYPH_CLASS_NAME}
        />
      </span>
    );
  }

  return (
    <span
      data-testid="row-leading-slot"
      data-kind="agent-avatar"
      className={SLOT_CLASS_NAME}
    >
      <AgentAvatar
        name={agent?.name ?? ""}
        color={agent?.color}
        icon={agentGlyph}
        size="xs"
        className={GLYPH_CLASS_NAME}
      />
    </span>
  );
}
