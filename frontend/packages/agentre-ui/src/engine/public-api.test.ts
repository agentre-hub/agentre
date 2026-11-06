import { describe, expect, it } from "vitest";

import * as pkg from "../index";

/**
 * `src/index.ts` 是这个包唯一的门：没从 barrel 放出去的东西，消费方
 * （agentre-server 经 git 依赖、桌面端经 workspace）根本看不见。
 */
describe("引擎配置的对外契约", () => {
  it("从 barrel 出得去", () => {
    const missing = [
      "LlmProvidersPanel",
      "AgentBackendsPanel",
      "ModelTargetPicker",
    ].filter((name) => !(name in pkg));

    expect(missing).toEqual([]);
  });
});
