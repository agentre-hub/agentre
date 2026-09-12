import * as React from "react";
import { useTranslation } from "react-i18next";
import {
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
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
  handleDragEnd: (event: DragEndEvent) => Promise<void>;
  reorderError: string | null;
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

  return { dragDisabled, sensors, handleDragEnd, reorderError };
}

export { useProjectReorder };
export type { ProjectReorder };
