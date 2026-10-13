import * as React from "react";
import { useTranslation } from "react-i18next";
import {
  KeyboardSensor,
  PointerSensor,
  closestCenter,
  useSensor,
  useSensors,
  type CollisionDetection,
  type DragEndEvent,
} from "@dnd-kit/core";
import { sortableKeyboardCoordinates } from "@dnd-kit/sortable";

import type { IndexAxis } from "@/lib/session-axis";

import * as WailsApp from "../../../../wailsjs/go/app/App";
import type { app } from "../../../../wailsjs/go/models";

type UseProjectReorderOptions = {
  axis: IndexAxis;
  /** 搜索或状态 chip 生效中。过滤后的列表里顺序没有意义（决策 9）。 */
  filtering: boolean;
  projectByID: Map<number, app.ProjectItem>;
  refreshProjectData: () => void;
};

type ProjectReorder = {
  /** 只在「按项目」下开启，且筛选生效时禁用（决策 9）。*/
  dragDisabled: boolean;
  sensors: ReturnType<typeof useSensors>;
  collisionDetection: CollisionDetection;
  handleDragEnd: (event: DragEndEvent) => Promise<void>;
  reorderError: string | null;
};

/**
 * 落点只在**同一层**里找。
 *
 * 每个项目组把整组（组头 + 会话 + 子项目）登记成落点，子项目又各自登记一遍，所以
 * 一个根项目拖进下一个根项目的范围时，往往同时压在那个根项目的子项目上。默认的
 * 重叠比例算法会挑中那个子项目 —— 跨了层，排序只做同级，于是这一下被丢掉：拖了
 * 一下，松手弹回，什么都没发生。同级由 SortableContext 的 containerId 认（每一层
 * 一个 context）。
 */
const sameLevelCollision: CollisionDetection = (args) => {
  const level = args.active.data.current?.sortable?.containerId;
  return closestCenter({
    ...args,
    droppableContainers: args.droppableContainers.filter(
      (c) => c.data.current?.sortable?.containerId === level,
    ),
  });
};

// useProjectReorder 收拢项目组的同级拖拽排序:开关条件、dnd-kit 传感器、落点计算与
// 失败提示。
function useProjectReorder({
  axis,
  filtering,
  projectByID,
  refreshProjectData,
}: UseProjectReorderOptions): ProjectReorder {
  const { t } = useTranslation();
  const [reorderError, setReorderError] = React.useState<string | null>(null);

  const dragDisabled = axis !== "project" || filtering;
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
      // 拾起只认空格。默认还认回车，而监听挂在组头上：回车被它吃掉，键盘就再也
      // 折不起 / 展不开一个项目组了。回车归折叠，空格归排序。
      keyboardCodes: {
        start: ["Space"],
        cancel: ["Escape"],
        end: ["Space", "Enter"],
      },
    }),
  );
  const handleDragEnd = React.useCallback(
    async (event: DragEndEvent) => {
      if (dragDisabled) return;
      const activeID = Number(String(event.active.id).replace("project-", ""));
      const overID = Number(
        String(event.over?.id ?? "").replace("project-", ""),
      );
      if (!activeID || !overID || activeID === overID) return;
      const parentOf = (id: number) => projectByID.get(id)?.parentID ?? 0;
      if (parentOf(activeID) !== parentOf(overID)) return; // 跨父不允许
      const siblings = [...projectByID.values()]
        .filter((p) => (p.parentID ?? 0) === parentOf(activeID))
        .map((p) => p.id);
      const from = siblings.indexOf(activeID);
      const to = siblings.indexOf(overID);
      if (from < 0 || to < 0) return;
      siblings.splice(to, 0, ...siblings.splice(from, 1));
      // 顺序来自后端树，本地没有乐观重排可回滚 —— 但失败必须说出来，否则用户看到的
      // 是「拖了一下，松手弹回去，什么都没发生」。
      setReorderError(null);
      try {
        await WailsApp.ProjectReorder({
          parentID: parentOf(activeID),
          orderedIDs: siblings,
        } as never);
      } catch (e) {
        setReorderError(
          t("projects.errors.reorderFailed", { error: String(e) }),
        );
        return;
      }
      refreshProjectData();
    },
    [dragDisabled, projectByID, refreshProjectData, t],
  );

  return {
    dragDisabled,
    sensors,
    collisionDetection: sameLevelCollision,
    handleDragEnd,
    reorderError,
  };
}

export { useProjectReorder };
export type { ProjectReorder };
