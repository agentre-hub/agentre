import { describe, expect, it } from "vitest";

import {
  ProtobufRpcCodec,
  rpcMethods,
  encodeRpcMethodRequest,
  decodeRpcMethodResponse,
} from "../index";
import { RpcMethod } from "../gen/agentre/wire/wire_pb";

// 线上对话身份是 uuid;这些用例要证的是"同一个值原样往返",取一个可读的固定值。
const CONVERSATION_ID = "00000000-0000-7000-8000-000000000042";

describe("typed protobuf RPC methods", () => {
  it("registers every stable production method ID exactly once", () => {
    // ID 是 proto 枚举里的全局稳定值,不是本表的下标:52–56(转录导入四条 + 活动
    // 汇总)至今只有桌面↔daemon 在走,浏览器侧没有调用方,所以这里是一段连号加上
    // 会话思考力度那条(57)。
    expect(
      Object.values(rpcMethods)
        .map((method) => method.id)
        .sort((a, b) => a - b),
    ).toEqual([...Array.from({ length: 51 }, (_, index) => index + 1), 57]);
  });
  // 这张表是手写的:id 写错不会被编译器发现,只会在对端解出「未知 method ID」时爆掉。
  // 新加的这条因此与生成的枚举直接对钉。
  it("pairs the session reasoning effort descriptor with its generated proto ID", () => {
    expect(rpcMethods.setSessionReasoningEffort.id).toBe(
      RpcMethod.SET_SESSION_REASONING_EFFORT,
    );
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
        value: expect.objectContaining({
          conversationId: CONVERSATION_ID,
          userText: "hello",
        }),
      },
    });
  });

  it("round-trips server production method families through typed descriptors", () => {
    const cases = [
      [rpcMethods.sessionPendingWaiters, { conversationId: CONVERSATION_ID }],
      [
        rpcMethods.setModelTarget,
        { conversationId: CONVERSATION_ID, providerKey: "p" },
      ],
      [rpcMethods.runtimeCapabilities, { backendType: "claudecode" }],
      // 会话思考力度:空串是**要写下去的值**(改回跟随后端配置),所以它必须能被
      // 独立编码进请求,而不是靠「省略即不改」。
      [
        rpcMethods.setSessionReasoningEffort,
        { conversationId: CONVERSATION_ID, reasoningEffort: "xhigh" },
      ],
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
