import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import {
  RuntimeRunResponseSchema,
  SessionListResponseSchema,
  SessionSummarySchema,
} from "../gen/agentre/wire/wire_pb";
import {
  runAckFromProtobuf,
  sessionListFromProtobuf,
} from "../protobuf-domain";

// 线上对话身份是 uuid;这些用例要证的是"同一个值原样往返",取一个可读的固定值。
const CONVERSATION_ID = "00000000-0000-7000-8000-000000000042";

describe("protobuf response to domain wire", () => {
  it("given a real session.list protobuf shape, then bigint fields become domain numbers", () => {
    const response = create(SessionListResponseSchema, {
      sessions: [
        create(SessionSummarySchema, {
          conversationId: CONVERSATION_ID,
          lifecycleState: "idle",
          latestSeq: 7n,
          lastMessageAt: 1_788_000_000_000n,
        }),
      ],
    });

    expect(sessionListFromProtobuf(response)).toEqual({
      sessions: [
        expect.objectContaining({
          conversationId: CONVERSATION_ID,
          lifecycleState: "idle",
          latestSeq: 7,
          lastMessageAt: 1_788_000_000_000,
        }),
      ],
    });
  });

  it("given a session that carries a CLI model choice, then its mode and model cross into the domain shape", () => {
    // web 端 pill 与每轮运行请求都从这里读会话的 CLI 选择;丢了它,承载机器上
    // 存着的「固定 opus」在浏览器里就成了「跟随 Agent 绑定」。
    const response = create(SessionListResponseSchema, {
      sessions: [
        create(SessionSummarySchema, {
          conversationId: CONVERSATION_ID,
          lifecycleState: "idle",
          latestSeq: 1n,
          cliModelMode: "fixed",
          cliModel: "opus",
        }),
      ],
    });

    expect(sessionListFromProtobuf(response).sessions[0]).toEqual(
      expect.objectContaining({ cliModelMode: "fixed", cliModel: "opus" }),
    );
  });

  it("given a session with no CLI model choice, then no CLI keys are invented", () => {
    // 空 = 跟随 Agent 绑定;与 providerKey 一样,零值不写成 "",留给调用方按「没选」处理。
    const response = create(SessionListResponseSchema, {
      sessions: [
        create(SessionSummarySchema, {
          conversationId: CONVERSATION_ID,
          lifecycleState: "idle",
          latestSeq: 1n,
        }),
      ],
    });

    const [session] = sessionListFromProtobuf(response).sessions;
    expect(session.cliModelMode).toBeUndefined();
    expect(session.cliModel).toBeUndefined();
  });

  it("given a paged session.list answer, then the cursor, hasMore and total cross into the domain shape", () => {
    // 机器轴靠这三格才翻得动:没有它们,浏览器只能整份取回来再自己切。
    const response = create(SessionListResponseSchema, {
      sessions: [],
      cursor: "20",
      hasMore: true,
      total: 44n,
    });

    expect(sessionListFromProtobuf(response)).toEqual({
      sessions: [],
      cursor: "20",
      hasMore: true,
      total: 44,
    });
  });

  it("given an unpaged answer from an older peer, then no paging keys are invented", () => {
    // 老对端不认得这三个字段,解出来是零值。把它们照原样填成 ""/false/0 会让调用方
    // 以为「这是最后一页」——那恰好是对的,但 total=0 会把「查看全部 N」写成 0。
    // 所以零值一律不进 domain 形状,调用方按「没说」处理,回退到手上的条数。
    const response = create(SessionListResponseSchema, { sessions: [] });

    expect(sessionListFromProtobuf(response)).toEqual({ sessions: [] });
  });

  it("given a real runtime.run protobuf ACK, then its conversation id crosses unchanged", () => {
    const response = create(RuntimeRunResponseSchema, {
      conversationId: CONVERSATION_ID,
    });

    expect(runAckFromProtobuf(response).conversationId).toBe(CONVERSATION_ID);
  });

  it("rejects protobuf integers that cannot be represented without precision loss", () => {
    // 对话身份换成字符串之后,仍然过 safeNumber 的是 int64 计数列(seq / agentId);
    // 这条守的就是它们:超出安全整数时报错,而不是静默丢精度。
    const response = create(SessionListResponseSchema, {
      sessions: [
        create(SessionSummarySchema, {
          conversationId: CONVERSATION_ID,
          latestSeq: BigInt(Number.MAX_SAFE_INTEGER) + 1n,
        }),
      ],
    });

    expect(() => sessionListFromProtobuf(response)).toThrow(/safe integer/i);
  });
});
