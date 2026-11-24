import { describe, expect, expectTypeOf, it } from "vitest";

import type { TranscriptBlock } from "./dto";

/**
 * 工具卡上的「AI 当时所在目录」（spec 2026-11-24-chat-right-toolwindows 决策 17）：
 * 后端 `chat_svc.ChatBlock.Cwd`（json `cwd`）的手写镜像。没有记录的旧消息不带这一格，
 * 消费方据此回落到会话工作目录，所以它是可选的字符串。
 *
 * 类型断言在 `tsc -b` 阶段生效（宿主的 typecheck 覆盖 packages/agentre-ui/src）。
 */
describe("TranscriptBlock.cwd", () => {
  it("carries the directory recorded on a tool block and stays optional", () => {
    expectTypeOf<TranscriptBlock["cwd"]>().toEqualTypeOf<string | undefined>();

    const recorded: TranscriptBlock = {
      type: "tool_use",
      toolUseId: "t1",
      cwd: "/repo/.claude/worktrees/feat",
    };
    const legacy: TranscriptBlock = { type: "tool_use", toolUseId: "t0" };

    expect(recorded.cwd).toBe("/repo/.claude/worktrees/feat");
    expect(legacy.cwd).toBeUndefined();
  });
});
