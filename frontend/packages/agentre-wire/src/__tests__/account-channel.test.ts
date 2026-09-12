import { describe, expect, it } from "vitest";

import {
  AccountChannelSyncVersion,
  ProtobufAccountChannelCodec,
} from "../index";

describe("account channel wire contract", () => {
  it("given a sync signal, when protobuf encodes it, then Go-compatible bytes survive future fields", () => {
    const payload = ProtobufAccountChannelCodec.encode({
      type: AccountChannelSyncVersion,
      version: 42,
    });

    expect(Array.from(payload)).toEqual([0x0a, 0x04, 0x0a, 0x02, 0x08, 0x2a]);

    const withFutureField = Uint8Array.from([...payload, 0x98, 0x06, 0x07]);
    expect(ProtobufAccountChannelCodec.decode(withFutureField)).toEqual({
      type: AccountChannelSyncVersion,
      version: 42,
    });
  });

  it("ignores future notifications and rejects malformed protobuf", () => {
    expect(
      ProtobufAccountChannelCodec.decode(
        Uint8Array.from([0x0a, 0x03, 0x98, 0x06, 0x01]),
      ),
    ).toBeNull();
    expect(() =>
      ProtobufAccountChannelCodec.decode(Uint8Array.from([0x0a, 0xff])),
    ).toThrow();
  });
});
