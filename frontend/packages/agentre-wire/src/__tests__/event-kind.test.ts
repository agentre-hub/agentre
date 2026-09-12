/**
 * 判别值现在住在 .proto 上,两侧从 descriptor 读同一格。这些用例守的是那一格
 * 本身：它必须齐、必须落在 agentruntime 的词表里、必须保住那几条拼法不一致的配对。
 */
import { describe, expect, it } from "vitest";

import * as generated from "../event-kinds.gen";
import { EVENT_CASE_BY_KIND, EVENT_KIND_BY_CASE } from "../event-kind";
import { RuntimeEventNotificationSchema } from "../gen/agentre/wire/wire_pb";

/** agentruntime 词表的运行期集合。从生成文件的导出**推**出来，不另写一份清单。 */
const vocabulary = new Set(
  Object.entries(generated)
    .filter(
      ([name, value]) => name.startsWith("Event") && typeof value === "string",
    )
    .map(([, value]) => value as string),
);

describe("event kind declared on the schema", () => {
  it("covers every oneof branch", () => {
    const branches = RuntimeEventNotificationSchema.fields
      .filter((field) => field.oneof?.name === "event")
      .map((field) => field.localName);

    expect(branches.length).toBeGreaterThan(0);
    expect(Object.keys(EVENT_KIND_BY_CASE).sort()).toEqual(branches.sort());
  });

  // 判别值落在词表外，运行期就是「未知事件」：归约器的 default 分支把它铺成一坨
  // JSON notice。这是手抄时代四条错映射的确切症状，所以这里逐个对词表校验。
  it("only declares kinds that exist in the agentruntime vocabulary", () => {
    for (const [branch, kind] of Object.entries(EVENT_KIND_BY_CASE)) {
      expect(
        vocabulary,
        `分支 ${branch} 的判别值 ${kind} 不在 EventKind 词表里`,
      ).toContain(kind);
    }
  });

  // 拼法一致的分支占多数，正因如此它们测不出问题；这四条是不一致的全部，
  // 也正是曾经写错的那几条。
  it("keeps the four branches whose spelling differs from their kind", () => {
    expect(EVENT_KIND_BY_CASE.toolCall).toBe("tool_use_start");
    expect(EVENT_KIND_BY_CASE.userAskRequest).toBe("ask_user_question");
    expect(EVENT_KIND_BY_CASE.userAskResolved).toBe(
      "ask_user_question_answered",
    );
    expect(EVENT_KIND_BY_CASE.usageUpdate).toBe("usage");
  });

  it("inverts without losing a branch", () => {
    expect(Object.keys(EVENT_CASE_BY_KIND).length).toBe(
      Object.keys(EVENT_KIND_BY_CASE).length,
    );
    expect(EVENT_CASE_BY_KIND.tool_use_start).toBe("toolCall");
  });
});
