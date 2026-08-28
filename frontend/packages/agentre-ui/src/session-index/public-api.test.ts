import { describe, expect, it } from "vitest";

import * as pkg from "../index";

/**
 * `src/index.ts` 是这个包**唯一**的门：package.json 的 exports 只把 `.` 映射到
 * `dist/index.js`，没从 barrel 里放出去的东西，消费方（agentre-server 经 git 依赖、
 * 桌面端经 workspace）根本看不见 —— 而那个失败要等到对面 import 时才炸。
 *
 * 索引这一组是跨仓契约（规格 2026-08-18「共享包承载什么」），所以钉在这里。
 */
describe("会话索引的对外契约", () => {
  it("投影与呈现件都从 barrel 出得去", () => {
    const missing = [
      "buildAxisGroups",
      "AxisPicker",
      "IndexGroupHeader",
      "groupActionRevealClassName",
      "groupActionRevealTouchClassName",
      "groupGlyphClassName",
      "ProjectGroupHeader",
      "AgentGroupHeader",
      "MachineGroupHeader",
      "FreeGroupHeader",
      "OwnSessionsHeader",
      "ProjectGlyph",
      "RowLeadingSlot",
      "RowSecondaryLine",
      "UNASSIGNED_PROJECT_KEY",
      "UNNAMED_AGENT_KEY",
      "UNKNOWN_MACHINE_KEY",
    ].filter((name) => !(name in pkg));

    expect(missing).toEqual([]);
  });
});
