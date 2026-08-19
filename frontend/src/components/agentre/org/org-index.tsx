import * as React from "react";
import {
  DndContext,
  PointerSensor,
  useDraggable,
  useDroppable,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DragStartEvent,
} from "@dnd-kit/core";
import {
  ChevronDown,
  CornerDownRight,
  FolderPlus,
  GripVertical,
  Plus,
  Search,
  Server,
  X,
} from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

import type { agent_backend_svc } from "../../../../wailsjs/go/models";
import { AgentAvatar } from "../primitives";
import { agentColorClassNames } from "../types";
import {
  isValidOrgDepartmentDrop,
  isValidOrgDrop,
  resolveOrgDrop,
  type OrgDragSubject,
  type OrgDropContext,
  type OrgDropTarget,
} from "./org-drop";
import {
  buildOrgIndex,
  buildReportsToOptions,
  type OrgIndexGroup,
  type OrgIndexRow,
} from "./org-index-model";
import {
  iconForKey,
  safeAgentColor,
  type OrgAgent,
  type OrgDepartment,
  type OrgSelection,
} from "./types";

// /org 的唯一视图：一条部门轴索引。
//
// 组头是部门（按 parent 递归缩进），行是 Agent 且**一律同级** —— 下属 Agent 与主管
// 平排，只在行内带一句 `↳ 主管`。三种落点各自对应一个入参不变的写操作（见
// ./org-drop.ts），键盘走一条显式的「拾起 / 移动 / 落下 / 取消」，当前落点属于哪一类
// 由 role="status" 播报出来 —— 只靠颜色区分的话，读屏和色觉障碍都读不到这一维。

export type OrgIndexProps = {
  departments: OrgDepartment[];
  agents: OrgAgent[];
  backends: agent_backend_svc.BackendItem[];
  selected: OrgSelection;
  onSelect: (sel: OrgSelection) => void;
  onMoveAgent: (
    id: number,
    placement: { departmentId: number; parentAgentId: number },
  ) => void;
  onMoveDepartment: (id: number, parentId: number) => void;
  onReorderAgent: (
    departmentId: number,
    parentAgentId: number,
    orderedIds: number[],
  ) => void;
  onCreateDepartment: () => void;
  /** 没有任何可挂载节点时不给这条出路（新 Agent 挂不上去）。 */
  onCreateAgent?: () => void;
};

type DragState = {
  subject: OrgDragSubject;
  mode: "pointer" | "keyboard";
  /** 键盘当前停在第几个候选落点；-1 = 刚拾起，还没选。 */
  index: number;
};

/** 渲染单元与键盘候选落点同源：两者一旦各走一套，方向键就会指到画面之外。 */
type IndexUnit =
  | { key: string; kind: "insert"; target: OrgDropTarget }
  | {
      key: string;
      kind: "header";
      group: OrgIndexGroup;
      target?: OrgDropTarget;
    }
  | {
      key: string;
      kind: "row";
      row: OrgIndexRow;
      /** 缩进只由所在部门决定 —— 下属与主管同级，见 org-index-model.ts。 */
      indent: number;
      target?: OrgDropTarget;
    };

