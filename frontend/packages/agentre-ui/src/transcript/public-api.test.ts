import { describe, expect, it } from "vitest";

import * as pkg from "../index";

/**
 * `src/index.ts` 是这个包唯一的门：没从 barrel 放出去的东西，消费方
 * （agentre-server 经 git 依赖、桌面端经 workspace）根本看不见。
 */
describe("转录的对外契约（手机形态新件）", () => {
  it("从 barrel 出得去", () => {
    const missing = [
      "formatCompactTurnDuration",
      "compactTokenCount",
      "normalizeModelName",
      "fitMetaLine",
      "ToolPermissionDock",
      "ShortcutHintsProvider",
      "useShortcutHintsHidden",
      "isPendingToolPermission",
    ].filter((name) => !(name in pkg));

    expect(missing).toEqual([]);
  });
});
