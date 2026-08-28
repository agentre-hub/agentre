import { describe, expect, it } from "vitest";

import type { TranscriptMessage } from "./dto";
import {
  autonomousTurnMessageIds,
  computeBottomVisibleMessageId,
  countTurnsAfterMessage,
} from "./transcript-turns";

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

/** 只承载供应商切换 notice 的旁白行 —— 它不是一轮对话。 */
function switchNotice(id: number): TranscriptMessage {
  return message(id, "assistant", [{ type: "notice", noticeKind: "switch" }]);
}

describe("autonomousTurnMessageIds", () => {
  it("Given 正常的 user→assistant, When 判定, Then 那条 assistant 不是自主续轮", () => {
    const messages = [message(1, "user"), message(2, "assistant")];
    expect([...autonomousTurnMessageIds(messages)]).toEqual([]);
  });

  it("Given assistant→assistant, When 判定, Then 后一条是自主续轮", () => {
    const messages = [
      message(1, "user"),
      message(2, "assistant"),
      message(3, "assistant"),
    ];
    expect([...autonomousTurnMessageIds(messages)]).toEqual([3]);
  });

  it("Given 会话首条就是 assistant, When 判定, Then 不算自主续轮", () => {
    expect([...autonomousTurnMessageIds([message(1, "assistant")])]).toEqual(
      [],
    );
  });

  // 旁白行垫在两条真实 assistant 之间时若不透明,后一条的「紧邻前一条」就变成了它,
  // 判定会被拆断。
  it("Given 旁白行垫在两条 assistant 之间, When 判定, Then 它自己不算、也不打断后一条的判定", () => {
    const messages = [
      message(1, "user"),
      message(2, "assistant"),
      switchNotice(3),
      message(4, "assistant"),
    ];
    expect([...autonomousTurnMessageIds(messages)]).toEqual([4]);
  });

  it("Given 旁白行垫在 user 与 assistant 之间, When 判定, Then 后一条仍是正常轮", () => {
    const messages = [
      message(1, "user"),
      switchNotice(2),
      message(3, "assistant"),
    ];
    expect([...autonomousTurnMessageIds(messages)]).toEqual([]);
  });
});

describe("countTurnsAfterMessage", () => {
  it("Given 边界之后有两条 user 消息, When 计数, Then 得 2", () => {
    const messages = [
      message(1, "user"),
      message(2, "assistant"),
      message(3, "user"),
      message(4, "assistant"),
      message(5, "user"),
      message(6, "assistant"),
    ];
    expect(countTurnsAfterMessage(messages, 2)).toBe(2);
  });

  // 边界正好落在用户刚发出的那条上:它的回复属于**同一轮**,下面不该算「还有一轮」。
  it("Given 边界是 user 消息、其后只有本轮的 assistant 回复, When 计数, Then 得 0", () => {
    const messages = [message(1, "user"), message(2, "assistant")];
    expect(countTurnsAfterMessage(messages, 1)).toBe(0);
  });

  it("Given 边界之后是自主续轮, When 计数, Then 算作一轮", () => {
    const messages = [
      message(1, "user"),
      message(2, "assistant"),
      message(3, "assistant"),
    ];
    expect(countTurnsAfterMessage(messages, 2)).toBe(1);
  });

  it("Given 边界之后只有旁白行, When 计数, Then 得 0", () => {
    const messages = [
      message(1, "user"),
      message(2, "assistant"),
      switchNotice(3),
    ];
    expect(countTurnsAfterMessage(messages, 2)).toBe(0);
  });

  // 边界行自己就是开轮行(这里是自主续轮的首条 assistant):它在视口里,不该被算进
  // 「下面还有几轮」。钉的是 index > boundary 而不是 >=。
  it("Given 边界行自己就是一轮的开头, When 计数, Then 不把它算进去", () => {
    const messages = [
      message(1, "user"),
      message(2, "assistant"),
      message(3, "assistant"),
    ];
    expect(countTurnsAfterMessage(messages, 3)).toBe(0);
  });

  // 数不出视口下沿(容器未布局 / 转录里一条消息行都没有)时不猜数字。
  it("Given 边界为 null, When 计数, Then 得 0", () => {
    const messages = [message(1, "user"), message(2, "user")];
    expect(countTurnsAfterMessage(messages, null)).toBe(0);
  });

  // 重载/删除后边界指向了一条已经不在列表里的消息:保守报 0,不去猜它原来在哪。
  it("Given 边界消息已不在列表里, When 计数, Then 得 0", () => {
    const messages = [message(1, "user"), message(2, "assistant")];
    expect(countTurnsAfterMessage(messages, 99)).toBe(0);
  });

  it("Given 空列表, When 计数, Then 得 0", () => {
    expect(countTurnsAfterMessage([], 1)).toBe(0);
  });
});

// countTurnsAfterMessage 的边界从哪来。宿主的行外层挂 data-message-id，这里按几何
// 挑出「最后一条顶边仍在视口内」的那条 —— 用视口**顶**那条当起点会把用户正看着的
// 这一屏也算进「下面还有」，数字恒偏大。
describe("computeBottomVisibleMessageId", () => {
  function fakeRow(id: string, top: number): HTMLElement {
    return {
      getAttribute: (name: string) => (name === "data-message-id" ? id : null),
      getBoundingClientRect: () => ({ top }) as DOMRect,
    } as unknown as HTMLElement;
  }
  function fakeContainer(bottom: number, rows: HTMLElement[]): HTMLElement {
    return {
      getBoundingClientRect: () => ({ bottom }) as DOMRect,
      querySelectorAll: () => rows as unknown as NodeListOf<HTMLElement>,
    } as unknown as HTMLElement;
  }

  it("Given 若干行跨过视口下沿, When 定位, Then 取最后一条顶边仍在视口内的消息", () => {
    const el = fakeContainer(400, [
      fakeRow("1", 0),
      fakeRow("2", 200),
      fakeRow("3", 380), // 顶边仍在视口内 → 命中
      fakeRow("4", 460), // 整条在视口下方（虚拟列表 overscan 渲出来的）→ 不算
    ]);
    expect(computeBottomVisibleMessageId(el)).toBe(3);
  });

  it("Given 一条长消息拆成的多行都属于同一条消息, When 定位, Then 返回那条消息的 id", () => {
    const el = fakeContainer(400, [fakeRow("9", 10), fakeRow("9", 300)]);
    expect(computeBottomVisibleMessageId(el)).toBe(9);
  });

  it("Given 转录里一条消息行都没有, When 定位, Then 返回 null", () => {
    expect(computeBottomVisibleMessageId(fakeContainer(400, []))).toBeNull();
  });

  // 容器还没布局时 rect 全是 0，每一行的顶边都不小于下沿 —— 此时不猜，交给调用方
  // 退回「回到底部」。
  it("Given 容器尚未布局, When 定位, Then 返回 null", () => {
    expect(
      computeBottomVisibleMessageId(fakeContainer(0, [fakeRow("1", 0)])),
    ).toBeNull();
  });
});
