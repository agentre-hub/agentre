/**
 * wire 编解码与 Go 侧黄金样本的逐字段同构测试(测试接缝 1)。
 *
 * 黄金样本与被测的 codec 由同一个 Go 生成器产出(wire 包的 golden_test.go /
 * tsgen_test.go),分别落在本包的 fixtures/ 与 src/*.gen.ts。本测试断言:TS 编解码
 * 对同一批帧解出的结构,与 Go 序列化出来的字节**逐字段一致**;并且带未知字段的帧在
 * decode → encode 往返后未知字段不丢。
 */
import { describe, expect, it } from "vitest";
import { create, toBinary } from "@bufbuild/protobuf";

import {
  ProtobufRpcCodec,
  encodeRpcCancel,
  encodeRpcMethodRequest,
  rpcMethods,
  type WireObject,
  MethodSteer,
  NotifyEvent,
  decodeEventFrame,
  decodeSteerParams,
  encodeSteerParams,
  decodeJournaledNotification,
  decodeRunAck,
  decodeRunParams,
  decodeRunResultDoneFrame,
  decodeSessionAttachParams,
  decodeSessionAttachResult,
  decodeSessionListResult,
  decodeSessionPendingWaitersParams,
  decodeSessionPendingWaitersResult,
  decodeSessionPullParams,
  decodeSessionPullResult,
  decodeSessionSummary,
  decodeAutonomousTurnStartedFrame,
  decodeUsageWire,
  encodeEventFrame,
  encodeRunAck,
  encodeRunParams,
  encodeSessionAttachParams,
  encodeSessionPendingWaitersParams,
  encodeSessionPullParams,
  encodeSessionPullResult,
  encodeRunResultDoneFrame,
} from "../index";
import {
  CancelSchema,
  RpcErrorSchema,
  RpcFrameSchema,
  RpcMethod,
  type SessionPullResponse,
} from "../gen/agentre/wire/wire_pb";

import runParamsFixture from "../../fixtures/run-params.json";
import runAckFixture from "../../fixtures/run-ack.json";
import sessionSummaryFixture from "../../fixtures/session-summary.json";
import sessionSummaryLegacyFixture from "../../fixtures/session-summary-legacy.json";
import sessionListResultFixture from "../../fixtures/session-list-result.json";

import sessionPullParamsFixture from "../../fixtures/session-pull-params.json";
import sessionPullResultFixture from "../../fixtures/session-pull-result.json";
import journaledNotificationFixture from "../../fixtures/journaled-notification.json";
import sessionAttachParamsFixture from "../../fixtures/session-attach-params.json";
import sessionAttachResultFixture from "../../fixtures/session-attach-result.json";
import sessionPendingWaitersParamsFixture from "../../fixtures/session-pending-waiters-params.json";
import sessionPendingWaitersResultFixture from "../../fixtures/session-pending-waiters-result.json";
import eventFrameFixture from "../../fixtures/event-frame.json";
import runResultDoneFrameFixture from "../../fixtures/run-result-done-frame.json";
import usageWireFixture from "../../fixtures/usage-wire.json";
import autonomousTurnStartedFixture from "../../fixtures/autonomous-turn-started.json";
import runParamsExtraFixture from "../../fixtures/run-params-extra.json";
import runParamsFreshFixture from "../../fixtures/run-params-fresh.json";
import sessionPullResultExtraFixture from "../../fixtures/session-pull-result-extra.json";


// 线上对话身份是 uuid;这些用例要证的是"同一个值原样往返",取一个可读的固定值。
const CONVERSATION_ID = "00000000-0000-7000-8000-000000000042";

