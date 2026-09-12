import { describe, expect, it } from "vitest";

import * as pkg from "../index";

/**
 * `src/index.ts` 是这个包**唯一**的门：package.json 的 exports 只把 `.` 映射到
 * `dist/index.js`，没从 barrel 里放出去的东西，消费方（agentre-server 经 git 依赖、
 * 桌面端经 workspace）根本看不见 —— 而那个失败要等到对面 import 时才炸。
 *
 * 组织面这一组是跨仓契约（规格 2026-08-18「server 端的组织管理面」要求两端
 * 「同一批共享组件」），所以钉在这里。
 *
 * 0.1.0 收窄：`EMPTY_ORG_FILTERS` / `buildOrgToolList` / `ORG_APPROVAL_TOOLS`
 * 已从 barrel 摘除（实现仍在各自模块，只是不再是对外契约）。
 */
describe("组织索引与详情的对外契约", () => {
  it("投影、落点判据与呈现件都从 barrel 出得去", () => {
    const missing = [
      // 索引投影 + 落点判据（纯函数）
      "buildOrgIndex",
      "buildOrgReportsToOptions",
      "buildOrgReportToMap",
      "resolveOrgReportTo",
      "computeOrgReorder",
      "isValidOrgDrop",
      "isValidOrgDepartmentDrop",
      "resolveOrgDrop",
      // 索引呈现件
      "OrgAgentRow",
      "OrgGroupHeader",
      "OrgInsertLine",
      // 详情呈现件
      "OrgPlacementField",
      "OrgToolList",
      "OrgExecTargetRow",
      "orgExecTargetReasonLabel",
    ].filter((name) => !(name in pkg));

    expect(missing).toEqual([]);
  });
});