function buildUnits(
  topRows: OrgIndexRow[],
  groups: OrgIndexGroup[],
  drag: DragState | null,
): IndexUnit[] {
  const subject = drag?.subject ?? null;
  const draggingAgent = subject?.kind === "agent" ? subject.id : 0;
  const draggingDepartment = subject?.kind === "department" ? subject.id : 0;

  const allRows = [...topRows, ...groups.flatMap((g) => g.rows)];
  const subjectRow = draggingAgent
    ? allRows.find((r) => r.agent.id === draggingAgent)
    : undefined;
  // 排序细线只在被拖那一行自己的桶里出现（同部门、同上级）。
  const bucketRows = subjectRow
    ? allRows.filter(
        (r) =>
          r.departmentId === subjectRow.departmentId &&
          r.parentAgentId === subjectRow.parentAgentId,
      )
    : [];
  const lastBucketRowId = bucketRows.at(-1)?.agent.id ?? 0;

  const insertUnit = (row: OrgIndexRow, index: number): IndexUnit => ({
    key: `insert-${row.departmentId}-${row.parentAgentId}-${index}`,
    kind: "insert",
    target: {
      kind: "reorder",
      departmentId: row.departmentId,
      parentAgentId: row.parentAgentId,
      index,
      orderedIds: row.bucketOrderedIds,
    },
  });

  const rowUnits = (rows: OrgIndexRow[], indent: number): IndexUnit[] =>
    rows.flatMap((row) => {
      const inBucket =
        subjectRow !== undefined &&
        row.departmentId === subjectRow.departmentId &&
        row.parentAgentId === subjectRow.parentAgentId;
      const units: IndexUnit[] = [];
      if (inBucket) units.push(insertUnit(row, row.bucketIndex));
      units.push({
        key: `row-${row.agent.id}`,
        kind: "row",
        row,
        indent,
        // 拖部门时行不是落点；拖 Agent 时自己那一行也不是。
        target:
          draggingAgent && row.agent.id !== draggingAgent
            ? { kind: "agent", agentId: row.agent.id }
            : undefined,
      });
      if (inBucket && row.agent.id === lastBucketRowId) {
        units.push(insertUnit(row, row.bucketOrderedIds.length));
      }
      return units;
    });

  return [
    ...rowUnits(topRows, 0),
    ...groups.flatMap((group) => [
      {
        key: `header-${group.department.id}`,
        kind: "header" as const,
        group,
        target:
          subject && group.department.id !== draggingDepartment
            ? ({
                kind: "department",
                departmentId: group.department.id,
              } as OrgDropTarget)
            : undefined,
      },
      ...rowUnits(group.rows, group.depth),
    ]),
  ];
}

