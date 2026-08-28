import { describe, expect, it } from "vitest";

import * as pkg from "../index";

/**
 * `src/index.ts` 是这个包**唯一**的门：没从 barrel 里放出去的东西，消费方
 * （agentre-server 经 git 依赖、桌面端经 workspace）根本看不见。
 *
 * 看板这一族是跨仓契约（规格 2026-08-27「两端如何分工」：筛选面板、卡片、
 * 空态与骨架都是两端同一批共享呈现件），所以钉在这里。
 */
describe("看板呈现件的对外契约", () => {
  it("呈现件、色调表与阶段常量都从 barrel 出得去", () => {
    const missing = [
      // 外壳与呈现件
      "IssueBoard",
      "BoardColumn",
      "BoardCard",
      "BoardCardLabels",
      "BoardCardMenu",
      "BoardEmptyState",
      "BoardSkeleton",
      // 派生
      "useBoardColumns",
      // 色调表与阶段
      "toneClass",
      "toneClassNames",
      "ISSUE_TONES",
      "BOARD_STAGES",
      "DONE_VISIBLE_LIMIT",
      // 范围选择器
      "ProjectScopePicker",
      "ProjectScopeTrigger",
      "ProjectScopePopover",
      "useProjectScope",
      "buildScopeRows",
      "filterScopeRows",
      "splitMatch",
      // 搜索与筛选
      "BoardFilterBar",
      "BoardFilterPanel",
      "BoardFilterChips",
      "BoardSearchBox",
      "useBoardQuery",
      "BOARD_SEARCH_DEBOUNCE_MS",
      "activeConditions",
      "activeConditionCount",
      "buildFilterChips",
      "dropChip",
      "EMPTY_BOARD_QUERY",
      "ALL_PROJECTS_SCOPE",
      "ANY_TIME",
      "DEFAULT_DONE_RETENTION",
      // 任务表单壳与三颗执行 pill（后两颗只有端口，实现留在宿主）
      "TaskFormShell",
      "TaskStagePill",
      "TaskProjectPill",
      "TaskLabelChips",
      "TaskExecPills",
      "TASK_PILL_CLASS",
      "initialTaskFormValue",
      "useTaskForm",
      // 标签管理
      "LabelManagerPanel",
      "LabelPalette",
      "useLabelManager",
    ].filter((name) => !(name in pkg));

    expect(missing).toEqual([]);
  });
});