describe("protobuf rpc envelope", () => {
  it("given an RPC error with binary details, when decoded, then its typed fields and bytes are preserved", () => {
    const payload = toBinary(
      RpcFrameSchema,
      create(RpcFrameSchema, {
        id: 7n,
        body: {
          case: "error",
          value: create(RpcErrorSchema, {
            code: -32602,
            message: "invalid params",
            details: new Uint8Array([0x00, 0x7f, 0xff]),
          }),
        },
      }),
    );

    expect(ProtobufRpcCodec.decode(payload)).toEqual({
      id: 7n,
      body: {
        case: "error",
        code: -32602,
        message: "invalid params",
        details: new Uint8Array([0x00, 0x7f, 0xff]),
      },
    });
  });

  it("given cancellation, when encoded through the public API, then the request ID round-trips independently of the frame ID", () => {
    const payload = encodeRpcCancel(8n, 7n);

    expect(ProtobufRpcCodec.decode(payload)).toEqual({
      id: 8n,
      body: { case: "cancel", requestId: 7n },
    });
    expect(
      toBinary(
        RpcFrameSchema,
        create(RpcFrameSchema, {
          id: 8n,
          body: {
            case: "cancel",
            value: create(CancelSchema, { requestId: 7n }),
          },
        }),
      ),
    ).toEqual(payload);
  });

  it.each([
    ["a frame without a body", new Uint8Array([0x08, 0x01])],
    ["malformed protobuf", new Uint8Array([0xff])],
  ])(
    "given %s, when decoded, then it is rejected instead of being misclassified",
    (_name, payload) => {
      expect(() => ProtobufRpcCodec.decode(payload)).toThrow();
    },
  );

  it("given LAN authentication methods, when referenced by clients, then their IDs are stable and append-only", () => {
    expect(RpcMethod.AUTH_PAIR).toBe(38);
    expect(RpcMethod.AUTH_CONNECT).toBe(39);
    expect(RpcMethod.AUTH_REVOKE).toBe(40);
  });

  it("given daemon management methods, when referenced by clients, then their IDs are stable and append-only", () => {
    expect(RpcMethod.LLM_UPSERT).toBe(41);
    expect(RpcMethod.LLM_DELETE).toBe(42);
    expect(RpcMethod.LLM_LIST).toBe(43);
    expect(RpcMethod.ENGINE_TEST).toBe(44);
    expect(RpcMethod.ENGINE_DISCOVER).toBe(45);
    expect(RpcMethod.ENGINE_SCAN).toBe(46);
    expect(RpcMethod.CLI_RESOLVE_PATH).toBe(47);
    expect(RpcMethod.CLI_PROBE).toBe(48);
    expect(RpcMethod.HEALTH_PING).toBe(49);
    expect(RpcMethod.CLAUDE_CODE_USAGE).toBe(50);
    expect(RpcMethod.SKILLS_LIST).toBe(51);
  });

  // 迁移自 oneof 分支时代的同名用例:钉的仍是「跨语言字节稳定」,只是搬到了生产
  // 唯一在走的 method_id + encoded_payload 路径上。Go 侧发同一帧必须逐字节一致,
  // 所以这里写死字节而不是只做往返。
  it("given auth.account, when encoded by method ID, then the request has stable cross-language bytes", () => {
    const payload = encodeRpcMethodRequest(1n, rpcMethods.authAccount, {
      credential: "jwt",
      deviceFingerprint: "fp",
    });

    expect(Array.from(payload)).toEqual([
      0x08, 0x01, 0x12, 0x0d, 0x08, 0x01, 0x12, 0x09, 0x0a, 0x03, 0x6a, 0x77,
      0x74, 0x12, 0x02, 0x66, 0x70,
    ]);
    const decoded = ProtobufRpcCodec.decode(payload);
    expect(decoded.id).toBe(1n);
    expect(decoded.body).toMatchObject({
      case: "typedMethodRequest",
      methodId: 1,
      method: "authAccount",
      value: expect.objectContaining({
        credential: "jwt",
        deviceFingerprint: "fp",
      }),
    });
  });

  it("given auth.account success, when encoded by method ID, then the response keeps the request id and its bytes", () => {
    const payload = ProtobufRpcCodec.encodeTypedMethodResponse(
      1n,
      rpcMethods.authAccount,
      { ok: true },
    );

    expect(Array.from(payload)).toEqual([
      0x08, 0x01, 0x1a, 0x06, 0x08, 0x01, 0x12, 0x02, 0x08, 0x01,
    ]);
    const decoded = ProtobufRpcCodec.decode(payload);
    expect(decoded.id).toBe(1n);
    expect(decoded.body).toMatchObject({
      case: "typedMethodResponse",
      methodId: 1,
      method: "authAccount",
      value: expect.objectContaining({ ok: true }),
    });
  });

  // 空载荷是这条路径的真实边界:请求/应答体一个字段都没有时 encoded_payload 是
  // 零长,protobuf 会整条省略,帧上只剩 method_id。收端仍必须解成带类型的帧,而
  // 不是「没有载荷 ⇒ 解不出」。
  it("given a payload-less method, when encoded by method ID, then the frame carries only the method id and still decodes typed", () => {
    const request = encodeRpcMethodRequest(2n, rpcMethods.sessionList, {});
    expect(Array.from(request)).toEqual([0x08, 0x02, 0x12, 0x02, 0x08, 0x02]);
    expect(ProtobufRpcCodec.decode(request).body).toMatchObject({
      case: "typedMethodRequest",
      methodId: 2,
      method: "sessionList",
    });

    const response = ProtobufRpcCodec.encodeTypedMethodResponse(
      2n,
      rpcMethods.sessionList,
      {},
    );
    expect(Array.from(response)).toEqual([0x08, 0x02, 0x1a, 0x02, 0x08, 0x02]);
    expect(ProtobufRpcCodec.decode(response).body).toMatchObject({
      case: "typedMethodResponse",
      methodId: 2,
      method: "sessionList",
      value: expect.objectContaining({
        sessions: [],
      }),
    });
  });

  // 补齐分页里的日志条目必须仍是**带类型**的 RpcNotification 而不是不透明字节 ——
  // agentre-server 的 relayClient.pullUntilCaughtUp 正是这样读它的
  // (journaledFromProtobuf 直接吃 notifications[].payload)。会拒绝的错误实现:
  // 把 JournaledNotification.payload 改回 bytes。Go 侧同形回归在
  // protowire/session_test.go。
  it("given session.pull with a journaled text event, when decoded by method ID, then the journal payload is still a typed notification", () => {
    const response = ProtobufRpcCodec.encodeTypedMethodResponse(
      4n,
      rpcMethods.sessionPull,
      {
        notifications: [
          {
            seq: 7n,
            payload: {
              payload: {
                case: "runtimeEvent",
                value: {
                  conversationId: CONVERSATION_ID,
                  seq: 7n,
                  event: { case: "textDelta", value: { text: "hello" } },
                },
              },
            },
          },
        ],
        cursor: 7n,
        hasMore: false,
        oldestSeq: 1n,
      },
    );

    const decoded = ProtobufRpcCodec.decode(response);
    expect(decoded.id).toBe(4n);
    expect(decoded.body.case).toBe("typedMethodResponse");
    if (decoded.body.case !== "typedMethodResponse") return;
    const value = decoded.body.value as SessionPullResponse;
    expect(value.cursor).toBe(7n);
    expect(value.hasMore).toBe(false);
    expect(value.oldestSeq).toBe(1n);
    expect(value.notifications).toHaveLength(1);
    expect(value.notifications[0].seq).toBe(7n);

    const journaled = value.notifications[0].payload;
    expect(journaled?.payload.case).toBe("runtimeEvent");
    if (journaled?.payload.case !== "runtimeEvent") return;
    const runtimeEvent = journaled.payload.value;
    expect(runtimeEvent.conversationId).toBe(CONVERSATION_ID);
    expect(runtimeEvent.event.case).toBe("textDelta");
    if (runtimeEvent.event.case !== "textDelta") return;
    expect(runtimeEvent.event.value.text).toBe("hello");
  });

  // 实时通知帧不经 Request/Response,删掉 oneof 与它无关 —— 原样保留覆盖。
  it("given a live runtime event notification, when encoded, then it round-trips outside the request/response path", () => {
    const notification = {
      case: "runtimeEventNotification" as const,
      conversationId: CONVERSATION_ID,
      seq: 7,
      event: { case: "textDelta" as const, text: "hello" },
    };
    const live = ProtobufRpcCodec.encode({ id: 0n, body: notification });
    expect(ProtobufRpcCodec.decode(live)).toEqual({
      id: 0n,
      body: notification,
    });
  });

  it.each([
    { case: "thinkingDelta", text: "reasoning" },
    { case: "outputActivity" },
    { case: "permissionModeChanged", mode: "plan" },
    { case: "retry", message: "busy", details: "later", attempt: 2, max: 5 },
    { case: "contextWindowUpdated", tokens: 128000 },
    {
      case: "compactBoundary",
      preTokens: 90000,
      postTokens: 20000,
      trigger: "auto",
      durationMs: 123,
    },
    { case: "runtimeStatus", status: "compacting" },
    {
      case: "done",
      model: "",
      durationMs: 0,
      firstTokenMs: 0,
      tokensPerSec: 0,
    },
    { case: "error", message: "failed" },
    {
      case: "userMessage",
      text: "follow up",
      sourceDevice: "fp",
      sourceDeviceName: "Mac",
    },
  ] as const)("given scalar runtime event $case, it stays typed", (event) => {
    const frame = {
      id: 0n,
      body: {
        case: "runtimeEventNotification" as const,
        conversationId: CONVERSATION_ID,
        seq: 8,
        event,
      },
    };
    expect(ProtobufRpcCodec.decode(ProtobufRpcCodec.encode(frame))).toEqual(
      frame,
    );
  });

  it.each([
    {
      case: "runResultDoneNotification",
      conversationId: CONVERSATION_ID,
      seq: 9,
      providerSessionId: "sess",
      usage: {
        promptTokens: 10,
        completionTokens: 2,
        reasoningTokens: 1,
        cachedTokens: 3,
        cacheCreationTokens: 4,
        totalTokens: 20,
      },
      userAnchor: "anchor",
      model: "gpt",
      contextWindow: 128000,
      turnToken: 3n,
      stopErrorMessage: "",
      stopErrorCode: 0,
      durationMs: 0,
      firstTokenMs: 0,
      tokensPerSec: 0,
    },
    {
      case: "autonomousTurnStartedNotification",
      conversationId: CONVERSATION_ID,
      seq: 10,
      trigger: "hook",
      turnToken: 4n,
    },
  ] as const)("given runtime notification $case, it stays typed", (body) => {
    const frame = { id: 0n, body };
    expect(ProtobufRpcCodec.decode(ProtobufRpcCodec.encode(frame))).toEqual(
      frame,
    );
  });

  it("given run completion without usage, it preserves the optional field", () => {
    const frame = {
      id: 0n,
      body: {
        case: "runResultDoneNotification" as const,
        conversationId: CONVERSATION_ID,
        seq: 9,
        providerSessionId: "",
        userAnchor: "",
        model: "",
        contextWindow: 0,
        turnToken: 0n,
        stopErrorMessage: "aborted",
        stopErrorCode: -32013,
        durationMs: 0,
        firstTokenMs: 0,
        tokensPerSec: 0,
      },
    };
    expect(ProtobufRpcCodec.decode(ProtobufRpcCodec.encode(frame))).toEqual(
      frame,
    );
  });

  it.each([
    {
      case: "toolCall",
      id: "t1",
      name: "Read",
      input: new Uint8Array([1, 2]),
      canonical: new Uint8Array(),
      parentToolCallId: "",
      subagentRunId: "",
    },
    {
      case: "toolResult",
      toolCallId: "t1",
      content: "ok",
      isError: false,
      parentToolCallId: "",
      subagentRunId: "",
      meta: new Uint8Array([3]),
    },
    {
      case: "steerConsumed",
      steers: [
        { queuedId: "q1", text: "next", sourcePeer: "fp", sourceName: "Mac" },
      ],
    },
    {
      case: "userAskRequest",
      requestId: "r1",
      toolCallId: "t1",
      parentToolCallId: "",
      questions: [
        {
          id: "q",
          question: "Continue?",
          header: "Choice",
          multiSelect: false,
          isOther: false,
          isSecret: false,
          options: [{ label: "Yes", description: "continue", preview: "" }],
        },
      ],
    },
    {
      case: "userAskResolved",
      requestId: "r1",
      parentToolCallId: "",
      answers: [{ questionIndex: 0, labels: ["Yes"], otherText: "" }],
      skipped: false,
    },
    {
      case: "toolPermissionRequest",
      requestId: "p1",
      toolCallId: "t1",
      toolName: "Bash",
      input: new Uint8Array([4]),
    },
    {
      case: "toolPermissionResolved",
      requestId: "p1",
      allowed: true,
      alwaysAllow: false,
      denyReason: "",
    },
    {
      case: "execApprovalRequested",
      id: "a1",
      commandText: "ls",
      commandPreview: "ls",
      allowedDecisions: ["allow"],
      host: "local",
      nodeId: "n",
      agentId: "a",
      sessionKey: "s",
      createdAtMs: 1,
      expiresAtMs: 2,
    },
    {
      case: "execApprovalResolved",
      id: "a1",
      status: "resolved",
      decision: "allow",
      resolvedBy: "user",
      resolvedAtMs: 3,
    },
    {
      case: "subagentStarted",
      toolCallId: "t1",
      info: {
        taskId: "task",
        subagentType: "explore",
        kind: "local_agent",
        taskDescription: "inspect",
        prompt: "go",
        lastToolName: "Read",
        toolUses: 1,
        totalTokens: 2,
        durationMs: 3,
        status: "running",
        mode: "parallel",
        runs: [],
        summary: "task summary",
      },
    },
    {
      case: "subagentProgress",
      toolCallId: "t1",
      info: {
        taskId: "task",
        subagentType: "explore",
        kind: "local_agent",
        taskDescription: "inspect",
        prompt: "go",
        lastToolName: "Read",
        toolUses: 1,
        totalTokens: 2,
        durationMs: 3,
        status: "running",
        mode: "parallel",
        runs: [],
        summary: "task summary",
      },
    },
    {
      case: "subagentDone",
      toolCallId: "t1",
      info: {
        taskId: "task",
        subagentType: "explore",
        kind: "local_agent",
        taskDescription: "inspect",
        prompt: "go",
        lastToolName: "Read",
        toolUses: 1,
        totalTokens: 2,
        durationMs: 3,
        status: "completed",
        mode: "parallel",
        runs: [],
        summary: "task summary",
      },
    },
    { case: "subagentModel", toolCallId: "t1", model: "gpt" },
    {
      case: "usageUpdate",
      usage: {
        promptTokens: 10,
        completionTokens: 2,
        reasoningTokens: 1,
        cachedTokens: 3,
        cacheCreationTokens: 4,
        totalTokens: 20,
      },
      totalInputTokens: 17,
      contextWindow: 128000,
    },
    {
      case: "planUpdated",
      steps: [{ id: "1", step: "Implement", status: "inProgress" }],
      text: "# Plan",
      actions: [
        { id: "plan.execute", kind: "approve", requiresFeedback: false },
      ],
    },
  ] as const)(
    "given structured runtime event $case, it stays typed",
    (event) => {
      const frame = {
        id: 0n,
        body: {
          case: "runtimeEventNotification" as const,
          conversationId: CONVERSATION_ID,
          seq: 11,
          event,
        },
      };
      expect(ProtobufRpcCodec.decode(ProtobufRpcCodec.encode(frame))).toEqual(
        frame,
      );
    },
  );
});

