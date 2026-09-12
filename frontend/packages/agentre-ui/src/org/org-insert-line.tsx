import type { OrgDropState } from "./drag-binding";
import { cn } from "../lib/utils";

/**
 * 行与行之间那条细线 —— 三种落点里的**纯排序**那一种。
 *
 * 只在被拖那一行自己的桶（同部门、同上级）里出现；哪些位置该长出细线由宿主的拖拽
 * 状态决定，这里只负责画。非法时**留在原地画成拒绝态**，而不是让高亮消失。
 */
export type OrgInsertLineProps = {
  /** 落点 id，同时充当 `data-testid`（宿主的 droppable id）。 */
  id: string;
  dropState?: OrgDropState;
  dropRef?: (node: HTMLElement | null) => void;
};

export function OrgInsertLine({ id, dropState, dropRef }: OrgInsertLineProps) {
  return (
    <div
      ref={dropRef}
      data-testid={id}
      data-slot="org-insert-line"
      data-drop-kind="reorder"
      data-drop-state={dropState}
      aria-hidden="true"
      className={cn(
        "mx-5 h-0.5 rounded-full transition-colors",
        dropState === "valid" ? "bg-primary" : "bg-border",
        dropState === "invalid" && "bg-destructive",
      )}
    />
  );
}
