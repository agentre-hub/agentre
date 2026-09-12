// 组织架构页左侧的索引树:部门分组 + 组内 agent,可拖拽排序与跨组移动。
//
// 树的那些零件在 org/ 下,与既有的 org-detail-* 同名风格一致:
// org-index-filter / org-index-group-header / org-index-agent-row / org-index-empty。

import * as React from "react";
import { DndContext } from "@dnd-kit/core";
import { CornerDownRight, Search, Server, X } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  Button,
  Input,
  buildOrgIndex,
  buildOrgReportsToOptions,
  type OrgIndexGroup,
} from "@agentre-hub/agentre-ui";

import type { agent_backend_svc } from "../../../../wailsjs/go/models";
import { type OrgAgent, type OrgDepartment, type OrgSelection } from "./types";
import { type DragState } from "./org-index-units";
import { useOrgIndexDrag } from "./use-org-index-drag";

import { AgentRow } from "./org-index-agent-row";
import { EmptyDepartments } from "./org-index-empty";
import { FilterEntry } from "./org-index-filter";
import { GroupHeader, InsertLine } from "./org-index-group-header";

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
