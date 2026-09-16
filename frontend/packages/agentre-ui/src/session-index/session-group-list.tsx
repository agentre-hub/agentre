import * as React from "react";

import { cn } from "../lib/utils";

/**
 * 组列表容器 —— 控制区与组集合之间的**唯一列表边界**（规格 2026-09-16 决策 2）。
 *
 * 它只拥有「多个会话组怎么连续排列」这一条规则：列表内相邻顶层组之间不额外加纵向
 * 间距。控制区到整份列表的 12px 归宿主，会经 `className` 落在同一个边界上；此前
 * 宿主把这段间距当作组间距重复落在每个空组之间，连续空组因此被撑成一片留白。
 *
 * 它**不**判断有哪些组、不排序、不画页面级空态，也不认识 DnD —— desktop 的递归项目
 * 树与拖拽、server 的组映射都留在宿主，经 `children` 进来。宿主的产品动作不在这里
 * 变成 prop。
 */
type SessionGroupListProps = {
  children?: React.ReactNode;
  className?: string;
};

function SessionGroupList({ children, className }: SessionGroupListProps) {
  return (
    <div
      data-slot="session-group-list"
      className={cn("flex flex-col", className)}
    >
      {children}
    </div>
  );
}

export { SessionGroupList };
export type { SessionGroupListProps };