/** decode → encode → parse 必须逐字段等于 Go 侧产出的原始帧;返回解码结果供定向断言。 */
function assertRoundTrip<T extends WireObject>(
  decode: (v: unknown) => T,
  encode: (v: T) => string,
  fixture: unknown,
  what: string,
): T {
  const decoded = decode(fixture);
  expect(
    JSON.parse(encode(decoded)),
    `${what} 往返后应与 Go 真实帧逐字段相同`,
  ).toEqual(fixture);
  return decoded;
}

describe("wire 编解码:与 Go 侧黄金样本逐字段同构", () => {
  it("RunParams 解出新字段(peerFingerprint/title/agentSyncId/providerSessionId/source*)", () => {
    const p = assertRoundTrip(
      decodeRunParams,
      encodeRunParams,
      runParamsFixture,
      "RunParams",
    );
    expect(p.conversationId).toBe(CONVERSATION_ID);
    expect(p.agentId).toBe(7);
    // R9:别的对端发起的那条会话上开新一轮时点名的 origin。
    expect(p.peerFingerprint).toBe("fp-desktop");
    expect(p.cwd).toBe("/home/agent/proj");
    expect(p.title).toBe("重构登录页");
    expect(p.agentSyncId).toBe("01JZ7W2A8KZ4R5T6Y7U8I9O0P1Q");
    expect(p.providerSessionId).toBe("sess_abc123");
    expect(p.sourceDevice).toBe("fp-web-1");
    expect(p.sourceDeviceName).toBe("Chrome · macOS");
    // 嵌套结构原样透传(mcpServers 在 Go 侧无 JSON tag,PascalCase 不动)。
    expect(p.mcpServers).toHaveLength(1);
    expect((p.mcpServers as Array<Record<string, unknown>>)[0].Name).toBe(
      "org",
    );
    expect(p.enabledPlugins).toEqual({
      "auto-continue": true,
      dangerous: false,
    });
  });

  it("RunParams 带未知字段:解码后仍在,往返不丢", () => {
    const p = assertRoundTrip(
      decodeRunParams,
      encodeRunParams,
      runParamsExtraFixture,
      "RunParams(extra)",
    );
    expect(p.futureField).toEqual({ nested: true });
    expect(p.clientNote).toBe("来自浏览器的自定义字段");
  });

  it("RunParams 解出 freshSession(挂账修复:regenerate 无锚点 / 会话失效恢复的显式全新信号)", () => {
    const p = assertRoundTrip(
      decodeRunParams,
      encodeRunParams,
      runParamsFreshFixture,
      "RunParams(fresh)",
    );
    expect(p.freshSession).toBe(true);
    expect(p.providerSessionId).toBeUndefined();
    expect(p.conversationId).toBe(CONVERSATION_ID);
  });

  it("RunAck 解出 providerSessionId 与回退信号", () => {
    const a = assertRoundTrip(
      decodeRunAck,
      encodeRunAck,
      runAckFixture,
      "RunAck",
    );
    expect(a.conversationId).toBe(CONVERSATION_ID);
    expect(a.providerSessionId).toBe("sess_abc123");
    expect(a.launchPermissionMode).toBe("default");
    expect(a.providerFallbackKey).toBe("key-fallback");
  });

  it("SessionSummary 解出 R7 新列;老会话如实留空(键缺失)", () => {
    const s = assertRoundTrip(
      decodeSessionSummary,
      (v) => JSON.stringify(v),
      sessionSummaryFixture,
      "SessionSummary",
    );
    expect(s.title).toBe("重构登录页");
    expect(s.agentSyncId).toBe("01JZ7W2A8KZ4R5T6Y7U8I9O0P1Q");
    expect(s.providerSessionId).toBe("sess_abc123");
    expect(s.lifecycleState).toBe("running");
    expect(s.waitingForInput).toBe(true);
    expect(s.latestSeq).toBe(12);
    // R5 的「最后活动时间」：唯一真相源在执行端那台机器上，随清单过线。
    expect(s.lastMessageAt).toBe(1754800000000);
    // 逐字段同构：它是被 codec 认识并校验的字段，不是漏进来的未知键。
    expect(() =>
      decodeSessionSummary({ ...sessionSummaryFixture, lastMessageAt: "昨天" }),
    ).toThrow(TypeError);

    const legacy = assertRoundTrip(
      decodeSessionSummary,
      (v) => JSON.stringify(v),
      sessionSummaryLegacyFixture,
      "SessionSummary(legacy)",
    );
    expect(legacy.title).toBeUndefined();
    expect(legacy.agentSyncId).toBeUndefined();
    expect(legacy.providerSessionId).toBeUndefined();
    expect(legacy.lifecycleState).toBe("idle");
    expect(legacy.peerFingerprint).toBe("fp-desktop");
    expect(legacy.lastMessageAt).toBeUndefined();
  });

  it("SessionListResult 逐条解出会话", () => {
    const r = assertRoundTrip(
      decodeSessionListResult,
      (v) => JSON.stringify(v),
      sessionListResultFixture,
      "SessionListResult",
    );
    expect(r.sessions).toHaveLength(2);
    expect(r.sessions[0].title).toBe("重构登录页");
    expect(r.sessions[1].title).toBeUndefined();
  });

  it("SessionPullParams 独占游标(首次 0)+ 限制", () => {
    const p = assertRoundTrip(
      decodeSessionPullParams,
      encodeSessionPullParams,
      sessionPullParamsFixture,
      "SessionPullParams",
    );
    expect(p.cursor).toBe(0);
    expect(p.limit).toBe(200);
  });

  it("JournaledNotification 的 params 不含 seq(补齐端自己盖)", () => {
    const n = assertRoundTrip(
      decodeJournaledNotification,
      (v) => JSON.stringify(v),
      journaledNotificationFixture,
      "JournaledNotification",
    );
    expect(n.seq).toBe(11);
    expect(n.method).toBe(NotifyEvent);
    const frame = decodeEventFrame(n.params);
    expect(frame.seq).toBeUndefined();
    expect(frame.conversationId).toBe(CONVERSATION_ID);
  });

  it("SessionPullResult 解出通知 / 游标 / HasMore / OldestSeq", () => {
    const r = assertRoundTrip(
      decodeSessionPullResult,
      encodeSessionPullResult,
      sessionPullResultFixture,
      "SessionPullResult",
    );
    expect(r.notifications).toHaveLength(2);
    expect(r.notifications?.[0].seq).toBe(11);
    expect(r.notifications?.[1].method).toBe("runtime.runResultDone");
    expect(r.cursor).toBe(12);
    expect(r.hasMore).toBe(false);
    expect(r.oldestSeq).toBe(1);
  });

  it("SessionPullResult 带未知字段:serverVersion 不丢", () => {
    const r = assertRoundTrip(
      decodeSessionPullResult,
      encodeSessionPullResult,
      sessionPullResultExtraFixture,
      "SessionPullResult(extra)",
    );
    expect(r.serverVersion).toBe("1.2.3");
  });

  it("attach 参数 / 结果", () => {
    const p = assertRoundTrip(
      decodeSessionAttachParams,
      encodeSessionAttachParams,
      sessionAttachParamsFixture,
      "SessionAttachParams",
    );
    expect(p.conversationId).toBe(CONVERSATION_ID);
    const r = assertRoundTrip(
      decodeSessionAttachResult,
      (v) => JSON.stringify(v),
      sessionAttachResultFixture,
      "SessionAttachResult",
    );
    expect(r.lifecycleState).toBe("running");
    expect(r.latestSeq).toBe(12);
    expect(r.backendType).toBe("claudecode");
  });

  it("pendingWaiters 参数 / 结果原样透传", () => {
    const p = assertRoundTrip(
      decodeSessionPendingWaitersParams,
      encodeSessionPendingWaitersParams,
      sessionPendingWaitersParamsFixture,
      "SessionPendingWaitersParams",
    );
    expect(p.conversationId).toBe(CONVERSATION_ID);
    const r = assertRoundTrip(
      decodeSessionPendingWaitersResult,
      (v) => JSON.stringify(v),
      sessionPendingWaitersResultFixture,
      "SessionPendingWaitersResult",
    );
    expect(r.toolPermissions).toHaveLength(1);
    expect(
      (r.toolPermissions as Array<Record<string, unknown>>)[0].RequestID,
    ).toBe("perm-1");
    expect(r.askUserQuestions).toHaveLength(1);
  });

  it("EventFrame 解出 sessionId / seq / 真实 event 载荷", () => {
    const f = assertRoundTrip(
      decodeEventFrame,
      encodeEventFrame,
      eventFrameFixture,
      "EventFrame",
    );
    expect(f.conversationId).toBe(CONVERSATION_ID);
    expect(f.seq).toBe(11);
    expect(f.event).toEqual({ kind: "text_delta", text: "你好" });
  });

  it("RunResultDoneFrame 解出 usage / 终态", () => {
    const f = assertRoundTrip(
      decodeRunResultDoneFrame,
      encodeRunResultDoneFrame,
      runResultDoneFrameFixture,
      "RunResultDoneFrame",
    );
    expect(f.conversationId).toBe(CONVERSATION_ID);
    expect(f.usage?.totalTokens).toBe(155);
    expect(f.model).toBe("claude-sonnet-4-5");
    expect(f.turnToken).toBe(9);
  });

  it("UsageWire 解出全部计数", () => {
    const u = assertRoundTrip(
      decodeUsageWire,
      (v) => JSON.stringify(v),
      usageWireFixture,
      "UsageWire",
    );
    expect(u.promptTokens).toBe(100);
    expect(u.totalTokens).toBe(155);
  });

  it("AutonomousTurnStartedFrame 解出 trigger / turnToken / seq", () => {
    const f = decodeAutonomousTurnStartedFrame(autonomousTurnStartedFixture);
    expect(f.conversationId).toBe(CONVERSATION_ID);
    expect(f.trigger).toBe("auto");
    expect(f.turnToken).toBe(9);
    expect(f.seq).toBe(13);
    // 往返同构。
    expect(JSON.parse(JSON.stringify(f))).toEqual(autonomousTurnStartedFixture);
  });

  it.each([
    {
      case: "autonomousTurnEventNotification",
      conversationId: CONVERSATION_ID,
      seq: 12,
      event: { case: "textDelta", text: "auto" },
    },
    {
      case: "autonomousTurnDoneNotification",
      conversationId: CONVERSATION_ID,
      seq: 13,
      providerSessionId: "sess",
      userAnchor: "",
      model: "gpt",
      contextWindow: 128000,
      turnToken: 4n,
      stopErrorMessage: "",
      stopErrorCode: 0,
      durationMs: 0,
      firstTokenMs: 0,
      tokensPerSec: 0,
    },
  ] as const)(
    "given autonomous notification $case, it does not collapse into the normal turn",
    (body) => {
      const frame = { id: 0n, body };
      expect(ProtobufRpcCodec.decode(ProtobufRpcCodec.encode(frame))).toEqual(
        frame,
      );
    },
  );
});

