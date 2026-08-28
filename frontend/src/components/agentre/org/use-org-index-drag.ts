import * as React from "react";
import {
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DragStartEvent,
} from "@dnd-kit/core";
import { useTranslation } from "react-i18next";

import {
  isValidOrgDepartmentDrop,
  isValidOrgDrop,
  resolveOrgDrop,
  type OrgDragSubject,
  type OrgDropContext,
  type OrgDropTarget,
  type OrgIndexGroup,
  type OrgIndexRow,
} from "@agentre-hub/agentre-ui";

import { buildUnits, type DragState } from "./org-index-units";
import type { OrgIndexProps } from "./org-index";

/**
 * 索引那一条拖拽链：渲染单元 / 键盘候选落点 / 合法性判定 / 播报文案 / 落库，
 * 以及 dnd-kit 的指针传感器。留在 OrgIndex 里的只有筛选与装配。
 *
 * 拖拽状态本身仍归 OrgIndex 持有：`onDragCancel` 与这里的候选链读的是同一份。
 */
export function useOrgIndexDrag(
  props: OrgIndexProps,
  state: {
    topRows: OrgIndexRow[];
    groups: OrgIndexGroup[];
    drag: DragState | null;
    setDrag: React.Dispatch<React.SetStateAction<DragState | null>>;
    setAnnouncement: (text: string) => void;
  },
) {
  const { t } = useTranslation();
  const { agents, departments } = props;
  const { topRows, groups, drag, setDrag, setAnnouncement } = state;

  const ctx = React.useMemo<OrgDropContext>(
    () => ({ agents, departments }),
    [agents, departments],
  );
  const agentById = React.useMemo(
    () => new Map(agents.map((a) => [a.id, a])),
    [agents],
  );
  const departmentById = React.useMemo(
    () => new Map(departments.map((d) => [d.id, d])),
    [departments],
  );

  const units = React.useMemo(
    () => buildUnits(topRows, groups, drag),
    [topRows, groups, drag],
  );
  const candidates = React.useMemo(
    () =>
      units.flatMap((unit, unitIndex) =>
        unit.target ? [{ unitIndex, target: unit.target }] : [],
      ),
    [units],
  );

  const isValidTarget = React.useCallback(
    (subject: OrgDragSubject, target: OrgDropTarget): boolean =>
      subject.kind === "department"
        ? isValidOrgDepartmentDrop(subject.id, target, ctx)
        : isValidOrgDrop(subject.id, target, ctx),
    [ctx],
  );

  const subjectName = React.useCallback(
    (subject: OrgDragSubject): string =>
      (subject.kind === "agent"
        ? agentById.get(subject.id)?.name
        : departmentById.get(subject.id)?.name) ?? "",
    [agentById, departmentById],
  );

  const zoneLabel = React.useCallback(
    (target: OrgDropTarget): string => {
      switch (target.kind) {
        case "reorder": {
          const nextId = target.orderedIds[target.index];
          if (nextId === undefined) return t("org.index.drag.zoneReorderEnd");
          return t("org.index.drag.zoneReorder", {
            name: agentById.get(nextId)?.name ?? "",
          });
        }
        case "agent":
          return t("org.index.drag.zoneAgent", {
            name: agentById.get(target.agentId)?.name ?? "",
          });
        case "department":
          return t("org.index.drag.zoneDepartment", {
            name: departmentById.get(target.departmentId)?.name ?? "",
          });
      }
    },
    [agentById, departmentById, t],
  );

  const refusalReason = React.useCallback(
    (subject: OrgDragSubject, target: OrgDropTarget): string => {
      if (subject.kind === "department") {
        return target.kind === "department"
          ? t("org.index.drag.reasonDepartmentCycle")
          : t("org.index.drag.reasonGeneric");
      }
      if (agentById.get(subject.id)?.systemBadge === "DEFAULT") {
        return t("org.index.drag.reasonSystem");
      }
      if (target.kind === "reorder") return t("org.index.drag.reasonBucket");
      if (target.kind === "agent") {
        return agentById.get(target.agentId)?.systemBadge === "DEFAULT"
          ? t("org.index.drag.reasonSystemTarget")
          : t("org.index.drag.reasonCycle");
      }
      return t("org.index.drag.reasonGeneric");
    },
    [agentById, t],
  );

  const describeTarget = React.useCallback(
    (subject: OrgDragSubject, target: OrgDropTarget): string =>
      isValidTarget(subject, target)
        ? zoneLabel(target)
        : t("org.index.drag.refused", {
            zone: zoneLabel(target),
            reason: refusalReason(subject, target),
          }),
    [isValidTarget, refusalReason, t, zoneLabel],
  );

  const commit = React.useCallback(
    (subject: OrgDragSubject, target: OrgDropTarget) => {
      const op = resolveOrgDrop(subject, target, ctx);
      if (!op) {
        // 拒绝不等于取消：落点留在原地显示为拒绝态，拖拽仍在进行。
        setAnnouncement(describeTarget(subject, target));
        return;
      }
      switch (op.op) {
        case "reorderAgents":
          props.onReorderAgent(
            op.departmentId,
            op.parentAgentId,
            op.orderedIds,
          );
          break;
        case "moveAgent":
          props.onMoveAgent(op.id, {
            departmentId: op.newDepartmentId,
            parentAgentId: op.newParentAgentId,
          });
          break;
        case "moveDepartment":
          props.onMoveDepartment(op.id, op.newParentId);
          break;
      }
      setAnnouncement(
        t("org.index.drag.dropped", { name: subjectName(subject) }),
      );
      setDrag(null);
    },
    [ctx, describeTarget, props, setAnnouncement, setDrag, subjectName, t],
  );

  const handleDragKeyDown = React.useCallback(
    (event: React.KeyboardEvent<HTMLElement>, subject: OrgDragSubject) => {
      if (!drag || drag.mode !== "keyboard") {
        if (event.key === " ") {
          event.preventDefault();
          setDrag({ subject, mode: "keyboard", index: -1 });
          setAnnouncement(
            t("org.index.drag.pickedUp", { name: subjectName(subject) }),
          );
        }
        return;
      }
      if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        event.preventDefault();
        if (candidates.length === 0) return;
        const next = Math.min(
          Math.max(drag.index + (event.key === "ArrowDown" ? 1 : -1), 0),
          candidates.length - 1,
        );
        setDrag({ ...drag, index: next });
        setAnnouncement(describeTarget(drag.subject, candidates[next].target));
        return;
      }
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        const current = candidates[drag.index];
        if (current) commit(drag.subject, current.target);
        return;
      }
      if (event.key === "Escape") {
        event.preventDefault();
        setDrag(null);
        setAnnouncement(t("org.index.drag.cancelled"));
      }
    },
    [
      candidates,
      commit,
      describeTarget,
      drag,
      setAnnouncement,
      setDrag,
      subjectName,
      t,
    ],
  );

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
  );

  const handleDragStart = React.useCallback(
    (event: DragStartEvent) => {
      const subject = event.active.data.current as OrgDragSubject | undefined;
      if (subject) setDrag({ subject, mode: "pointer", index: -1 });
    },
    [setDrag],
  );

  const handleDragEnd = React.useCallback(
    (event: DragEndEvent) => {
      const subject = event.active.data.current as OrgDragSubject | undefined;
      const target = event.over?.data.current as OrgDropTarget | undefined;
      setDrag(null);
      if (subject && target) commit(subject, target);
    },
    [commit, setDrag],
  );

  const currentUnitIndex =
    drag && drag.mode === "keyboard" && drag.index >= 0
      ? candidates[drag.index]?.unitIndex
      : undefined;

  // 落点合不合法只算一次，指针悬停与键盘停留共用它：非法落点两条路径下都必须**留在
  // 原地画成拒绝态**，而不是一条路上变红、另一条路上照样高亮成可放置。
  const validityOf = (target?: OrgDropTarget) =>
    drag && target ? isValidTarget(drag.subject, target) : undefined;

  return {
    units,
    sensors,
    handleDragStart,
    handleDragEnd,
    handleDragKeyDown,
    currentUnitIndex,
    validityOf,
  };
}
