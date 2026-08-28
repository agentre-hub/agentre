import type * as React from "react";

import type { BoardStage, IssueTone } from "./types";

/**
 * 看板**查询面**的宿主中立契约：范围、六个筛选条件、标签增删改的意图。
 *
 * 与 `types.ts`（画什么）分开：那一份描述已经算好的视图，这一份描述**要什么**。
 * 桌面端把它翻成 Wails 的 `ListIssues` 入参，agentre-server 翻成 query string，
 * 两端翻的是同一个结构。
 */

/** 当前看板收窄到哪一段项目树。`project` 含**整棵子树**，不只是它自己。 */
export type ProjectScope =
  | { kind: "all" }
  | { kind: "unassigned" }
  | { kind: "project"; projectId: number };

export const ALL_PROJECTS_SCOPE: ProjectScope = { kind: "all" };

/**
 * 范围选择器里的一行项目。**扁平前序**列表 + `depth`（与 `ProjectFlat` 同形）——
 * 父子关系由 depth 栈推出来，宿主不必再拼一遍树。
 */
export interface ScopeProjectNode {
  id: number;
  name: string;
  /** 0 = 根。缩进与引导线都按它画。 */
  depth: number;
  /** 颜色 token，如 "agent-1"；交给 `ProjectGlyph`。 */
  color?: string;
  /** 宿主图标注册表解出来的那枚图标；不给就退回项目名首字。 */
  glyph?: React.ReactNode;
  /**
   * 该项目**及其子树**里未完成的任务数。**不随筛选缩水** —— 打开选择器是为了判断
   * 该切到哪，跟着当前筛选走就失去了这个用途，所以宿主按未筛选口径算好再喂进来。
   */
  unfinished?: number;
}

/** 标签「任意一个」还是「全部满足」。 */
export type LabelMatchMode = "any" | "all";

export type TimePreset = "any" | "today" | "7d" | "30d" | "custom";

/** 一段时间条件。`custom` 时 `from` / `to` 是毫秒 epoch，缺一端 = 那一端不限。 */
export interface TimeRange {
  preset: TimePreset;
  from?: number;
  to?: number;
}

export const ANY_TIME: TimeRange = { preset: "any" };

/**
 * 「已完成保留多久」。它替掉「默认只显示最近 N 个」那种写死的数字，成为一个能说
 * 出口、也能被摘掉的条件；`30d` 是默认档，所以只有另外两档才算「生效」。
 */
export type DoneRetention = "30d" | "90d" | "all";

export const DEFAULT_DONE_RETENTION: DoneRetention = "30d";

/** 六个条件的完整状态。范围与关键词有各自的触发器，但它们同样是条件。 */
export interface BoardQuery {
  keyword: string;
  scope: ProjectScope;
  labelIds: number[];
  labelMatch: LabelMatchMode;
  /** 只看没有任何标签的任务；与 `labelIds` 互斥。 */
  noLabelOnly: boolean;
  updated: TimeRange;
  created: TimeRange;
  doneRetention: DoneRetention;
}

export const EMPTY_BOARD_QUERY: BoardQuery = {
  keyword: "",
  scope: ALL_PROJECTS_SCOPE,
  labelIds: [],
  labelMatch: "any",
  noLabelOnly: false,
  updated: ANY_TIME,
  created: ANY_TIME,
  doneRetention: DEFAULT_DONE_RETENTION,
};

/** 标签管理列表里的一行：色调 + 被多少个任务用着。 */
export interface LabelUsageView {
  id: number;
  name: string;
  tone: IssueTone;
  /** 被多少个任务使用；删除前的爆炸半径就是它。 */
  usageCount: number;
}

/**
 * 标签的写意图。`update` 一次带齐名字与色调，与 `issue_svc.UpdateLabel` 同形 ——
 * 拆成 rename / retone 两个动作会让「改完名字再改色」变成两次往返两次失败面。
 */
export type LabelMutation =
  | { kind: "create"; name: string; tone: IssueTone }
  | { kind: "update"; id: number; name: string; tone: IssueTone }
  | { kind: "delete"; id: number };

export interface TaskFormValue {
  /** 有 id = 编辑态。 */
  id?: number;
  title: string;
  description: string;
  stage: BoardStage;
  /** `null` = 未归属。 */
  projectId: number | null;
  labelIds: number[];
  /** 执行归属三件套。本轮没有任何路径读它们，只是随任务存下来（决策 9）。 */
  assigneeAgentId: number | null;
  agentBackendId: number | null;
  llmProviderKey: string;
  llmModelKey: string;
  /** 编辑态头部右侧那个时间；毫秒 epoch。 */
  updatedAt?: number;
}

/**
 * 查询面的动作出口。三件事各归其位：改查询、存任务、动标签。取数、落库与路由都在
 * 宿主，包里只发意图。
 */
export interface BoardQueryPorts {
  onQueryChange: (next: BoardQuery) => void;
  onSave: (value: TaskFormValue) => Promise<void> | void;
  onLabelMutate: (mutation: LabelMutation) => Promise<void> | void;
}