export function OrgIndex(props: OrgIndexProps) {
  const { t } = useTranslation();
  const { agents, departments, backends } = props;

  const [search, setSearch] = React.useState("");
  const [backendId, setBackendId] = React.useState(0);
  const [reportsToId, setReportsToId] = React.useState(0);
  const [drag, setDrag] = React.useState<DragState | null>(null);
  const [announcement, setAnnouncement] = React.useState("");

  const filters = React.useMemo(
    () => ({ search, backendId, reportsToId }),
    [search, backendId, reportsToId],
  );
  const model = React.useMemo(
    () => buildOrgIndex({ agents, departments, filters }),
    [agents, departments, filters],
  );
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
  const reportsToOptions = React.useMemo(
    () => buildReportsToOptions(agents, departments),
    [agents, departments],
  );

  const units = React.useMemo(
    () => buildUnits(model.topRows, model.groups, drag),
    [model, drag],
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
    [ctx, describeTarget, props, subjectName, t],
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
    [candidates, commit, describeTarget, drag, subjectName, t],
  );

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
  );

  const handleDragStart = React.useCallback((event: DragStartEvent) => {
    const subject = event.active.data.current as OrgDragSubject | undefined;
    if (subject) setDrag({ subject, mode: "pointer", index: -1 });
  }, []);

  const handleDragEnd = React.useCallback(
    (event: DragEndEvent) => {
      const subject = event.active.data.current as OrgDragSubject | undefined;
      const target = event.over?.data.current as OrgDropTarget | undefined;
      setDrag(null);
      if (subject && target) commit(subject, target);
    },
    [commit],
  );

  const currentUnitIndex =
    drag && drag.mode === "keyboard" && drag.index >= 0
      ? candidates[drag.index]?.unitIndex
      : undefined;

  // 落点合不合法只算一次，指针悬停与键盘停留共用它：非法落点两条路径下都必须**留在
  // 原地画成拒绝态**，而不是一条路上变红、另一条路上照样高亮成可放置。
  const validityOf = (target?: OrgDropTarget) =>
    drag && target ? isValidTarget(drag.subject, target) : undefined;

  // ── 顶栏的两个筛选 + 命中后的 chip ──
  const backendName = backends.find((b) => b.id === backendId)?.name ?? "";
  const reportsToName =
    reportsToOptions.find((a) => a.id === reportsToId)?.name ?? "";
  const conditions = [
    search.trim()
      ? {
          key: "search",
          label: t("org.index.filters.searchLabel", { value: search.trim() }),
          clear: () => setSearch(""),
        }
      : null,
    backendId > 0
      ? {
          key: "backend",
          label: t("org.index.filters.backendLabel", { name: backendName }),
          clear: () => setBackendId(0),
        }
      : null,
    reportsToId > 0
      ? {
          key: "reportsTo",
          label: t("org.index.filters.reportsToLabel", { name: reportsToName }),
          clear: () => setReportsToId(0),
        }
      : null,
  ].filter((c): c is { key: string; label: string; clear: () => void } =>
    Boolean(c),
  );
  const clearAll = () => {
    setSearch("");
    setBackendId(0);
    setReportsToId(0);
  };
  // chip 只替**筛选**说话：搜索词已经摆在搜索框里了，再长一枚 chip 是复述
  // （规格「顶栏只有搜索框与筛选入口；筛选命中后才在下方长出可清除的 chip」）。
  const filterChips = conditions.filter((c) => c.key !== "search");
  const noMatch = model.matchedAgents === 0 && conditions.length > 0;

  return (
    <div
      className="flex h-full min-h-0 min-w-0 flex-1 flex-col bg-card"
      data-slot="org-index"
    >
      <div
        className="flex shrink-0 items-center gap-2 border-b bg-background px-5 py-2.5"
        data-slot="org-index-toolbar"
      >
        <div className="relative">
          <Search
            aria-hidden="true"
            className="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"
          />
          <Input
            aria-label={t("org.index.search.aria")}
            placeholder={t("org.index.search.placeholder")}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="h-8 w-60 pl-7 text-xs"
          />
        </div>

        <FilterMenu
          testId="org-filter-backend"
          ariaLabel={t("org.index.filters.backendAria")}
          label={t("org.index.filters.backendLabel", {
            name: backendName || t("common.all"),
          })}
          active={backendId > 0}
          icon={<Server className="size-3.5" aria-hidden="true" />}
          value={String(backendId)}
          onValueChange={(value) => setBackendId(Number(value))}
          options={backends.map((b) => ({ id: b.id, label: b.name }))}
        />
        <FilterMenu
          testId="org-filter-reportsTo"
          ariaLabel={t("org.index.filters.reportsToAria")}
          label={t("org.index.filters.reportsToLabel", {
            name: reportsToName || t("common.all"),
          })}
          active={reportsToId > 0}
          icon={<CornerDownRight className="size-3.5" aria-hidden="true" />}
          value={String(reportsToId)}
          onValueChange={(value) => setReportsToId(Number(value))}
          options={reportsToOptions.map((a) => ({ id: a.id, label: a.name }))}
        />
      </div>

      {filterChips.length > 0 && (
        <div
          className="flex shrink-0 flex-wrap items-center gap-1.5 border-b bg-background px-5 py-1.5"
          data-slot="org-index-chips"
          data-testid="org-index-chips"
        >
          {filterChips.map((condition) => (
            <button
              key={condition.key}
              type="button"
              aria-label={t("org.index.filters.clear", {
                label: condition.label,
              })}
              onClick={condition.clear}
              className="inline-flex items-center gap-1 rounded-full border border-primary bg-primary-soft px-2 py-0.5 font-mono text-2xs text-primary-text"
            >
              <span>{condition.label}</span>
              <X className="size-3" aria-hidden="true" />
            </button>
          ))}
        </div>
      )}

      <p
        role="status"
        aria-live="polite"
        data-testid="org-index-announcer"
        className="sr-only"
      >
        {announcement}
      </p>

      <div
        className="min-h-0 flex-1 overflow-y-auto"
        data-slot="org-index-body"
      >
        {noMatch ? (
          <div className="flex h-full flex-col items-center justify-center gap-2 p-8 text-center">
            <span className="text-sm text-muted-foreground">
              {t("org.index.noMatch.title")}
            </span>
            <span
              className="font-mono text-2xs text-muted-foreground"
              data-testid="org-index-no-match-conditions"
            >
              {t("org.index.noMatch.conditions", {
                conditions: conditions.map((c) => c.label).join(" · "),
              })}
            </span>
            <Button variant="outline" size="sm" onClick={clearAll}>
              {t("org.index.filters.clearAll")}
            </Button>
          </div>
        ) : (
          <DndContext
            sensors={sensors}
            onDragStart={handleDragStart}
            onDragEnd={handleDragEnd}
            onDragCancel={() => setDrag(null)}
          >
            {units.map((unit, unitIndex) => {
              const valid = validityOf(unit.target);
              const current = unitIndex === currentUnitIndex;
              if (unit.kind === "insert") {
                return (
                  <InsertLine
                    key={unit.key}
                    id={unit.key}
                    target={unit.target}
                    valid={valid}
                    current={current}
                  />
                );
              }
              if (unit.kind === "header") {
                return (
                  <GroupHeader
                    key={unit.key}
                    group={unit.group}
                    target={unit.target}
                    valid={valid}
                    current={current}
                    selected={
                      props.selected?.kind === "department" &&
                      props.selected.id === unit.group.department.id
                    }
                    onSelect={props.onSelect}
                    onDragKeyDown={handleDragKeyDown}
                  />
                );
              }
              return (
                <AgentRow
                  key={unit.key}
                  row={unit.row}
                  indent={unit.indent}
                  target={unit.target}
                  valid={valid}
                  current={current}
                  selected={
                    props.selected?.kind === "agent" &&
                    props.selected.id === unit.row.agent.id
                  }
                  onSelect={props.onSelect}
                  onDragKeyDown={handleDragKeyDown}
                />
              );
            })}

            {departments.length === 0 && (
              <EmptyDepartments
                onCreateDepartment={props.onCreateDepartment}
                onCreateAgent={props.onCreateAgent}
              />
            )}
          </DndContext>
        )}
      </div>
    </div>
  );
}

