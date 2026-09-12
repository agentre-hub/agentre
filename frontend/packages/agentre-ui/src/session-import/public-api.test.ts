import { describe, expect, it } from "vitest";

import * as pkg from "../index";

/**
 * `src/index.ts` 是这个包**唯一**的门：package.json 的 exports 只把 `.` 映射到
 * `dist/index.js`，没从 barrel 里放出去的东西，消费方（agentre-server 经 git 依赖、
 * 桌面端经 workspace）根本看不见 —— 而那个失败要等到对面 import 时才炸。
 *
 * 导入本地会话这一面是跨仓契约（规格 2026-08-26 硬约束 6：候选列表、转录预览、
 * 导入对话框、组头菜单条目各只有一份实现，且那份实现住在这里），所以钉在这里。
 */
describe("导入本地会话的对外契约", () => {
  it("呈现件与纯函数都从 barrel 出得去", () => {
    const missing = [
      // 入口：四条轴共用同一份条目定义
      "ImportLocalSessionMenu",
      "ImportLocalSessionIcon",
      "useImportLocalSessionLabel",
      "IMPORT_MENU_ITEM_ID",
      // 对话框与两栏
      "ImportSessionDialog",
      "CandidateList",
      "PreviewPane",
      // 纯函数：两端按同一条规则分组与格式化
      "buildCandidateGroups",
      "formatCandidateTime",
    ].filter((name) => !(name in pkg));

    expect(missing).toEqual([]);
  });

  /**
   * 类型也要出得去，否则宿主写不出 adapter —— 它得先说得出 `SessionImportPorts`
   * 是什么才实现得了它。类型在运行时没有值，所以这里检的是**源码里的导出语句**。
   */
  it("宿主写 adapter 要用到的类型都在 barrel 上", async () => {
    const { readFileSync } = await import("node:fs");
    const { join } = await import("node:path");
    const barrel = readFileSync(join(__dirname, "..", "index.ts"), "utf8");
    const missing = [
      "SessionImportPorts",
      "ImportDeviceView",
      "ImportAgentOption",
      "ImportCandidateView",
      "ImportCandidatesRequest",
      "ImportCandidatesResult",
      "ImportScanIssue",
      "ImportScanStatus",
      "ImportGapView",
      "ImportTranscriptMetaView",
      "ImportPreviewRequest",
      "ImportPreviewResult",
      "ImportRunRequest",
      "ImportOutcome",
      "ImportDialogPrefill",
      "ImportSessionDialogProps",
      "CandidateListProps",
      "PreviewPaneProps",
      "PreviewState",
      "CandidateBucket",
      "CandidateGroup",
      "ImportLocalSessionMenuProps",
    ].filter((name) => !new RegExp(`\\b${name}\\b`).test(barrel));

    expect(missing).toEqual([]);
  });
});
