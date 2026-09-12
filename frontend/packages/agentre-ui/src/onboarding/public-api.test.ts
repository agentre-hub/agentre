import { describe, expect, it } from "vitest";

import * as pkg from "../index";

/**
 * 引导这一面是跨仓契约（规格 2026-09-05：两端引导由同一份引导域渲染），而
 * `src/index.ts` 是这个包唯一的门 —— 没从 barrel 出去的东西，agentre-server 经
 * git 依赖根本看不见，且那个失败要等到对面 import 时才炸。
 */
describe("引导这一面的对外契约", () => {
  it("三件呈现件、两个纯函数入口与常量都从 barrel 出得去", () => {
    const missing = [
      // 呈现件
      "CommandCard",
      "GuideStepRail",
      "AgentredInstallSection",
      "AgentredInstallDocsLink",
      "AgentredServiceSection",
      // 命令：两端唯一的一份
      "agentredCommands",
      "agentredInstallCommand",
      "agentredVersionCommand",
      "agentredLoginCommand",
      "agentredPairCommand",
      // 对外链接
      "AGENTRED_IMAGE",
      "AGENTRED_RELEASES_URL",
      "AGENTRED_DEPLOY_DOC_URL",
    ].filter((name) => !(name in pkg));

    expect(missing).toEqual([]);
  });

  /**
   * 类型也要出得去，否则宿主声明不出自己那几个 useState —— 它得先说得出「安装方式」
   * 的取值范围。类型在运行时没有值，所以这里检的是**源码里的导出语句**，
   * 而不是 `in pkg`（那对 type-only 导出恒为 false）。
   */
  it("宿主声明自身状态要用到的类型都在 barrel 上", async () => {
    const { readFileSync } = await import("node:fs");
    const { join } = await import("node:path");
    const barrel = readFileSync(join(__dirname, "..", "index.ts"), "utf8");
    const missing = [
      "AgentredInstallMethod",
      "AgentredTargetOS",
      "AgentredRunMode",
      "GuideStep",
      "GuideStepRailProps",
      "CommandCardProps",
      "AgentredInstallSectionProps",
      "AgentredServiceSectionProps",
    ].filter((name) => !new RegExp(`\\b${name}\\b`).test(barrel));

    expect(missing).toEqual([]);
  });
});
