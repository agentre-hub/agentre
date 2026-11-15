import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

import * as pkg from "../index";

describe("批量清理对话的对外契约", () => {
  it("SessionCleanupSection 从 barrel 出得去", () => {
    expect("SessionCleanupSection" in pkg).toBe(true);
  });

  it("宿主写 adapter 要用到的类型都在 barrel 上", () => {
    const barrel = readFileSync(join(__dirname, "..", "index.ts"), "utf8");
    const missing = [
      "SessionCleanupPorts",
      "CleanupFilter",
      "CleanupPreviewView",
      "CleanupExecuteView",
      "CleanupCopyView",
      "CleanupOption",
      "SessionCleanupSectionProps",
    ].filter((name) => !new RegExp(`\\b${name}\\b`).test(barrel));

    expect(missing).toEqual([]);
  });
});