/**
 * SteerParams 手写形状契约（**不是** Go 生成的黄金样本）。
 *
 * golden_test.go 还没有为 SteerParams 出样本,而黄金样本的帧清单不属于本任务的
 * 改动范围。所以这一组照抄 wire.go:353-359 的 JSON tag 手写断言：
 *
 *	type SteerParams struct {
 *	    SessionID       int64  `json:"sessionId"`
 *	    PeerFingerprint string `json:"peerFingerprint,omitempty"`
 *	    QueuedID        string `json:"queuedId,omitempty"`
 *	    Text            string `json:"text"`
 *	}
 *
 * 等 golden_test.go 补上 steer-params.json 样本后，这一组应改成 assertRoundTrip。
 */
describe("SteerParams 形状契约(手写,待补黄金样本)", () => {
  it("方法名与 Go wire.MethodSteer 同源", () => {
    expect(MethodSteer).toBe("runtime.steer");
  });

  it("必填 sessionId / text；可选 peerFingerprint / queuedId 按 omitempty 省略", () => {
    const full = {
      conversationId: CONVERSATION_ID,
      peerFingerprint: "fp-desktop",
      queuedId: "q-1",
      text: "顺便把标题也改了",
    };
    const decoded = decodeSteerParams(full);
    expect(decoded.conversationId).toBe(CONVERSATION_ID);
    expect(decoded.text).toBe("顺便把标题也改了");
    expect(JSON.parse(encodeSteerParams(decoded))).toEqual(full);

    // omitempty：可选字段缺席时解码结果里也不该冒出这两个键。
    const minimal = { conversationId: CONVERSATION_ID, text: "继续" };
    expect(JSON.parse(encodeSteerParams(decodeSteerParams(minimal)))).toEqual(
      minimal,
    );
  });

  it("未知字段原样保留(与其它帧同一条纪律)", () => {
    const withExtra = { conversationId: CONVERSATION_ID, text: "继续", futureField: 7 };
    expect(JSON.parse(encodeSteerParams(decodeSteerParams(withExtra)))).toEqual(
      withExtra,
    );
  });

  it.each([
    ["sessionId 缺失", { text: "继续" }],
    ["sessionId 非数字", { sessionId: "42", text: "继续" }],
    ["text 缺失", { sessionId: 42 }],
    [
      "peerFingerprint 类型错",
      { conversationId: CONVERSATION_ID, text: "x", peerFingerprint: 1 },
    ],
  ])("%s → 解码报错", (_what, bad) => {
    expect(() => decodeSteerParams(bad)).toThrow(TypeError);
  });
});

