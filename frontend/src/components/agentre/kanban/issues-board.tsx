import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  closestCorners,
  useDroppable,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { Circle, CircleCheckBig, CircleDashed, CircleDot } from "lucide-react";
import * as React from "react";
import { useTranslation } from "react-i18next";

import type { app } from "../../../../wailsjs/go/models";
import { IssueLabels } from "../issue-labels";
import { afterIdForDrop, groupByStage } from "./reorder";
import { STAGES, type StageId } from "./stages";

type DndTransform = { x: number; y: number; scaleX: number; scaleY: number };

function transformToString(transform: DndTransform | null): string | undefined {
  if (!transform) return undefined;
  const { x, y, scaleX, scaleY } = transform;
  return `translate3d(${x}px, ${y}px, 0) scaleX(${scaleX}) scaleY(${scaleY})`;
}

const STAGE_ICON: Record<
  StageId,
  React.ComponentType<{ className?: string }>
> = {
  todo: Circle,
  doing: CircleDot,
  review: CircleDashed,
  done: CircleCheckBig,
};

export function IssuesBoard({
  issues,
  stageCounts,
  onEdit,
  onMove,
}: {
  issues: app.IssueItem[];
  stageCounts: Record<string, number>;
  onEdit: (issue: app.IssueItem) => void;
  onMove: (id: number, stage: StageId, afterID: number) => Promise<void> | void;
}) {
  const { t } = useTranslation();
  // 本地乐观镜像：拖拽后先改本地，再 await onMove。
  const [local, setLocal] = React.useState(issues);
  React.useEffect(() => setLocal(issues), [issues]);
  const grouped = React.useMemo(() => groupByStage(local), [local]);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    }),
  );

  const onDragEnd = (e: DragEndEvent) => {
    const activeId = Number(e.active.id);
    const over = e.over;
    if (!over) return;
    // over 可能是卡片 id 或列容器 id（"col:doing"）。
    const overId = String(over.id);
    const targetStage: StageId = overId.startsWith("col:")
      ? (overId.slice(4) as StageId)
      : ((local.find((i) => i.id === Number(overId))?.stage ||
          "todo") as StageId);
    const targetList = grouped[targetStage].filter((i) => i.id !== activeId);
    const overIndex = overId.startsWith("col:")
      ? targetList.length
      : targetList.findIndex((i) => i.id === Number(overId));
    const afterID = afterIdForDrop(targetList, Math.max(overIndex, 0));
    // 乐观：本地更新 stage（position 由后端权威，reload 时校正）。
    setLocal((prev) =>
      prev.map((i) =>
        i.id === activeId
          ? (Object.assign(Object.create(Object.getPrototypeOf(i)), i, {
              stage: targetStage,
            }) as app.IssueItem)
          : i,
      ),
    );
    void onMove(activeId, targetStage, afterID);
  };

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={closestCorners}
      onDragEnd={onDragEnd}
    >
      <section
        aria-label={t("issues.board.aria")}
        className="min-h-0 flex-1 overflow-auto bg-sidebar px-5 py-3.5"
      >
        <div className="flex min-w-max items-start gap-3">
          {STAGES.map((stage) => {
            const Icon = STAGE_ICON[stage.id];
            const items = grouped[stage.id];
            return (
              <section
                key={stage.id}
                id={`col:${stage.id}`}
                className="flex w-[300px] shrink-0 flex-col gap-2.5"
              >
                <header className="flex items-center gap-2 px-1">
                  <Icon className={`size-3.5 ${stage.accent}`} aria-hidden />
                  <h2 className="text-aux font-semibold">
                    {t(stage.labelKey)}
                  </h2>
                  <span className="font-mono text-2xs font-semibold text-muted-foreground">
                    {stageCounts[stage.id] ?? items.length}
                  </span>
                </header>
                <SortableContext
                  items={items.map((i) => i.id)}
                  strategy={verticalListSortingStrategy}
                >
                  <ColumnDropZone stageId={stage.id}>
                    {items.map((issue) => (
                      <BoardCard key={issue.id} issue={issue} onEdit={onEdit} />
                    ))}
                    {items.length === 0 ? (
                      <p className="rounded-md border border-dashed border-border px-3 py-6 text-center text-2xs text-muted-foreground">
                        {t("issues.board.emptyColumn")}
                      </p>
                    ) : null}
                  </ColumnDropZone>
                </SortableContext>
              </section>
            );
          })}
        </div>
      </section>
    </DndContext>
  );
}

function ColumnDropZone({
  stageId,
  children,
}: {
  stageId: StageId;
  children: React.ReactNode;
}) {
  const { setNodeRef } = useDroppable({ id: `col:${stageId}` });
  return (
    <div ref={setNodeRef} className="flex flex-col gap-2" data-stage={stageId}>
      {children}
    </div>
  );
}

function BoardCard({
  issue,
  onEdit,
}: {
  issue: app.IssueItem;
  onEdit: (issue: app.IssueItem) => void;
}) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: issue.id });
  const done = issue.stage === "done";
  return (
    <button
      ref={setNodeRef}
      type="button"
      onClick={() => onEdit(issue)}
      {...attributes}
      {...listeners}
      style={{ transform: transformToString(transform), transition }}
      className={`flex flex-col gap-2 rounded-md border bg-card px-3 py-2.5 text-left ${
        isDragging
          ? "border-primary shadow-lg opacity-90"
          : "border-border shadow-xs"
      } ${done ? "opacity-80" : ""}`}
    >
      <span className="font-mono text-2xs font-semibold text-primary-text">
        #{issue.id}
      </span>
      <h3 className="line-clamp-2 text-xs font-semibold leading-normal">
        {issue.title}
      </h3>
      <IssueLabels labels={issue.labels} />
    </button>
  );
}
