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
