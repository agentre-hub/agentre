import { create, fromBinary, toBinary } from "@bufbuild/protobuf";

import {
  AccountDevicePresenceSchema,
  AccountMirrorChangedSchema,
  AccountSyncVersionSchema,
  NotificationSchema,
  WireFrameSchema,
} from "./gen/agentre/wire/wire_pb";

export const AccountChannelSyncVersion = "sync_version";
export const AccountChannelMirrorChanged = "mirror_changed";
export const AccountChannelDevicePresence = "device_presence";

export interface AccountChannelSignal {
  type: string;
  version: number;
}

export interface AccountChannelCodec {
  encode(signal: AccountChannelSignal): Uint8Array;
  decode(payload: unknown): AccountChannelSignal | null;
}

function payloadBytes(payload: unknown): Uint8Array {
  if (
    payload instanceof ArrayBuffer ||
    Object.prototype.toString.call(payload) === "[object ArrayBuffer]"
  ) {
    return new Uint8Array(payload as ArrayBuffer);
  }
  if (ArrayBuffer.isView(payload)) {
    return new Uint8Array(
      payload.buffer,
      payload.byteOffset,
      payload.byteLength,
    );
  }
  throw new TypeError("wire: payload 必须是二进制字节");
}

export const ProtobufAccountChannelCodec: AccountChannelCodec = {
  encode(signal) {
    let payload;
    switch (signal.type) {
      case AccountChannelSyncVersion:
        payload = {
          case: "accountSyncVersion" as const,
          value: create(AccountSyncVersionSchema, {
            version: BigInt(signal.version),
          }),
        };
        break;
      case AccountChannelMirrorChanged:
        payload = {
          case: "accountMirrorChanged" as const,
          value: create(AccountMirrorChangedSchema),
        };
        break;
      case AccountChannelDevicePresence:
        payload = {
          case: "accountDevicePresence" as const,
          value: create(AccountDevicePresenceSchema),
        };
        break;
      default:
        throw new TypeError(`wire: 未知账号通道信号 ${signal.type}`);
    }
    return toBinary(
      WireFrameSchema,
      create(WireFrameSchema, {
        body: {
          case: "notification",
          value: create(NotificationSchema, { payload }),
        },
      }),
    );
  },
  decode(payload) {
    const frame = fromBinary(WireFrameSchema, payloadBytes(payload));
    if (frame.body.case !== "notification") return null;
    switch (frame.body.value.payload.case) {
      case "accountSyncVersion": {
        const version = frame.body.value.payload.value.version;
        if (version > BigInt(Number.MAX_SAFE_INTEGER)) {
          throw new TypeError("wire: account.sync_version 超出安全整数范围");
        }
        return { type: AccountChannelSyncVersion, version: Number(version) };
      }
      case "accountMirrorChanged":
        return { type: AccountChannelMirrorChanged, version: 0 };
      case "accountDevicePresence":
        return { type: AccountChannelDevicePresence, version: 0 };
      default:
        return null;
    }
  },
};
