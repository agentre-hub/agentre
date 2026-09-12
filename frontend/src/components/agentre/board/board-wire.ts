// board-wire.ts —— 看板宿主那一半的**纯翻译层**。
//
// 共享包描述「要什么」（`BoardQuery`）与「画什么」（`BoardViewModel`），Wails 绑定
// 说的是 `IssueListRequest` / `IssueListResponse`。两者之间的换算全在这里，没有
// 一行 React、没有一次 IPC —— 时间预设算成几号几点、色调怎么落到卡片上这类判断，
// 摆在组件里就只能靠渲染整块看板来测。

import {
  BOARD_STAGES,
  activeConditions,
  buildScopeRows,
  type BoardCardProject,
  type BoardCardView,
  type BoardColumnView,
  type BoardQuery,
  type BoardStage,
  type IssueTone,
  type ProjectScope,
  type TaskFormValue,
  type TimeRange,
} from "@agentre-hub/agentre-ui";

import type { app } from "../../../../wailsjs/go/models";

const DAY_MS = 86_400_000;

/** 「已完成保留多久」换成天数；`all` = 不设窗口。 */
const DONE_RETENTION_DAYS: Record<string, number> = {
  "30d": 30,
  "90d": 90,
  all: 0,
};

/**
 * 一段时间条件的起止毫秒。0 = 那一端不限 —— 后端按 `> 0` 才拼进 SQL，所以
 * 「不限」与「1970-01-01」在线上是同一件事，这里不必再造一个哨兵。
 */
export function timeRangeToEpoch(
  range: TimeRange,
  nowMs: number,
): { from: number; to: number } {
  switch (range.preset) {
    case "today": {
      const midnight = new Date(nowMs);
      midnight.setHours(0, 0, 0, 0);
      return { from: midnight.getTime(), to: 0 };
    }
    case "7d":
      return { from: nowMs - 7 * DAY_MS, to: 0 };
    case "30d":
      return { from: nowMs - 30 * DAY_MS, to: 0 };
    case "custom":
      return { from: range.from ?? 0, to: range.to ?? 0 };
    default:
      return { from: 0, to: 0 };
  }
}

/** 六个筛选条件 → 一次 `IssueList` 的入参。 */
export function toIssueListRequest(
  query: BoardQuery,
  nowMs: number,
): app.IssueListRequest {
  const updated = timeRangeToEpoch(query.updated, nowMs);
  const created = timeRangeToEpoch(query.created, nowMs);

  return {
    scope: query.scope.kind,
    projectID: query.scope.kind === "project" ? query.scope.projectId : 0,
    keyword: query.keyword.trim(),
    labelIDs: query.labelIds,
    labelMatchAll: query.labelMatch === "all",
    noLabel: query.noLabelOnly,
    updatedFrom: updated.from,
    updatedTo: updated.to,
    createdFrom: created.from,
    createdTo: created.to,
    doneWithinDays: DONE_RETENTION_DAYS[query.doneRetention] ?? 30,
    // 看板永远按列内位置排：拖拽落库的就是 position，换成「最近更新」会让刚拖过
    // 的那张卡自己跳走。
    sort: "position",
  };
}

/**
 * 列头是否要变成「命中 / 全部」。
 *
 * **项目范围不算**：`stageTotals` 本来就只吃项目范围（`app.IssueListResponse` 的
 * 那条注释），所以只切了范围时分子分母恒等，画出来是一句「3 / 3」的废话。
 */
export function isFiltering(query: BoardQuery): boolean {
  return activeConditions(query).some((key) => key !== "scope");
}

/** 一条任务要画成卡片时，它的项目那一格由宿主解（图标注册表在宿主手里）。 */
export type BoardCardProjectResolver = (
  projectID: number,
) => BoardCardProject | null;

