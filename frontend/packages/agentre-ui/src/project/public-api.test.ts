import { describe, expect, it } from "vitest";

import * as pkg from "../index";

/**
 * `src/index.ts` 是这个包**唯一**的门：package.json 的 exports 只把 `.` 映射到
 * `dist/index.js`，没从 barrel 里放出去的东西，消费方（agentre-server 经 git 依赖、
 * 桌面端经 workspace）根本看不见 —— 而那个失败要等到对面 import 时才炸。
 *
 * 项目这一面是跨仓契约（规格 2026-08-22：项目设置 / 新建 / 删除确认 / 组头菜单 /
 * 目录选择器各只保留一份实现，且那份实现住在这里），所以钉在这里。
 */
describe("项目这一面的对外契约", () => {
  it("五件呈现件与纯函数都从 barrel 出得去", () => {
    const missing = [
      // 弹窗外壳（两端的表单都建在它上面）
      "DialogShell",
      "DialogShellHeader",
      "DialogShellBody",
      "DialogShellFooter",
      "DialogShellSubmit",
      // 项目表单三件
      "ProjectSettingsDialog",
      "ProjectCreateDialog",
      "ProjectDeleteDialog",
      // 组头动作：⋮ 与右键由同一份定义渲染两遍
      "ProjectHeaderActions",
      "ProjectHeaderContextMenu",
      // 目录选择器
      "DirectoryPicker",
      // 纯函数：两端都要按同一条规则拼路径与切面包屑
      "joinPath",
      "breadcrumbOf",
    ].filter((name) => !(name in pkg));

    expect(missing).toEqual([]);
  });

  /**
   * 类型也要出得去，否则宿主写不出 adapter —— 它得先说得出 `ProjectSettingsPorts`
   * 是什么才实现得了它。类型在运行时没有值，所以这里检的是**源码里的导出语句**，
   * 而不是 `in pkg`（那对 type-only 导出恒为 false）。
   */
  it("宿主写 adapter 要用到的类型都在 barrel 上", async () => {
    const { readFileSync } = await import("node:fs");
    const { join } = await import("node:path");
    const barrel = readFileSync(join(__dirname, "..", "index.ts"), "utf8");
    const missing = [
      "ProjectFsPort",
      "ListDirOutcome",
      "MkdirOutcome",
      "PickerMachine",
      "ProjectSettingsPorts",
      "ProjectSettingsView",
      "ProjectMachineView",
      "ProjectMemberView",
      "ProjectCandidateView",
      "ProjectFieldValues",
      "ProjectWriteOutcome",
      "ProjectWriteFailure",
      "ProjectWriteFailureKind",
      "ProjectCreatePorts",
      "ProjectCreateDraft",
      "ProjectCreateOutcome",
      "ProjectGitInfo",
      "ProjectDeletePorts",
      "ProjectHeaderActionsProps",
      "ProjectHeaderMember",
      "ProjectMenuCapabilities",
      "DialogShellSaveState",
      "DialogShellSize",
    ].filter((name) => !new RegExp(`\\b${name}\\b`).test(barrel));

    expect(missing).toEqual([]);
  });
});
