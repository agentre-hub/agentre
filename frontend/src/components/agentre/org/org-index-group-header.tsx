// 树里的部门组头,以及拖拽时插在组之间的落点线。

import * as React from "react";
import { useDraggable, useDroppable } from "@dnd-kit/core";
import { Plus } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  OrgGroupHeader,
  OrgInsertLine,
  type OrgDragSubject,
  type OrgDropTarget,
  type OrgIndexGroup,
} from "@agentre-hub/agentre-ui";

import { type OrgSelection } from "./types";

export function GroupHeader({
  group,
  target,
  valid,
  current,
  selected,
  onSelect,
  onDragKeyDown,
  expanded,
  onToggleExpanded,
  onCreateAgent,
}: {
  group: OrgIndexGroup;
  target?: OrgDropTarget;
  valid?: boolean;
  current: boolean;
  selected: boolean;
  onSelect: (sel: OrgSelection) => void;
  onDragKeyDown: (
    event: React.KeyboardEvent<HTMLElement>,
    subject: OrgDragSubject,
  ) => void;
  expanded: boolean;
  onToggleExpanded: (departmentId: number) => void;
  onCreateAgent?: (departmentId: number) => void;
}) {
  const { t } = useTranslation();
  const department = group.department;
  const subject: OrgDragSubject = { kind: "department", id: department.id };
  const { attributes, listeners, setActivatorNodeRef } = useDraggable({
    id: `dept-${department.id}`,
    data: subject,
  });
  const { isOver, setNodeRef } = useDroppable({
    id: `dept-drop-${department.id}`,
    data: { kind: "department", departmentId: department.id },
  });
  return (
    <OrgGroupHeader
      group={group}
      selected={selected}
      onSelect={onSelect}
      droppable={Boolean(target)}
      dropState={dropStateOf(valid, current || isOver)}
      dropRef={setNodeRef}
      dragHandle={{
        ref: setActivatorNodeRef,
        attributes,
        listeners,
        onKeyDown: (event) => onDragKeyDown(event, subject),
      }}
      expanded={expanded}
      onToggleExpanded={() => onToggleExpanded(department.id)}
      actions={
        onCreateAgent ? (
          <button
            type="button"
            aria-label={t("org.index.addAgentToDepartment", {
              name: department.name,
            })}
            title={t("org.index.addAgentToDepartment", {
              name: department.name,
            })}
            onClick={() => onCreateAgent(department.id)}
            className="inline-flex size-5 shrink-0 cursor-pointer items-center justify-center rounded text-muted-foreground transition-colors hover:bg-accent hover:text-foreground motion-reduce:transition-none"
          >
            <Plus className="size-3" aria-hidden="true" />
          </button>
        ) : undefined
      }
    />
  );
}

export function InsertLine({
  id,
  target,
  valid,
  current,
}: {
  id: string;
  target: OrgDropTarget;
  valid?: boolean;
  current: boolean;
}) {
  // 解构而不是留着 `drop.` 前缀：把 setNodeRef 挂到 ref= 上会让整个返回对象被
  // react-hooks/refs 视作 ref，之后连 isOver 都读不得。
  const { isOver, setNodeRef } = useDroppable({ id, data: target });
  return (
    <OrgInsertLine
      id={id}
      dropRef={setNodeRef}
      dropState={dropStateOf(valid, current || isOver)}
    />
  );
}

/** 只有正被瞄准的候选落点才有状态；不是候选的地方悬停也不画颜色。 */
export function dropStateOf(
  valid: boolean | undefined,
  aimed: boolean,
): "valid" | "invalid" | undefined {
  if (!aimed || valid === undefined) return undefined;
  return valid ? "valid" : "invalid";
}