/**
 * 一轮的统计（模型 · 耗时 · 首字 · 速率）在两个载体上各走一遍。
 *
 * 两个生产者各填自己填得起的那一个：桌面端 chat_svc 在 runtime 之上收口，直接填
 * `done` 事件；agentred 在事件流之上量表，知道结果时 `done` 早转发出去了，于是填
 * `runtime.runResultDone` 终态帧。`rpc.ts` 是这条 Protobuf 传输的**手写**层，漏一
 * 格的表现是静默丢字段 —— 转录上那一行 meta 空着，而 proto 与两侧 Go 都是对的。
 */
describe("turn stats on the wire", () => {
  it("given a done event carrying turn stats, it round-trips", () => {
    const frame = {
      id: 0n,
      body: {
        case: "runtimeEventNotification" as const,
        conversationId: CONVERSATION_ID,
        seq: 11,
        event: {
          case: "done" as const,
          model: "claude-sonnet-4-6",
          durationMs: 9640,
          firstTokenMs: 8010,
          tokensPerSec: 14.25,
        },
      },
    };
    expect(ProtobufRpcCodec.decode(ProtobufRpcCodec.encode(frame))).toEqual(
      frame,
    );
  });

  it("given a run result done frame carrying turn stats, it round-trips", () => {
    const frame = {
      id: 0n,
      body: {
        case: "runResultDoneNotification" as const,
        conversationId: CONVERSATION_ID,
        seq: 12,
        providerSessionId: "",
        userAnchor: "",
        model: "glm-5.3",
        contextWindow: 0,
        turnToken: 0n,
        stopErrorMessage: "",
        stopErrorCode: 0,
        durationMs: 9640,
        firstTokenMs: 8010,
        tokensPerSec: 14.25,
      },
    };
    expect(ProtobufRpcCodec.decode(ProtobufRpcCodec.encode(frame))).toEqual(
      frame,
    );
  });
});
