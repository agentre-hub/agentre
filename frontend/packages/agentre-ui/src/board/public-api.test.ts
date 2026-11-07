import { describe, expect, it } from "vitest";

import * as pkg from "../index";

/**
 * `src/index.ts` 是这个包**唯一**的门：没从 barrel 里放出去的东西，消费方
 * （agentre-server 经 git 依赖、桌面端经 workspace）根本看不见。
 *
 * 看板这一族是跨仓契约（规格 2026-08-27「两端如何分工」：筛选面板、卡片、
 * 空态与骨架都是两端同一批共享呈现件），所以钉在这里。
 *
 * 0.1.0 收窄：下面这份清单只保留两端真正消费的符号；其余呈现件、派生与
 * 色调表已从 barrel 摘除（实现仍在各自模块，只是不再是对外契约）。
 */
describe("看板呈现件的对外契约", () => {
  it("呈现件、阶段常量与查询面都从 barrel 出得去", () => {
    const missing = [
      // 外壳
      "IssueBoard",
      // 阶段
      "BOARD_STAGES",
      // 范围选择器
      "ProjectScopePicker",
      "buildScopeRows",
      // 搜索与筛选
      "BoardFilterBar",
      "activeConditions",
      "EMPTY_BOARD_QUERY",
      "ALL_PROJECTS_SCOPE",
      // 任务表单壳
      "TaskFormShell",
      "initialTaskFormValue",
      // 标签管理
      "LabelManagerPanel",
    ].filter((name) => !(name in pkg));

    expect(missing).toEqual([]);
  });
});
