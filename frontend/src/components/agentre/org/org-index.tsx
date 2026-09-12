import * as React from "react";
import { DndContext, useDraggable, useDroppable } from "@dnd-kit/core";
import {
  ChevronDown,
  CornerDownRight,
  FolderPlus,
  Plus,
  Search,
  Server,
  SlidersHorizontal,
  X,
} from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Input,
  OrgAgentRow,
  OrgGroupHeader,
  OrgInsertLine,
  buildOrgIndex,
  buildOrgReportsToOptions,
  type OrgDragSubject,
  type OrgDropTarget,
  type OrgIndexGroup,
  type OrgIndexRow,
} from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

import type { agent_backend_svc } from "../../../../wailsjs/go/models";
import { AgentAvatar } from "../primitives";
import {
  safeAgentColor,
  type OrgAgent,
  type OrgDepartment,
  type OrgSelection,
} from "./types";
import { type DragState } from "./org-index-units";
import { useOrgIndexDrag } from "./use-org-index-drag";

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
  /**
   * 没有任何可挂载节点时不给这条出路（新 Agent 挂不上去）。
   *
   * 带部门 id：组头上的 ＋ 说的是「往**这个**部门加」，空态那个说的是「随便加一个」
   * （给 0）。调用方忽略这个参数也合法，只是新建对话框不会预选部门。
   */
  onCreateAgent?: (departmentId: number) => void;
};

export function OrgIndex(props: OrgIndexProps) {
  const { t } = useTranslation();
  const { agents, departments, backends } = props;

  const [search, setSearch] = React.useState("");
  const [backendId, setBackendId] = React.useState(0);
  const [reportsToId, setReportsToId] = React.useState(0);
  const [drag, setDrag] = React.useState<DragState | null>(null);
  const [announcement, setAnnouncement] = React.useState("");
  // 收起哪些部门归宿主（共享包的组头只画三角、发回调）。默认全展开：一进来就看见
  // 全貌，收起是用户自己做的减法。
  const [collapsed, setCollapsed] = React.useState<ReadonlySet<number>>(
    () => new Set<number>(),
  );

  const filters = React.useMemo(
    () => ({ search, backendId, reportsToId }),
    [search, backendId, reportsToId],
  );
  // 「一档执行目标都没有」是宿主算的，不由包从 backend 缺席反推（见共享包
  // types.ts 的 noExecTarget）。LoadOrg 每个 Agent 都带 execTargets（空也是 `[]`），
  // 所以这里只在**真的收到了一个空列表**时才说没有。
  const indexAgents = React.useMemo(
    () =>
      agents.map((a) => ({
        ...a,
        // 包的 OrgAgentModel.agentBackendId 是「按后端筛选」那一维的视图契约，由
        // 宿主喂。桌面端的真源是 execTargets，①（sort_order 最小的那一档）就是
        // 这一维要的那个后端。
        agentBackendId: a.execTargets?.[0]?.agentBackendId ?? 0,
        noExecTarget:
          Array.isArray(a.execTargets) && a.execTargets.length === 0,
      })),
    [agents],
  );
  const model = React.useMemo(
    () => buildOrgIndex({ agents: indexAgents, departments, filters }),
    [indexAgents, departments, filters],
  );
  // 收起一个部门连它的子部门一起收走：组是 DFS 前序 + depth，遇到收起的那一个就把
  // 后面所有更深的组跳掉 —— 只收一层会让子部门浮在收起的父部门下面。
  const visibleGroups = React.useMemo(() => {
    const out: OrgIndexGroup[] = [];
    let hiddenBelow = -1;
    for (const group of model.groups) {
      if (hiddenBelow >= 0 && group.depth > hiddenBelow) continue;
      hiddenBelow = -1;
      out.push(group);
      if (collapsed.has(group.department.id)) hiddenBelow = group.depth;
    }
    return out.map((group) =>
      collapsed.has(group.department.id) ? { ...group, rows: [] } : group,
    );
  }, [model.groups, collapsed]);
  const toggleCollapsed = React.useCallback((departmentId: number) => {
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (!next.delete(departmentId)) next.add(departmentId);
      return next;
    });
  }, []);
  const {
    units,
    sensors,
    handleDragStart,
    handleDragEnd,
    handleDragKeyDown,
    currentUnitIndex,
    validityOf,
  } = useOrgIndexDrag(props, {
    topRows: model.topRows,
    groups: visibleGroups,
    drag,
    setDrag,
    setAnnouncement,
  });
  const reportsToOptions = React.useMemo(
    () => buildOrgReportsToOptions(agents, departments),
    [agents, departments],
  );

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

        <FilterEntry
          activeCount={filterChips.length}
          sections={[
            {
              key: "backend",
              heading: t("org.index.filters.backendAria"),
              icon: <Server className="size-3.5" aria-hidden="true" />,
              value: String(backendId),
              onValueChange: (value) => setBackendId(Number(value)),
              options: backends.map((b) => ({ id: b.id, label: b.name })),
            },
            {
              key: "reportsTo",
              heading: t("org.index.filters.reportsToAria"),
              icon: <CornerDownRight className="size-3.5" aria-hidden="true" />,
              value: String(reportsToId),
              onValueChange: (value) => setReportsToId(Number(value)),
              options: reportsToOptions.map((a) => ({
                id: a.id,
                label: a.name,
              })),
            },
          ]}
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

      {/* 行与组头是**内缩的圆角块**而不是通栏条，所以左右内缩由这一层给
          （mockup `.rows { padding: 2px 6px 8px }`）——包里的行只管自己那点内边距。 */}
      <div
        className="min-h-0 flex-1 overflow-y-auto px-1.5 pb-2 pt-0.5"
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
                    expanded={!collapsed.has(unit.group.department.id)}
                    onToggleExpanded={toggleCollapsed}
                    onCreateAgent={props.onCreateAgent}
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

