// 树里的 agent 行(名字、头像、部门标签、拖拽手柄)。

import * as React from "react";
import { useDraggable, useDroppable } from "@dnd-kit/core";

import {
  OrgAgentRow,
  type OrgDragSubject,
  type OrgDropTarget,
  type OrgIndexRow,
} from "@agentre-hub/agentre-ui";

import { AgentAvatar } from "../primitives";
import { dropStateOf } from "./org-index-group-header";
import { safeAgentColor, type OrgSelection } from "./types";

export function AgentRow({
  row,
  indent,
  target,
  valid,
  current,
  selected,
  onSelect,
  onDragKeyDown,
}: {
  row: OrgIndexRow;
  indent: number;
  target?: OrgDropTarget;
  valid?: boolean;
  current: boolean;
  selected: boolean;
  onSelect: (sel: OrgSelection) => void;
  onDragKeyDown: (
    event: React.KeyboardEvent<HTMLElement>,
    subject: OrgDragSubject,
  ) => void;
}) {
  const agent = row.agent;
  const isSystem = agent.systemBadge === "DEFAULT";
  const subject: OrgDragSubject = { kind: "agent", id: agent.id };
  const { attributes, listeners, setActivatorNodeRef } = useDraggable({
    id: `agent-${agent.id}`,
    data: subject,
    disabled: isSystem,
  });
  const { isOver, setNodeRef } = useDroppable({
    id: `agent-drop-${agent.id}`,
    data: { kind: "agent", agentId: agent.id },
  });

  return (
    <OrgAgentRow
      row={row}
      indent={indent}
      selected={selected}
      onSelect={onSelect}
      droppable={Boolean(target)}
      dropState={dropStateOf(valid, current || isOver)}
      dropRef={setNodeRef}
      // 系统 Agent 不可拖动：不给绑定，包里画的就是那个等宽占位。
      dragHandle={
        isSystem
          ? undefined
          : {
              ref: setActivatorNodeRef,
              attributes,
              listeners,
              onKeyDown: (event) => onDragKeyDown(event, subject),
            }
      }
      avatar={
        // 索引这一枚是 18px（mockup `.av`）：32px 会把行高从 ≈28 顶到 ≥48，一屏
        // 少装一半的 Agent。详情里那枚仍是大的，两处不是同一个用途。
        <AgentAvatar
          name={agent.name}
          color={safeAgentColor(agent.avatarColor ?? "")}
          size="sm"
          avatarDataUrl={agent.avatarDataUrl}
          avatarIcon={agent.avatarIcon}
          className="size-4.5 shrink-0 rounded-[5px] text-3xs"
        />
      }
    />
  );
}