/** 只有正被瞄准的候选落点才有状态；不是候选的地方悬停也不画颜色。 */
function dropStateOf(
  valid: boolean | undefined,
  aimed: boolean,
): "valid" | "invalid" | undefined {
  if (!aimed || valid === undefined) return undefined;
  return valid ? "valid" : "invalid";
}

type FilterMenuProps = {
  testId: string;
  ariaLabel: string;
  label: string;
  active: boolean;
  icon: React.ReactNode;
  value: string;
  onValueChange: (value: string) => void;
  options: Array<{ id: number; label: string }>;
};

function FilterMenu(props: FilterMenuProps) {
  const { t } = useTranslation();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          data-testid={props.testId}
          aria-label={props.ariaLabel}
          className={cn(
            "inline-flex h-8 shrink-0 cursor-pointer items-center gap-1.5 rounded-md border px-2 text-xs outline-none transition-colors hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-ring/50",
            props.active
              ? "border-primary bg-primary-soft text-primary-text"
              : "border-border text-muted-foreground",
          )}
        >
          {props.icon}
          <span className="max-w-[160px] truncate">{props.label}</span>
          <ChevronDown className="size-3 opacity-70" aria-hidden="true" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-64 overflow-auto">
        <DropdownMenuRadioGroup
          value={props.value}
          onValueChange={props.onValueChange}
        >
          <DropdownMenuRadioItem
            value="0"
            data-testid={`${props.testId}-option-0`}
          >
            {t("common.all")}
          </DropdownMenuRadioItem>
          {props.options.map((option) => (
            <DropdownMenuRadioItem
              key={option.id}
              value={String(option.id)}
              data-testid={`${props.testId}-option-${option.id}`}
            >
              {option.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function InsertLine({
  id,
  target,
  valid,
  current,
}: {
  id: string;
  target: OrgDropTarget;
  valid?: boolean;
  current: boolean;
}) {
  // 解构而不是留着 `drop.` 前缀：把 setNodeRef 挂到 ref= 上会让整个返回对象被
  // react-hooks/refs 视作 ref，之后连 isOver 都读不得。
  const { isOver, setNodeRef } = useDroppable({ id, data: target });
  const dropState = dropStateOf(valid, current || isOver);
  return (
    <div
      ref={setNodeRef}
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

function GroupHeader({
  group,
  target,
  valid,
  current,
  selected,
  onSelect,
  onDragKeyDown,
}: {
  group: OrgIndexGroup;
  target?: OrgDropTarget;
  valid?: boolean;
  current: boolean;
  selected: boolean;
  onSelect: (sel: OrgSelection) => void;
  onDragKeyDown: (
    event: React.KeyboardEvent<HTMLElement>,
    subject: OrgDragSubject,
  ) => void;
}) {
  const { t } = useTranslation();
  const department = group.department;
  const subject: OrgDragSubject = { kind: "department", id: department.id };
  const { attributes, listeners, setActivatorNodeRef } = useDraggable({
    id: `dept-${department.id}`,
    data: subject,
  });
  const { isOver, setNodeRef } = useDroppable({
    id: `dept-drop-${department.id}`,
    data: { kind: "department", departmentId: department.id },
  });
  const dropState = dropStateOf(valid, current || isOver);
  const accent = safeAgentColor(department.accentColor);
  const iconNode = React.createElement(iconForKey(department.icon), {
    className: "size-3.5",
    "aria-hidden": true,
  });

  return (
    <div
      ref={setNodeRef}
      data-testid={`org-group-${department.id}`}
      data-slot="org-group-header"
      data-department-id={department.id}
      data-depth={group.depth}
      data-drop-kind={target ? "department" : undefined}
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
      <button
        type="button"
        ref={setActivatorNodeRef}
        data-testid={`org-group-handle-${department.id}`}
        aria-label={t("org.index.drag.departmentHandle", {
          name: department.name,
        })}
        {...attributes}
        {...listeners}
        onKeyDown={(event) => onDragKeyDown(event, subject)}
        className="shrink-0 cursor-grab touch-none select-none rounded-sm text-subtle-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
      >
        <GripVertical className="size-3.5" aria-hidden="true" />
      </button>
      <button
        type="button"
        data-testid={`org-group-select-${department.id}`}
        onClick={() => onSelect({ kind: "department", id: department.id })}
        className="flex min-w-0 flex-1 items-center gap-2 text-left"
      >
        <span
          className={cn(
            "inline-flex size-5 shrink-0 items-center justify-center rounded-md text-white",
            agentColorClassNames[accent],
          )}
        >
          {iconNode}
        </span>
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
            count: department.memberCount,
          })}
        </span>
      </button>
    </div>
  );
}

function AgentRow({
  row,
  indent,
  target,
  valid,
  current,
  selected,
  onSelect,
  onDragKeyDown,
}: {
  row: OrgIndexRow;
  indent: number;
  target?: OrgDropTarget;
  valid?: boolean;
  current: boolean;
  selected: boolean;
  onSelect: (sel: OrgSelection) => void;
  onDragKeyDown: (
    event: React.KeyboardEvent<HTMLElement>,
    subject: OrgDragSubject,
  ) => void;
}) {
  const { t } = useTranslation();
  const agent = row.agent;
  const isSystem = agent.systemBadge === "DEFAULT";
  const subject: OrgDragSubject = { kind: "agent", id: agent.id };
  const { attributes, listeners, setActivatorNodeRef } = useDraggable({
    id: `agent-${agent.id}`,
    data: subject,
    disabled: isSystem,
  });
  const { isOver, setNodeRef } = useDroppable({
    id: `agent-drop-${agent.id}`,
    data: { kind: "agent", agentId: agent.id },
  });

  const dropState = dropStateOf(valid, current || isOver);
  return (
    <div
      ref={setNodeRef}
      data-testid={`org-row-${agent.id}`}
      data-slot="org-index-row"
      data-agent-id={agent.id}
      // 下属与主管一律同级：缩进只由所在部门决定，从属关系写在行内。
      data-indent={indent}
      style={{ paddingLeft: 17 + indent * 16 }}
      data-drop-kind={target ? "agent" : undefined}
      data-drop-state={dropState}
      aria-current={selected ? "true" : undefined}
      className={cn(
        "flex items-center gap-2 border-b border-l-[3px] border-border/60 py-2 pr-5 transition-colors",
        selected ? "border-l-primary bg-primary-soft" : "border-l-transparent",
        dropState === "valid" && "ring-2 ring-primary/60",
        dropState === "invalid" && "ring-2 ring-destructive",
      )}
    >
      {isSystem ? (
        <span className="size-3.5 shrink-0" aria-hidden="true" />
      ) : (
        <button
          type="button"
          ref={setActivatorNodeRef}
          data-testid={`org-row-handle-${agent.id}`}
          aria-label={t("org.index.drag.agentHandle", { name: agent.name })}
          {...attributes}
          {...listeners}
          onKeyDown={(event) => onDragKeyDown(event, subject)}
          className="shrink-0 cursor-grab touch-none select-none rounded-sm text-subtle-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
        >
          <GripVertical className="size-3.5" aria-hidden="true" />
        </button>
      )}
      <button
        type="button"
        data-testid={`org-row-select-${agent.id}`}
        onClick={() => onSelect({ kind: "agent", id: agent.id })}
        className="flex min-w-0 flex-1 items-center gap-3 text-left"
      >
        <AgentAvatar
          name={agent.name}
          color={safeAgentColor(agent.avatarColor)}
          size="md"
          avatarDataUrl={agent.avatarDataUrl}
          avatarIcon={agent.avatarIcon}
          className="size-8 shrink-0 rounded-lg"
        />
        <span className="flex min-w-0 flex-col">
          <span className="flex min-w-0 items-center gap-1.5">
            <span
              className={cn(
                "truncate text-sm font-semibold",
                selected && "text-primary-text",
              )}
            >
              {agent.name}
            </span>
            {row.reportsToName && (
              <span
                data-slot="org-row-reports-to"
                className="shrink-0 font-mono text-2xs text-muted-foreground"
              >
                {t("org.index.reportsToInline", { name: row.reportsToName })}
              </span>
            )}
          </span>
          {agent.description && (
            <span className="truncate text-2xs text-muted-foreground">
              {agent.description}
            </span>
          )}
        </span>
        {agent.backend?.name && (
          <span className="shrink-0 rounded border border-transparent bg-secondary px-2 py-0.5 font-mono text-2xs">
            {agent.backend.name}
          </span>
        )}
      </button>
    </div>
  );
}

function EmptyDepartments({
  onCreateDepartment,
  onCreateAgent,
}: {
  onCreateDepartment: () => void;
  onCreateAgent?: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div
      className="m-5 flex flex-col items-center gap-2 rounded-lg border border-dashed p-6 text-center"
      data-slot="org-index-empty-departments"
    >
      <span className="text-sm font-semibold">
        {t("org.index.emptyDepartments.title")}
      </span>
      <span className="text-2xs text-muted-foreground">
        {t("org.index.emptyDepartments.description")}
      </span>
      <div className="mt-1 flex items-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-7 px-2.5 text-2xs"
          onClick={onCreateDepartment}
        >
          <FolderPlus className="size-3" aria-hidden="true" />
          {t("org.index.emptyDepartments.newDepartment")}
        </Button>
        {onCreateAgent ? (
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-7 px-2.5 text-2xs"
            onClick={onCreateAgent}
          >
            <Plus className="size-3" aria-hidden="true" />
            {t("org.index.emptyDepartments.addAgent")}
          </Button>
        ) : null}
      </div>
    </div>
  );
}
