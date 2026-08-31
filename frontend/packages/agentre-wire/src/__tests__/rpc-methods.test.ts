import { describe, expect, it } from "vitest";

import {
  ProtobufRpcCodec,
  rpcMethods,
  encodeRpcMethodRequest,
  decodeRpcMethodResponse,
} from "../index";


// 线上对话身份是 uuid;这些用例要证的是"同一个值原样往返",取一个可读的固定值。
const CONVERSATION_ID = "00000000-0000-7000-8000-000000000042";

describe("typed protobuf RPC methods", () => {
  it("registers every stable production method ID exactly once", () => {
    expect(
      Object.values(rpcMethods)
        .map((method) => method.id)
        .sort((a, b) => a - b),
    ).toEqual(Array.from({ length: 51 }, (_, index) => index + 1));
  });
  it("encodes runtime.run by stable method ID without exposing payload bytes", () => {
    const payload = encodeRpcMethodRequest(9n, rpcMethods.runtimeRun, {
      conversationId: CONVERSATION_ID,
      userText: "hello",
    });
    expect(ProtobufRpcCodec.decode(payload)).toEqual({
      id: 9n,
      body: {
        case: "typedMethodRequest",
        methodId: 17,
        method: "runtimeRun",
        value: expect.objectContaining({ conversationId: CONVERSATION_ID, userText: "hello" }),
      },
    });
  });

  it("round-trips server production method families through typed descriptors", () => {
    const cases = [
      [rpcMethods.sessionPendingWaiters, { conversationId: CONVERSATION_ID }],
      [rpcMethods.setModelTarget, { conversationId: CONVERSATION_ID, providerKey: "p" }],
      [rpcMethods.runtimeCapabilities, { backendType: "claudecode" }],
      [rpcMethods.skillCatalog, { backendType: "claudecode" }],
      [rpcMethods.projectSetLocalPath, { projectSyncId: "p", path: "/tmp" }],
      [rpcMethods.remoteFsListDir, { path: "/tmp" }],
      [rpcMethods.workspaceFsReadFile, { root: "/tmp", relPath: "a.txt" }],
      [rpcMethods.engineDiscover, { providerKey: "p" }],
    ] as const;
    for (const [method, value] of cases) {
      const encoded = encodeRpcMethodRequest(1n, method, value);
      const decoded = ProtobufRpcCodec.decode(encoded);
      expect(decoded.body).toMatchObject({
        case: "typedMethodRequest",
        methodId: method.id,
        method: method.name,
      });
    }
  });

  it("decodes only the response schema paired with the requested method", () => {
    const encoded = ProtobufRpcCodec.encodeTypedMethodResponse(
      3n,
      rpcMethods.remoteFsMkdir,
      { path: "/tmp/new" },
    );
    expect(
      decodeRpcMethodResponse(encoded, rpcMethods.remoteFsMkdir),
    ).toMatchObject({
      path: "/tmp/new",
    });
    expect(() =>
      decodeRpcMethodResponse(encoded, rpcMethods.engineDiscover),
    ).toThrow(/method ID/);
  });
});