function toBoardCard(
  issue: app.IssueItem,
  projectOf: BoardCardProjectResolver,
): BoardCardView {
  return {
    id: issue.id,
    stage: (issue.stage || "todo") as BoardStage,
    title: issue.title,
    labels: (issue.labels ?? []).map((label) => ({
      id: label.id,
      name: label.name,
      tone: label.tone as IssueTone,
    })),
    hasDescription: Boolean(issue.body),
    // 正文只在关键词命中它时露一行摘录（呈现件自己判），卡片上从不铺开。
    description: issue.body,
    updatedAt: issue.updatetime,
    project: issue.projectID ? projectOf(issue.projectID) : null,
  };
}

/**
 * 一次响应摊成四列。缺的列给空列而不是不给 —— 四列固定这件事在呈现件那边也兜着，
 * 但让宿主先说全，`total` 才有地方落。
 */
export function toBoardColumns(
  response: app.IssueListResponse,
  projectOf: BoardCardProjectResolver,
): Partial<Record<BoardStage, BoardColumnView>> {
  const byStage = new Map<BoardStage, BoardCardView[]>();
  for (const stage of BOARD_STAGES) byStage.set(stage, []);

  const issues = [...(response.issues ?? [])].sort(
    (a, b) => a.position - b.position,
  );
  for (const issue of issues) {
    const card = toBoardCard(issue, projectOf);
    (byStage.get(card.stage) ?? byStage.get("todo"))?.push(card);
  }

  const columns: Partial<Record<BoardStage, BoardColumnView>> = {};
  for (const stage of BOARD_STAGES) {
    const cards = byStage.get(stage) ?? [];
    columns[stage] = {
      cards,
      total: response.stageTotals?.[stage] ?? cards.length,
      matched: response.stageCounts?.[stage] ?? cards.length,
    };
  }
  return columns;
}

/** 搜索框右侧那个命中数：四列命中之和。 */
export function matchedTotal(stageCounts: Record<string, number>): number {
  return Object.values(stageCounts ?? {}).reduce(
    (sum, count) => sum + (count ?? 0),
    0,
  );
}

/** 项目选择器每一项右侧的子树未完成数；没有那一行就是 0。 */
export function projectCountOf(
  counts: app.ProjectIssueCount[] | undefined,
  projectID: number,
): number {
  return (counts ?? []).find((row) => row.projectID === projectID)?.count ?? 0;
}

/** 一条任务摊回表单要编辑的那些字段（含三个执行字段）。 */
export function toTaskFormValue(issue: app.IssueItem): TaskFormValue {
  return {
    id: issue.id,
    title: issue.title,
    description: issue.body,
    stage: (issue.stage || "todo") as BoardStage,
    // 本地表存的是自增外键，0 = 没有；表单说的是 `null`，两者别混着传。
    projectId: issue.projectID || null,
    labelIds: (issue.labels ?? []).map((label) => label.id),
    assigneeAgentId: issue.assigneeAgentID || null,
    agentBackendId: issue.agentBackendID || null,
    llmProviderKey: issue.llmProviderKey,
    llmModelKey: issue.llmModelKey,
    updatedAt: issue.updatetime,
  };
}

/**
 * 卡片上画不画项目字形：判据是**当前范围里是否不止一个项目**。
 *
 * 范围是单个没有子项目的项目时，每张卡都画同一枚字形等于一句重复的废话；「未归属」
 * 里根本没有项目。父子关系从 `depth` 栈推（与 `buildScopeRows` 同一份），宿主不必
 * 再拼一遍树。
 */
export function scopeShowsGlyphs(
  scope: ProjectScope,
  projects: { id: number; depth?: number }[],
): boolean {
  if (scope.kind === "all") return true;
  if (scope.kind === "unassigned") return false;
  const rows = buildScopeRows(
    projects.map((project) => ({
      id: project.id,
      depth: project.depth ?? 0,
      name: "",
    })),
  );
  const row = rows.find((entry) => entry.node.id === scope.projectId);
  return (row?.descendantCount ?? 0) > 0;
}
