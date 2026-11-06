import { describe, expect, it } from "vitest";
import type { TranscriptBlock } from "./dto";
import { countPendingBelow, pendingDecisionId } from "./pending-below";

const perm = (requestId: string, resolved: boolean): TranscriptBlock =>
  ({
    type: "tool_use",
    canonical: {
      kind: "tool.permission",
      toolPermission: { requestId, toolName: "Bash", resolved },
    },
  }) as unknown as TranscriptBlock;
const ask = (requestId: string, answered: boolean): TranscriptBlock =>
  ({
    type: "ask_user_question",
    canonical: {
      kind: "user.ask",
      userAsk: { requestId, questions: [], answered },
    },
  }) as unknown as TranscriptBlock;
const legacyPerm = (requestId: string, resolved: boolean): TranscriptBlock =>
  ({
    type: "tool_use",
    toolPermission: { requestId, resolved },
  }) as unknown as TranscriptBlock;
const legacyAsk = (requestId: string, o: object): TranscriptBlock =>
  ({
    type: "ask_user_question",
    askUserQuestion: { requestId, ...o },
  }) as unknown as TranscriptBlock;
const toolApproval = (requestId: string, status: string): TranscriptBlock =>
  ({
    type: "tool_approval",
    toolApproval: { requestId, status },
  }) as unknown as TranscriptBlock;
const exec = (id: string, status: string): TranscriptBlock =>
  ({
    type: "exec_approval",
    execApproval: { id, status },
  }) as unknown as TranscriptBlock;

describe("pendingDecisionId", () => {
  it("canonical tool.permission: 未决返回 requestId,已决 null", () => {
    expect(pendingDecisionId(perm("a", false))).toBe("a");
    expect(pendingDecisionId(perm("a", true))).toBeNull();
  });
  it("canonical user.ask: 未决返回 requestId,已答 null", () => {
    expect(pendingDecisionId(ask("q", false))).toBe("q");
    expect(pendingDecisionId(ask("q", true))).toBeNull();
  });
  it("旧形态 toolPermission", () => {
    expect(pendingDecisionId(legacyPerm("p", false))).toBe("p");
    expect(pendingDecisionId(legacyPerm("p", true))).toBeNull();
  });
  it("旧形态 askUserQuestion: answered / skipped / expired 都算已决", () => {
    expect(pendingDecisionId(legacyAsk("q", {}))).toBe("q");
    expect(pendingDecisionId(legacyAsk("q", { answered: true }))).toBeNull();
    expect(pendingDecisionId(legacyAsk("q", { skipped: true }))).toBeNull();
    expect(pendingDecisionId(legacyAsk("q", { expired: true }))).toBeNull();
  });
  it("toolApproval: 仅 pending 计", () => {
    expect(pendingDecisionId(toolApproval("t", "pending"))).toBe("t");
    expect(pendingDecisionId(toolApproval("t", "approved"))).toBeNull();
  });
  it("execApproval: 仅 pending 计,以 id 为键", () => {
    expect(pendingDecisionId(exec("e", "pending"))).toBe("e");
    expect(pendingDecisionId(exec("e", "approved"))).toBeNull();
  });
  it("普通块返回 null", () => {
    expect(pendingDecisionId({ type: "text" } as TranscriptBlock)).toBeNull();
  });
});

describe("countPendingBelow", () => {
  const m = (id: number, blocks: TranscriptBlock[] = []) => ({ id, blocks });

  it("每种块各计一项", () => {
    const msgs = [
      m(1),
      m(2, [
        perm("a", false),
        ask("b", false),
        legacyPerm("c", false),
        legacyAsk("d", {}),
        toolApproval("e", "pending"),
        exec("f", "pending"),
      ]),
    ];
    expect(countPendingBelow(msgs, 1)).toBe(6);
  });
  it("已决不计", () => {
    expect(
      countPendingBelow(
        [m(1), m(2, [perm("a", true), exec("f", "approved")])],
        1,
      ),
    ).toBe(0);
  });
  it("只数 id 大于下沿的消息", () => {
    expect(
      countPendingBelow(
        [m(1, [perm("a", false)]), m(2, [perm("b", false)])],
        1,
      ),
    ).toBe(1);
    expect(countPendingBelow([m(1, [perm("a", false)]), m(2)], 2)).toBe(0);
  });
  it("按 requestId 去重", () => {
    expect(
      countPendingBelow(
        [
          m(2, [perm("x", false)]),
          m(2, [perm("x", false)]),
          m(3, [ask("x", false)]),
        ],
        1,
      ),
    ).toBe(1);
  });
  it("下沿为 null 记 0", () => {
    expect(countPendingBelow([m(2, [perm("a", false)])], null)).toBe(0);
  });
  it("消息没有 blocks 不报错", () => {
    expect(countPendingBelow([{ id: 2 }], 1)).toBe(0);
  });
});
