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

describe("protobuf response to domain wire", () => {
  it("given a real session.list protobuf shape, then bigint fields become domain numbers", () => {
    const response = create(SessionListResponseSchema, {
      sessions: [
        create(SessionSummarySchema, {
          sessionId: 42n,
          lifecycleState: "idle",
          latestSeq: 7n,
          lastMessageAt: 1_788_000_000_000n,
        }),
      ],
    });

    expect(sessionListFromProtobuf(response)).toEqual({
      sessions: [
        expect.objectContaining({
          sessionId: 42,
          lifecycleState: "idle",
          latestSeq: 7,
          lastMessageAt: 1_788_000_000_000,
        }),
      ],
    });
  });

  it("given a real runtime.run protobuf ACK, then its session ID becomes a domain number", () => {
    const response = create(RuntimeRunResponseSchema, { sessionId: 9001n });

    expect(runAckFromProtobuf(response).sessionId).toBe(9001);
  });

  it("rejects protobuf integers that cannot be represented without precision loss", () => {
    const response = create(RuntimeRunResponseSchema, {
      sessionId: BigInt(Number.MAX_SAFE_INTEGER) + 1n,
    });

    expect(() => runAckFromProtobuf(response)).toThrow(/safe integer/i);
  });
});
