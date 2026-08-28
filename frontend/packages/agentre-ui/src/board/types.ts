import type * as React from "react";

import type { ProjectGlyphInfo } from "../session-index/project-glyph";

/**
 * 看板的**宿主中立**数据契约。
 *
 * 桌面端（Wails 绑定）与 agentre-server（HTTP）取数的路子完全不同，但画出来的
 * 那块板必须是同一块。所以这里只描述「画什么」：视图模型从 props 进，动作经
 * `BoardPorts` 出，包里既没有取数、也没有路由、更没有全局状态。
 */

/** 四列固定，与 `issue_entity` 的 `Stage*` 常量一一对应；不新增列也不让用户改列。 */
export const BOARD_STAGES = ["todo", "doing", "review", "done"] as const;

export type BoardStage = (typeof BOARD_STAGES)[number];

/**
 * 8 档色调。取值与 `issue_entity.Label.Check` 的 `allowedTones` 同一份 —— 名字是
 * **颜色**不是用途：标签一旦可自建，「一个叫『前端』的标签，色调叫 bug」就说不通。
 * 线上取值用蛇形（`red_solid`），与 `labels.tone` 那一列逐字相同；五档旧值按
 * 规格「数据与迁移」的 1:1 映射迁过来（`docs`→`gray`、`refactor`→`steel`…）。
 */
export const ISSUE_TONES = [
  "gray",
  "red",
  "red_solid",
  "amber",
  "green",
  "steel",
  "blue",
  "violet",
] as const;

export type IssueTone = (typeof ISSUE_TONES)[number];

export interface BoardLabelView {
  id: number;
  name: string;
  tone: IssueTone;
}

/**
 * 卡片上的项目字形。**是否**画它是范围判据（「当前范围里是否不止一个项目」），
 * 那是宿主/查询层的事；这里只收结果。`nested` = 该任务属于当前范围的子项目，
 * 画一枚「↳ + 字形」把层级说出来。
 */
export interface BoardCardProject extends ProjectGlyphInfo {
  /** 宿主的图标注册表解出来的那枚图标；不给就退回项目名首字。 */
  glyph?: React.ReactNode;
  nested?: boolean;
}

export interface BoardCardView {
  id: number;
  stage: BoardStage;
  title: string;
  labels?: BoardLabelView[];
  /** 描述非空 —— 元信息行上一枚图标，不把描述正文搬到卡片上。 */
  hasDescription?: boolean;
  /** 描述正文。只在关键词命中它时用：卡片多出一行不超过一行的摘录。 */
  description?: string;
  /** 毫秒时间戳；相对时间由包内 `formatRelativeTime` 算，两端一份实现。 */
  updatedAt?: number;
  project?: BoardCardProject | null;
}

export interface BoardColumnView {
  /** 这一列此刻要画的卡片（已按 position 排好）。 */
  cards: BoardCardView[];
  /** 全部数量：**不随筛选缩水**，筛选态下当「命中 / 全部」的分母。 */
  total: number;
  /** 命中数；只有筛选态才有意义。缺省时按 `cards.length` 算。 */
  matched?: number;
}

export interface BoardViewModel {
  /** 缺哪一列就画成空列 —— 四列固定这件事由呈现件保证，不赌宿主每次都给全。 */
  columns: Partial<Record<BoardStage, BoardColumnView>>;
  /** 搜索或筛选生效中：列头计数变「命中 / 全部」，已完成列自动展开。 */
  filtering: boolean;
  /** 此刻生效的关键词：命中片段在卡片上高亮，命中落在描述里时多摘一行。 */
  keyword?: string;
  /** 首屏取数中：四列骨架卡片就地占位，不是屏幕中央一个转圈。 */
  loading?: boolean;
}

/**
 * 动作出口。三个必给的是卡片自己的动作；两个可选的是空态那两条出路 —— 它们由
 * 页面壳（筛选面板 / 任务表单）接上，看板单独渲染时不给也画得出来。
 */
export interface BoardPorts {
  onMove: (cardId: number, toStage: BoardStage) => void;
  onEdit: (cardId: number) => void;
  onDelete: (cardId: number) => void;
  /** 新建一条任务。带上列名 = 从那一列的「+」进来，表单的阶段预置为它。 */
  onCreateTask?: (stage?: BoardStage) => void;
  onClearFilters?: () => void;
}

/**
 * 拖拽的**手势**属于宿主：包里没有 dnd-kit（agentre-server 那侧的传感器与桌面端
 * 未必同一套），所以呈现件收的是**已经算好的视觉态**，与 `org/drag-binding.ts`
 * 立下的那条缝同形。
 */
export type BoardCardDragState =
  /** 原位残影：卡片被拿走了，位置还留着。 */
  | "ghost"
  /** 被拖起的那一张：抬起 + 微旋。 */
  | "lifted";

export interface BoardCardDragBinding {
  setNodeRef?: (node: HTMLElement | null) => void;
  attributes?: React.HTMLAttributes<HTMLElement>;
  listeners?: React.DOMAttributes<HTMLElement>;
  style?: React.CSSProperties;
  state?: BoardCardDragState;
}

/** `undefined` = 现在没瞄这一列，什么都不画。 */
export type BoardColumnDropState = "over" | undefined;

export interface BoardColumnDragBinding {
  setNodeRef?: (node: HTMLElement | null) => void;
  dropState?: BoardColumnDropState;
}

export interface BoardDragBindings {
  card?: (cardId: number) => BoardCardDragBinding | undefined;
  column?: (stage: BoardStage) => BoardColumnDragBinding | undefined;
}