// 筛选是**一个**入口，两维都收在里面（决策 12「筛选不常驻占位」明确否决了
// 「常驻两个下拉」：未筛选时它们既不说明用途也占掉一整行）。命中之后说话的是
// 下面那排可清除的 chip，不是顶栏上一直摆着的两个「后端：全部」。
type FilterSection = {
  key: string;
  heading: string;
  icon: React.ReactNode;
  value: string;
  onValueChange: (value: string) => void;
  options: Array<{ id: number; label: string }>;
};

function FilterEntry(props: {
  activeCount: number;
  sections: FilterSection[];
}) {
  const { t } = useTranslation();
  const active = props.activeCount > 0;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          data-testid="org-filter-entry"
          aria-label={t("org.index.filters.entryAria")}
          className={cn(
            "inline-flex h-8 shrink-0 cursor-pointer items-center gap-1.5 rounded-md border px-2 text-xs outline-none transition-colors hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-ring/50",
            active
              ? "border-primary bg-primary-soft text-primary-text"
              : "border-border text-muted-foreground",
          )}
        >
          <SlidersHorizontal className="size-3.5" aria-hidden="true" />
          <span>{t("org.index.filters.entry")}</span>
          {active ? (
            <span className="font-mono text-2xs">{props.activeCount}</span>
          ) : null}
          <ChevronDown className="size-3 opacity-70" aria-hidden="true" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-80 overflow-auto">
        {props.sections.map((section, index) => (
          <React.Fragment key={section.key}>
            {index > 0 ? <DropdownMenuSeparator /> : null}
            <DropdownMenuLabel className="flex items-center gap-1.5 text-muted-foreground">
              {section.icon}
              {section.heading}
            </DropdownMenuLabel>
            <DropdownMenuRadioGroup
              value={section.value}
              onValueChange={section.onValueChange}
            >
              <DropdownMenuRadioItem
                value="0"
                data-testid={`org-filter-${section.key}-option-0`}
              >
                {t("common.all")}
              </DropdownMenuRadioItem>
              {section.options.map((option) => (
                <DropdownMenuRadioItem
                  key={option.id}
                  value={String(option.id)}
                  data-testid={`org-filter-${section.key}-option-${option.id}`}
                >
                  {option.label}
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
          </React.Fragment>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/**
 * 三个呈现件都在共享包里（规格 2026-08-18「server 端的组织管理面」要两端同一批
 * 组件）。留在这一层的只有**装配**：dnd-kit 的 `useDraggable` / `useDroppable`、
 * 键盘那条候选落点链，以及桌面端自己的头像 / 图标注册表 —— 这些在 agentre-server
 * 那侧要么不存在（Wails 头像），要么形态不同（浏览器不拖拽）。
 */
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
  return (
    <OrgInsertLine
      id={id}
      dropRef={setNodeRef}
      dropState={dropStateOf(valid, current || isOver)}
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
  expanded,
  onToggleExpanded,
  onCreateAgent,
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
  expanded: boolean;
  onToggleExpanded: (departmentId: number) => void;
  onCreateAgent?: (departmentId: number) => void;
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
  return (
    <OrgGroupHeader
      group={group}
      selected={selected}
      onSelect={onSelect}
      droppable={Boolean(target)}
      dropState={dropStateOf(valid, current || isOver)}
      dropRef={setNodeRef}
      dragHandle={{
        ref: setActivatorNodeRef,
        attributes,
        listeners,
        onKeyDown: (event) => onDragKeyDown(event, subject),
      }}
      expanded={expanded}
      onToggleExpanded={() => onToggleExpanded(department.id)}
      actions={
        onCreateAgent ? (
          <button
            type="button"
            aria-label={t("org.index.addAgentToDepartment", {
              name: department.name,
            })}
            title={t("org.index.addAgentToDepartment", {
              name: department.name,
            })}
            onClick={() => onCreateAgent(department.id)}
            className="inline-flex size-5 shrink-0 cursor-pointer items-center justify-center rounded text-muted-foreground transition-colors hover:bg-accent hover:text-foreground motion-reduce:transition-none"
          >
            <Plus className="size-3" aria-hidden="true" />
          </button>
        ) : undefined
      }
    />
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

  return (
    <OrgAgentRow
      row={row}
      indent={indent}
      selected={selected}
      onSelect={onSelect}
      droppable={Boolean(target)}
      dropState={dropStateOf(valid, current || isOver)}
      dropRef={setNodeRef}
      // 系统 Agent 不可拖动：不给绑定，包里画的就是那个等宽占位。
      dragHandle={
        isSystem
          ? undefined
          : {
              ref: setActivatorNodeRef,
              attributes,
              listeners,
              onKeyDown: (event) => onDragKeyDown(event, subject),
            }
      }
      avatar={
        // 索引这一枚是 18px（mockup `.av`）：32px 会把行高从 ≈28 顶到 ≥48，一屏
        // 少装一半的 Agent。详情里那枚仍是大的，两处不是同一个用途。
        <AgentAvatar
          name={agent.name}
          color={safeAgentColor(agent.avatarColor ?? "")}
          size="sm"
          avatarDataUrl={agent.avatarDataUrl}
          avatarIcon={agent.avatarIcon}
          className="size-4.5 shrink-0 rounded-[5px] text-3xs"
        />
      }
    />
  );
}

function EmptyDepartments({
  onCreateDepartment,
  onCreateAgent,
}: {
  onCreateDepartment: () => void;
  onCreateAgent?: (departmentId: number) => void;
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
            onClick={() => onCreateAgent(0)}
          >
            <Plus className="size-3" aria-hidden="true" />
            {t("org.index.emptyDepartments.addAgent")}
          </Button>
        ) : null}
      </div>
    </div>
  );
}
