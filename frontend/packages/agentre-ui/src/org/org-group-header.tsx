import * as React from "react";
import { ChevronDown, GripVertical } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import type { OrgDragHandleBinding, OrgDropState } from "./drag-binding";
import type { OrgIndexGroup } from "./org-index-model";
import type { OrgSelection } from "./types";

/**
 * 索引里的一个组头 = 一个部门。
 *
 * 缩进只发生在这里（子部门缩在父部门里，`depth` 由部门链决定），组头上标出该部门
 * 的负责人。**空部门照常摆组头**（决策 13）—— 组里一行都没有并不代表这个部门不
 * 存在，藏起来用户就找不到往哪儿放人。
 *
 * 形态与行同族：**无底色的圆角块**，不是一条通栏灰带 —— 灰带把索引切成几段，而组
 * 头要读起来是「行的标题」。收放三角与右侧动作都只在宿主给了对应 prop 时才画。
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
  /**
   * 收起 / 展开。**状态归宿主**（与这一层「呈现件不判定任何东西」一致）：包只画
   * 三角的朝向并把点击原样交回去，谁被折起来、折起来之后哪些行还渲染，都是宿主的
   * 事。不给 `onToggleExpanded` 就不画三角 —— 画一个按下去没反应的三角是假手感。
   */
  expanded?: boolean;
  onToggleExpanded?: () => void;
  /**
   * 组头右侧的动作（「往这个部门加 Agent」之类）。**包里不硬编码任何动作语义**：
   * 两端能做的事不同（桌面端有对话框，agentre-server 走 REST + 权限），一旦在这里
   * 写死一个 `onAddAgent`，另一端就得传一个空函数假装有这个能力。
   *
   * 渲染在「选中」那个按钮**之外** —— `<button>` 不能嵌 `<button>`。
   */
  actions?: React.ReactNode;
};

export function OrgGroupHeader({
  group,
  selected,
  onSelect,
  droppable,
  dropState,
  dropRef,
  dragHandle,
  expanded = true,
  onToggleExpanded,
  actions,
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
      style={{ paddingLeft: 6 + group.depth * 15 }}
      className={cn(
        "flex items-center gap-1.5 rounded-md py-[5px] pr-1.5 text-xs font-semibold transition-colors",
        !selected && "hover:bg-sidebar-active-bg",
        selected && "bg-sidebar-selected-bg text-primary-text",
        dropState === "valid" && "ring-2 ring-primary/60",
        dropState === "invalid" && "ring-2 ring-destructive",
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
          className="shrink-0 cursor-grab touch-none select-none rounded-sm text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
        >
          <GripVertical className="size-3.5" aria-hidden="true" />
        </button>
      ) : (
        <span className="size-3.5 shrink-0" aria-hidden="true" />
      )}
      {onToggleExpanded && (
        <button
          type="button"
          data-testid={`org-group-toggle-${department.id}`}
          aria-expanded={expanded}
          aria-label={t("org.index.toggleGroup", { name: department.name })}
          onClick={onToggleExpanded}
          className="shrink-0 rounded-sm text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
        >
          <ChevronDown
            className={cn(
              "size-3 transition-transform duration-150 ease-out motion-reduce:transition-none",
              !expanded && "-rotate-90",
            )}
            aria-hidden="true"
          />
        </button>
      )}
      <button
        type="button"
        data-testid={`org-group-select-${department.id}`}
        onClick={() => onSelect({ kind: "department", id: department.id })}
        className="flex min-w-0 flex-1 items-center gap-1.5 text-left"
      >
        <span className="truncate">{department.name}</span>
        {department.leadAgentName && (
          <span className="inline-flex min-w-0 items-center gap-1 rounded-sm bg-secondary px-1.5 text-2xs font-normal text-muted-foreground">
            <span className="truncate">
              {t("org.index.lead", { name: department.leadAgentName })}
            </span>
          </span>
        )}
        <span className="ml-auto shrink-0 font-mono text-2xs font-normal text-muted-foreground">
          {t("org.department.departmentMemberCount", {
            count: department.memberCount ?? 0,
          })}
        </span>
      </button>
      {actions}
    </div>
  );
}
