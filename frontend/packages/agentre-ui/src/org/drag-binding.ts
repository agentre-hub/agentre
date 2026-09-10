import type * as React from "react";

/**
 * 索引呈现件与**宿主的拖拽实现**之间那条缝。
 *
 * 包里没有 dnd-kit：传感器、`useDraggable` / `useDroppable`、键盘那条「拾起 /
 * 移动 / 落下 / 取消」都属于宿主 —— agentre-server 的组织面根本不拖拽（移动端与
 * 浏览器改归属走详情的下拉），它把这些字段全部不传，行照样画得出来。
 *
 * 所以呈现件收的是**已经算好的结果**：这一格是不是候选落点、合不合法。判据本身
 * （`isValidOrgDrop`）在包里，但「现在瞄的是哪一格」是宿主的手势状态。
 */

/** 落点的视觉态。`undefined` = 现在没在瞄这一格，什么颜色都不画。 */
export type OrgDropState = "valid" | "invalid" | undefined;

/** 拖拽柄：宿主把 `setActivatorNodeRef` / `attributes` / `listeners` 挂进来。 */
export type OrgDragHandleBinding = {
  ref?: (node: HTMLElement | null) => void;
  attributes?: React.HTMLAttributes<HTMLElement>;
  listeners?: React.DOMAttributes<HTMLElement>;
  onKeyDown?: (event: React.KeyboardEvent<HTMLElement>) => void;
};

/**
 * 可排序行（执行目标一档一行）的绑定：整行的 ref/位移样式由宿主的 `useSortable`
 * 给，拖拽柄单独一份 —— 柄是唯一的激活器，避免整行都变成拖拽热区把里面的按钮吞掉。
 */
export type OrgSortableRowBinding = {
  setNodeRef?: (node: HTMLElement | null) => void;
  handle?: OrgDragHandleBinding;
  style?: React.CSSProperties;
  isDragging?: boolean;
};
