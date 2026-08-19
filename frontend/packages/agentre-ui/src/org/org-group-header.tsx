import * as React from "react";
import { GripVertical } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { Glyph } from "../session-index/glyph";
import type { OrgDragHandleBinding, OrgDropState } from "./drag-binding";
import type { OrgIndexGroup } from "./org-index-model";
import type { OrgSelection } from "./types";

/**
 * 索引里的一个组头 = 一个部门。
 *
 * 缩进只发生在这里（子部门缩在父部门里，`depth` 由部门链决定），组头上标出该部门
 * 的负责人。**空部门照常摆组头**（决策 13）—— 组里一行都没有并不代表这个部门不
 * 存在，藏起来用户就找不到往哪儿放人。
 */
export type OrgGroupHeaderProps = {
  group: OrgIndexGroup;
  selected: boolean;
  onSelect: (selection: OrgSelection) => void;
  /** 这个组头现在是不是一个落点（决定 `data-drop-kind`）。 */
  droppable?: boolean;
  dropState?: OrgDropState;
  dropRef?: (node: HTMLElement | null) => void;
  /** 拖拽柄。不传就画一个等宽占位，让它与行的左缘仍然对齐。 */
  dragHandle?: OrgDragHandleBinding;
  /** 宿主自带的部门字形（图标注册表解出来的那一枚）。 */
  glyph?: React.ReactNode;
};

export function OrgGroupHeader({
  group,
  selected,
  onSelect,
  droppable,
  dropState,
  dropRef,
  dragHandle,
  glyph,
}: OrgGroupHeaderProps) {
  const { t } = useUiTranslation();
  // 解构而不是留着 `dragHandle.` 前缀：把它的 ref 挂到 ref= 上会让整个绑定对象被
  // react-hooks/refs 视作 ref，之后连 attributes 都读不得（宿主那侧踩过同一条）。
  const {
    ref: handleRef,
    attributes: handleAttributes,
    listeners: handleListeners,
    onKeyDown: onHandleKeyDown,
  } = dragHandle ?? {};
  const department = group.department;

  return (
    <div
      ref={dropRef}
      data-testid={`org-group-${department.id}`}
      data-slot="org-group-header"
      data-department-id={department.id}
      data-depth={group.depth}
      data-drop-kind={droppable ? "department" : undefined}
      data-drop-state={dropState}
      aria-current={selected ? "true" : undefined}
      style={{ paddingLeft: 20 + group.depth * 16 }}
      className={cn(
        "flex items-center gap-2 border-b border-t bg-secondary/60 py-1.5 pr-5",
        dropState === "valid" && "ring-2 ring-primary/60",
        dropState === "invalid" && "ring-2 ring-destructive",
        selected && "bg-primary-soft",
      )}
    >
      {dragHandle ? (
        <button
          type="button"
          ref={handleRef}
          data-testid={`org-group-handle-${department.id}`}
          aria-label={t("org.index.drag.departmentHandle", {
            name: department.name,
          })}
          {...(handleAttributes ?? {})}
          {...(handleListeners ?? {})}
          onKeyDown={onHandleKeyDown}
          className="shrink-0 cursor-grab touch-none select-none rounded-sm text-subtle-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
        >
          <GripVertical className="size-3.5" aria-hidden="true" />
        </button>
      ) : (
        <span className="size-3.5 shrink-0" aria-hidden="true" />
      )}
      <button
        type="button"
        data-testid={`org-group-select-${department.id}`}
        onClick={() => onSelect({ kind: "department", id: department.id })}
        className="flex min-w-0 flex-1 items-center gap-2 text-left"
      >
        {glyph ?? (
          <Glyph
            color={department.accentColor}
            className="size-5 shrink-0 rounded-md"
          />
        )}
        <span className="truncate text-xs font-semibold">
          {department.name}
        </span>
        {department.leadAgentName && (
          <span className="inline-flex min-w-0 items-center gap-1 rounded-sm bg-card px-1.5 py-0.5 font-mono text-2xs font-semibold">
            <span className="truncate">
              {t("org.index.lead", { name: department.leadAgentName })}
            </span>
          </span>
        )}
        <span className="ml-auto shrink-0 font-mono text-2xs text-muted-foreground">
          {t("org.department.departmentMemberCount", {
            count: department.memberCount ?? 0,
          })}
        </span>
      </button>
    </div>
  );
}
