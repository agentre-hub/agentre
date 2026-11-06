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
import { toast } from "sonner";

import { classifyAppError, type AppError } from "@/lib/app-error";
import type { IndexAxis } from "@/lib/session-axis";

import { FailureBody } from "../failure-body";

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

/**
 * 失败 toast 的正文：后端带码的那句人话当正文；机器原话只进默认折叠的「详情」。
 * 这个文件是 .ts，所以用 createElement 而不是 JSX。
 */
function failureDescription(err: AppError): React.ReactNode {
  if (!err.message && !err.detail) return undefined;
  return React.createElement(FailureBody, {
    message: err.message,
    detail: err.detail,
  });
}

// useProjectReorder 收拢项目组的同级拖拽排序:开关条件、dnd-kit 传感器、落点计算与
// 失败提示。
function useProjectReorder({
  axis,
  filtering,
  projectByID,
  refreshProjectData,
}: UseProjectReorderOptions): ProjectReorder {
  const { t } = useTranslation();

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
  // 顺序来自后端树，本地没有乐观重排 —— 失败时项目已经弹回原位。失败必须就地说
  // 出来并给「重试」，否则用户看到的是「拖了一下，松手弹回去，什么都没发生」。
  const save = React.useCallback(
    async function saveOrder(parentID: number, orderedIDs: number[]) {
      try {
        await WailsApp.ProjectReorder({ parentID, orderedIDs } as never);
      } catch (e) {
        toast.error(t("projects.errors.reorderFailed"), {
          description: failureDescription(classifyAppError(e)),
          action: {
            label: t("common.retry"),
            onClick: () => void saveOrder(parentID, orderedIDs),
          },
        });
        return;
      }
      refreshProjectData();
    },
    [refreshProjectData, t],
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
      await save(parentOf(activeID), siblings);
    },
    [dragDisabled, projectByID, save],
  );

  return {
    dragDisabled,
    sensors,
    collisionDetection: sameLevelCollision,
    handleDragEnd,
  };
}

export { useProjectReorder };
export type { ProjectReorder };
