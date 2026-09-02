import { describe, expect, it } from "vitest";

import type { TranscriptMessage } from "./dto";
import {
  indicatorHostMessageId,
  isNoticeOnlyMessage,
} from "./generating-indicator";

function message(
  id: number,
  role: string,
  blocks: TranscriptMessage["blocks"] = [],
): TranscriptMessage {
  return {
    id,
    sessionId: 1,
    role,
    blocks,
    model: "",
    promptTokens: 0,
    completionTokens: 0,
    cachedTokens: 0,
    cacheCreationTokens: 0,
    reasoningTokens: 0,
    totalInputTokens: 0,
    durationMs: 0,
    errorText: "",
    seq: 0,
    createtime: 0,
  };
}

describe("indicatorHostMessageId", () => {
  it("末条是 assistant:挂在它身上", () => {
    const messages = [message(1, "user"), message(2, "assistant")];
    expect(indicatorHostMessageId(messages)).toBe(2);
  });

  // 这一条是本函数存在的理由。往回找会挂到上一轮的回复上 —— 用户刚发出去的那句话
  // 下面空着,而上面那段早就说完的回复看着像还在写。
  it("末条是用户消息:谁都不挂,不往回找上一轮的回复", () => {
    const messages = [
      message(1, "user"),
      message(2, "assistant"),
      message(3, "user"),
    ];
    expect(indicatorHostMessageId(messages)).toBeNull();
  });

  it("一条都没有:谁都不挂", () => {
    expect(indicatorHostMessageId([])).toBeNull();
  });

  // 轮刚起时 assistant 那条的 blocks 恒为空,那是真实的一轮 —— 指示器就该在那一刻
  // 出现,不能被当成「没内容」跳过。
  it("空块的 assistant 占位:照挂", () => {
    const messages = [message(1, "user"), message(2, "assistant", [])];
    expect(indicatorHostMessageId(messages)).toBe(2);
  });

  it("供应商切换的旁白行透明:三点留在它前面那条在跑的 assistant 上", () => {
    const messages = [
      message(1, "user"),
      message(2, "assistant"),
      message(3, "assistant", [
        { type: "notice", noticeKind: "switch", text: "换了个供应商" },
      ]),
    ];
    expect(indicatorHostMessageId(messages)).toBe(2);
  });

  it("旁白行之前是用户消息:照样谁都不挂", () => {
    const messages = [
      message(1, "user"),
      message(2, "assistant", [
        { type: "notice", noticeKind: "switch", text: "换了个供应商" },
      ]),
    ];
    expect(indicatorHostMessageId(messages)).toBeNull();
  });

  // 会话级思考力度切换 notice(spec 2026-09-01 决策 7)与供应商切换 notice 同一
  // 口径:也是独立插入、随时可能落在在跑 assistant 之后的旁白行,必须同样透明。
  it("会话思考力度切换的旁白行也透明:三点留在它前面那条在跑的 assistant 上", () => {
    const messages = [
      message(1, "user"),
      message(2, "assistant"),
      message(3, "assistant", [
        {
          type: "notice",
          noticeKind: "reasoning_effort",
          text: "已切换到 high",
        },
      ]),
    ];
    expect(indicatorHostMessageId(messages)).toBe(2);
  });
});

describe("isNoticeOnlyMessage", () => {
  it("只有 switch notice:是旁白行", () => {
    expect(
      isNoticeOnlyMessage({
        blocks: [{ type: "notice", noticeKind: "switch" }],
      }),
    ).toBe(true);
  });

  // 回退 notice 由后端追加进**这一轮自己**的 assistant 消息,零内容收尾时块正好
  // 只剩它 —— 按「块全是 notice」判,一轮真实对话就会被当成旁白行跳过。
  it("别的 noticeKind:不是旁白行", () => {
    expect(
      isNoticeOnlyMessage({
        blocks: [{ type: "notice", noticeKind: "fallback" }],
      }),
    ).toBe(false);
  });

  it("reasoning_effort notice:也是旁白行(与 switch 同一口径)", () => {
    expect(
      isNoticeOnlyMessage({
        blocks: [{ type: "notice", noticeKind: "reasoning_effort" }],
      }),
    ).toBe(true);
  });

  it("没有块:不是旁白行", () => {
    expect(isNoticeOnlyMessage({ blocks: [] })).toBe(false);
  });

  it("掺着正文:不是旁白行", () => {
    expect(
      isNoticeOnlyMessage({
        blocks: [
          { type: "notice", noticeKind: "switch" },
          { type: "text", text: "在的" },
        ],
      }),
    ).toBe(false);
  });
});
