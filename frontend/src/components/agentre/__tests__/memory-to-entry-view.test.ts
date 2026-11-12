import { describe, expect, it } from "vitest";

import type { memory_svc } from "../../../../wailsjs/go/models";
import { toEntryView } from "../memory-page/to-entry-view";

// 行上的「最近修改日期」是最近一次变更（与预算取舍、注入段落同一口径），不是行的
// updatetime：置顶、同步落地、项目合并都会刷新 updatetime，却不是对内容的修改。

const ctx = {
  sessionTitle: () => undefined,
  fallbackLabel: () => "",
  openSession: () => {},
};

describe("toEntryView", () => {
  it("Given a row touched after its last change, Then updatedAt is the last change time", () => {
    const view = toEntryView(
      {
        id: 1,
        owner: { kind: "global", id: 0 },
        summary: "用 pnpm",
        detail: "",
        pinned: true,
        state: "normal",
        hasPrevious: false,
        prevSummary: "",
        prevDetail: "",
        creator: { kind: "user", agentId: 0, sessionId: 0 },
        lastChange: {
          action: "created",
          actor: { kind: "user", agentId: 0, sessionId: 0 },
          at: 1_000,
        },
        createtime: 1_000,
        updatetime: 9_000,
      } as unknown as memory_svc.MemoryItem,
      ctx,
    );
    expect(view.updatedAt).toBe(1_000);
  });
});
