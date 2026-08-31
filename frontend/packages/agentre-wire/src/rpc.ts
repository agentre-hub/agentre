import {
  create,
  fromBinary,
  toBinary,
  type MessageInitShape,
} from "@bufbuild/protobuf";
import * as pb from "./gen/agentre/wire/wire_pb";
import {
  decodeTypedMethodFrame,
  encodeRpcMethodResponse,
  type AnyRpcMethod,
  type TypedMethodFrame,
} from "./rpc-methods";

import {
  RpcFrameSchema,
  CancelSchema,
  RpcNotificationSchema,
  RuntimeEventNotificationSchema,
  TextDeltaSchema,
  ThinkingDeltaSchema,
  OutputActivitySchema,
  PermissionModeChangedSchema,
  RetrySchema,
  ContextWindowUpdatedSchema,
  CompactBoundarySchema,
  RuntimeStatusSchema,
  DoneSchema,
  ErrorEventSchema,
  UserMessageSchema,
  UsageSchema,
  RunResultDoneNotificationSchema,
  AutonomousTurnStartedNotificationSchema,
} from "./gen/agentre/wire/wire_pb";

export interface RuntimeEventNotificationFrame {
  case: "runtimeEventNotification";
  conversationId: string;
  seq: number;
  event:
    | { case: "textDelta"; text: string }
    | { case: "thinkingDelta"; text: string }
    | { case: "outputActivity" }
    | { case: "permissionModeChanged"; mode: string }
    | {
        case: "retry";
        message: string;
        details: string;
        attempt: number;
        max: number;
      }
    | { case: "contextWindowUpdated"; tokens: number }
    | {
        case: "compactBoundary";
        preTokens: number;
        postTokens: number;
        trigger: string;
        durationMs: number;
      }
    | { case: "runtimeStatus"; status: string }
    | {
        case: "done";
        model: string;
        durationMs: number;
        firstTokenMs: number;
        tokensPerSec: number;
      }
    | { case: "error"; message: string }
    | {
        case: "userMessage";
        text: string;
        sourceDevice: string;
        sourceDeviceName: string;
      }
    | StructuredRuntimeEvent;
}

export type StructuredRuntimeEvent = {
  case:
    | "toolCall"
    | "toolResult"
    | "steerConsumed"
    | "userAskRequest"
    | "userAskResolved"
    | "toolPermissionRequest"
    | "toolPermissionResolved"
    | "execApprovalRequested"
    | "execApprovalResolved"
    | "subagentStarted"
    | "subagentProgress"
    | "subagentDone"
    | "subagentModel"
    | "usageUpdate"
    | "planUpdated"
    | "unrecognizedBlock";
} & Record<string, unknown>;

export interface RpcNotificationFrame {
  id: bigint;
  body: RpcNotificationBody;
}

export interface RpcErrorFrame {
  id: bigint;
  body: {
    case: "error";
    code: number;
    message: string;
    details: Uint8Array;
  };
}

export interface RpcCancelFrame {
  id: bigint;
  body: { case: "cancel"; requestId: bigint };
}

export interface RpcUsage {
  promptTokens: number;
  completionTokens: number;
  reasoningTokens: number;
  cachedTokens: number;
  cacheCreationTokens: number;
  totalTokens: number;
}
export interface RunResultDoneNotificationFrame {
  case: "runResultDoneNotification";
  conversationId: string;
  seq: number;
  providerSessionId: string;
  usage?: RpcUsage;
  userAnchor: string;
  model: string;
  contextWindow: number;
  turnToken: bigint;
  stopErrorMessage: string;
  stopErrorCode: number;
  durationMs: number;
  firstTokenMs: number;
  tokensPerSec: number;
}
export interface AutonomousTurnStartedNotificationFrame {
  case: "autonomousTurnStartedNotification";
  conversationId: string;
  seq: number;
  trigger: string;
  turnToken: bigint;
}
export interface AutonomousTurnEventNotificationFrame extends Omit<
  RuntimeEventNotificationFrame,
  "case"
> {
  case: "autonomousTurnEventNotification";
}
export interface AutonomousTurnDoneNotificationFrame extends Omit<
  RunResultDoneNotificationFrame,
  "case"
> {
  case: "autonomousTurnDoneNotification";
}
export type RpcNotificationBody =
  | RuntimeEventNotificationFrame
  | RunResultDoneNotificationFrame
  | AutonomousTurnStartedNotificationFrame
  | AutonomousTurnEventNotificationFrame
  | AutonomousTurnDoneNotificationFrame;

export type ProtobufRpcFrame =
  | RpcNotificationFrame
  | RpcErrorFrame
  | RpcCancelFrame
  | TypedMethodFrame;

export function encodeRpcCancel(id: bigint, requestId: bigint): Uint8Array {
  return toBinary(
    RpcFrameSchema,
    create(RpcFrameSchema, {
      id,
      body: {
        case: "cancel",
        value: create(CancelSchema, { requestId }),
      },
    }),
  );
}

function encodeStructuredEvent(event: StructuredRuntimeEvent) {
  switch (event.case) {
    case "toolCall":
      return {
        case: event.case,
        value: create(pb.ToolCallSchema, event as never),
      };
    case "toolResult":
      return {
        case: event.case,
        value: create(pb.ToolResultSchema, event as never),
      };
    case "steerConsumed":
      return {
        case: event.case,
        value: create(pb.SteerConsumedSchema, event as never),
      };
    case "userAskRequest":
      return {
        case: event.case,
        value: create(pb.UserAskRequestSchema, event as never),
      };
    case "userAskResolved":
      return {
        case: event.case,
        value: create(pb.UserAskResolvedSchema, event as never),
      };
    case "toolPermissionRequest":
      return {
        case: event.case,
        value: create(pb.ToolPermissionRequestSchema, event as never),
      };
    case "toolPermissionResolved":
      return {
        case: event.case,
        value: create(pb.ToolPermissionResolvedSchema, event as never),
      };
    case "execApprovalRequested":
      return {
        case: event.case,
        value: create(pb.ExecApprovalRequestedSchema, {
          ...event,
          createdAtMs: BigInt(event.createdAtMs as number),
          expiresAtMs: BigInt(event.expiresAtMs as number),
        } as never),
      };
    case "execApprovalResolved":
      return {
        case: event.case,
        value: create(pb.ExecApprovalResolvedSchema, {
          ...event,
          resolvedAtMs: BigInt(event.resolvedAtMs as number),
        } as never),
      };
    case "subagentStarted":
      return {
        case: event.case,
        value: create(pb.SubagentEventSchema, event as never),
      };
    case "subagentProgress":
      return {
        case: event.case,
        value: create(pb.SubagentEventSchema, event as never),
      };
    case "subagentDone":
      return {
        case: event.case,
        value: create(pb.SubagentEventSchema, event as never),
      };
    case "subagentModel":
      return {
        case: event.case,
        value: create(pb.SubagentModelSchema, event as never),
      };
    case "usageUpdate":
      return {
        case: event.case,
        value: create(pb.UsageUpdateSchema, event as never),
      };
    case "planUpdated":
      return {
        case: event.case,
        value: create(pb.PlanUpdatedSchema, event as never),
      };
    case "unrecognizedBlock":
      return {
        case: event.case,
        value: create(pb.UnrecognizedBlockSchema, event as never),
      };
  }
}

function plainProto(value: unknown): unknown {
  if (value instanceof Uint8Array) return value;
  if (Array.isArray(value)) return value.map(plainProto);
  if (typeof value !== "object" || value === null) return value;
  const out: Record<string, unknown> = {};
  for (const [key, item] of Object.entries(value)) {
    if (key !== "$typeName") out[key] = plainProto(item);
  }
  return out;
}

function encodeRuntimeEvent(value: RuntimeEventNotificationFrame) {
  const event = value.event;
  let encodedEvent;
  switch (event.case) {
    case "textDelta":
      encodedEvent = {
        case: event.case,
        value: create(TextDeltaSchema, event),
      };
      break;
    case "thinkingDelta":
      encodedEvent = {
        case: event.case,
        value: create(ThinkingDeltaSchema, event),
      };
      break;
    case "outputActivity":
      encodedEvent = { case: event.case, value: create(OutputActivitySchema) };
      break;
    case "permissionModeChanged":
      encodedEvent = {
        case: event.case,
        value: create(PermissionModeChangedSchema, event),
      };
      break;
    case "retry":
      encodedEvent = { case: event.case, value: create(RetrySchema, event) };
      break;
    case "contextWindowUpdated":
      encodedEvent = {
        case: event.case,
        value: create(ContextWindowUpdatedSchema, event),
      };
      break;
    case "compactBoundary":
      encodedEvent = {
        case: event.case,
        value: create(CompactBoundarySchema, event),
      };
      break;
    case "runtimeStatus":
      encodedEvent = {
        case: event.case,
        value: create(RuntimeStatusSchema, event),
      };
      break;
    case "done":
      encodedEvent = { case: event.case, value: create(DoneSchema, event) };
      break;
    case "error":
      encodedEvent = {
        case: event.case,
        value: create(ErrorEventSchema, event),
      };
      break;
    case "userMessage":
      encodedEvent = {
        case: event.case,
        value: create(UserMessageSchema, event),
      };
      break;
    default: {
      encodedEvent = encodeStructuredEvent(event);
      break;
    }
  }
  return create(RpcNotificationSchema, {
    payload: {
      case: "runtimeEvent",
      value: create(RuntimeEventNotificationSchema, {
        conversationId: value.conversationId,
        seq: BigInt(value.seq),
        event: encodedEvent,
      }),
    },
  });
}

function decodeRuntimeEvent(
  value: ReturnType<typeof encodeRuntimeEvent>,
): RuntimeEventNotificationFrame {
  if (value.payload.case !== "runtimeEvent") {
    throw new TypeError("wire: 未知 RPC 通知");
  }
  const event = value.payload.value.event;
  let decodedEvent: RuntimeEventNotificationFrame["event"];
  switch (event.case) {
    case "textDelta":
      decodedEvent = { case: event.case, text: event.value.text };
      break;
    case "thinkingDelta":
      decodedEvent = { case: event.case, text: event.value.text };
      break;
    case "outputActivity":
      decodedEvent = { case: event.case };
      break;
    case "permissionModeChanged":
      decodedEvent = { case: event.case, mode: event.value.mode };
      break;
    case "retry":
      decodedEvent = {
        case: event.case,
        message: event.value.message,
        details: event.value.details,
        attempt: event.value.attempt,
        max: event.value.max,
      };
      break;
    case "contextWindowUpdated":
      decodedEvent = { case: event.case, tokens: event.value.tokens };
      break;
    case "compactBoundary":
      decodedEvent = {
        case: event.case,
        preTokens: event.value.preTokens,
        postTokens: event.value.postTokens,
        trigger: event.value.trigger,
        durationMs: event.value.durationMs,
      };
      break;
    case "runtimeStatus":
      decodedEvent = { case: event.case, status: event.value.status };
      break;
    case "done":
      decodedEvent = {
        case: event.case,
        model: event.value.model,
        durationMs: event.value.durationMs,
        firstTokenMs: event.value.firstTokenMs,
        tokensPerSec: event.value.tokensPerSec,
      };
      break;
    case "error":
      decodedEvent = { case: event.case, message: event.value.message };
      break;
    case "userMessage":
      decodedEvent = {
        case: event.case,
        text: event.value.text,
        sourceDevice: event.value.sourceDevice,
        sourceDeviceName: event.value.sourceDeviceName,
      };
      break;
    default: {
      const fields = plainProto(event.value) as Record<string, unknown>;
      if (event.case === "execApprovalRequested") {
        fields.createdAtMs = safeNumber(
          fields.createdAtMs as bigint,
          "created_at_ms",
        );
        fields.expiresAtMs = safeNumber(
          fields.expiresAtMs as bigint,
          "expires_at_ms",
        );
      } else if (event.case === "execApprovalResolved") {
        fields.resolvedAtMs = safeNumber(
          fields.resolvedAtMs as bigint,
          "resolved_at_ms",
        );
      }
      decodedEvent = {
        case: event.case,
        ...fields,
      } as StructuredRuntimeEvent;
      break;
    }
  }
  return {
    case: "runtimeEventNotification",
    conversationId: value.payload.value.conversationId,
    seq: safeNumber(value.payload.value.seq, "seq"),
    event: decodedEvent,
  };
}

function encodeNotification(value: RpcNotificationBody) {
  if (value.case === "runtimeEventNotification")
    return encodeRuntimeEvent(value);
  if (value.case === "runResultDoneNotification") {
    return create(RpcNotificationSchema, {
      payload: {
        case: "runResultDone",
        value: create(RunResultDoneNotificationSchema, {
          ...value,
          conversationId: value.conversationId,
          seq: BigInt(value.seq),
          usage:
            value.usage === undefined
              ? undefined
              : create(UsageSchema, value.usage),
        }),
      },
    });
  }
  if (value.case === "autonomousTurnEventNotification") {
    const normal = encodeRuntimeEvent({
      ...value,
      case: "runtimeEventNotification",
    });
    if (normal.payload.case !== "runtimeEvent")
      throw new TypeError("wire: autonomous event 编码失败");
    return create(RpcNotificationSchema, {
      payload: { case: "autonomousTurnEvent", value: normal.payload.value },
    });
  }
  if (value.case === "autonomousTurnDoneNotification") {
    return create(RpcNotificationSchema, {
      payload: {
        case: "autonomousTurnDone",
        value: create(RunResultDoneNotificationSchema, {
          ...value,
          conversationId: value.conversationId,
          seq: BigInt(value.seq),
          usage:
            value.usage === undefined
              ? undefined
              : create(UsageSchema, value.usage),
        }),
      },
    });
  }
  return create(RpcNotificationSchema, {
    payload: {
      case: "autonomousTurnStarted",
      value: create(AutonomousTurnStartedNotificationSchema, {
        ...value,
        conversationId: value.conversationId,
        seq: BigInt(value.seq),
      }),
    },
  });
}

function decodeNotification(
  value: ReturnType<typeof encodeNotification>,
): RpcNotificationBody {
  if (value.payload.case === "runtimeEvent") return decodeRuntimeEvent(value);
  if (value.payload.case === "autonomousTurnEvent") {
    const normal = decodeRuntimeEvent(
      create(RpcNotificationSchema, {
        payload: { case: "runtimeEvent", value: value.payload.value },
      }),
    );
    return { ...normal, case: "autonomousTurnEventNotification" };
  }
  if (value.payload.case === "runResultDone") {
    const v = value.payload.value;
    return {
      case: "runResultDoneNotification",
      conversationId: v.conversationId,
      seq: safeNumber(v.seq, "seq"),
      providerSessionId: v.providerSessionId,
      ...(v.usage === undefined
        ? {}
        : {
            usage: {
              promptTokens: v.usage.promptTokens,
              completionTokens: v.usage.completionTokens,
              reasoningTokens: v.usage.reasoningTokens,
              cachedTokens: v.usage.cachedTokens,
              cacheCreationTokens: v.usage.cacheCreationTokens,
              totalTokens: v.usage.totalTokens,
            },
          }),
      userAnchor: v.userAnchor,
      model: v.model,
      contextWindow: v.contextWindow,
      turnToken: v.turnToken,
      stopErrorMessage: v.stopErrorMessage,
      stopErrorCode: v.stopErrorCode,
      durationMs: v.durationMs,
      firstTokenMs: v.firstTokenMs,
      tokensPerSec: v.tokensPerSec,
    };
  }
  if (value.payload.case === "autonomousTurnStarted") {
    const v = value.payload.value;
    return {
      case: "autonomousTurnStartedNotification",
      conversationId: v.conversationId,
      seq: safeNumber(v.seq, "seq"),
      trigger: v.trigger,
      turnToken: v.turnToken,
    };
  }
  if (value.payload.case === "autonomousTurnDone") {
    const v = value.payload.value;
    return {
      case: "autonomousTurnDoneNotification",
      conversationId: v.conversationId,
      seq: safeNumber(v.seq, "seq"),
      providerSessionId: v.providerSessionId,
      ...(v.usage === undefined
        ? {}
        : { usage: plainProto(v.usage) as RpcUsage }),
      userAnchor: v.userAnchor,
      model: v.model,
      contextWindow: v.contextWindow,
      turnToken: v.turnToken,
      stopErrorMessage: v.stopErrorMessage,
      stopErrorCode: v.stopErrorCode,
      durationMs: v.durationMs,
      firstTokenMs: v.firstTokenMs,
      tokensPerSec: v.tokensPerSec,
    };
  }
  throw new TypeError("wire: 未知 RPC 通知");
}

function safeNumber(value: bigint, field: string): number {
  if (
    value > BigInt(Number.MAX_SAFE_INTEGER) ||
    value < BigInt(Number.MIN_SAFE_INTEGER)
  ) {
    throw new TypeError(`wire: ${field} 超出安全整数范围`);
  }
  return Number(value);
}

export const ProtobufRpcCodec = {
  encodeTypedMethodResponse<M extends AnyRpcMethod>(
    id: bigint,
    method: M,
    value: MessageInitShape<M["response"]>,
  ): Uint8Array {
    return encodeRpcMethodResponse(id, method, value);
  },
  encode(frame: ProtobufRpcFrame): Uint8Array {
    if (
      frame.body.case === "runtimeEventNotification" ||
      frame.body.case === "runResultDoneNotification" ||
      frame.body.case === "autonomousTurnStartedNotification" ||
      frame.body.case === "autonomousTurnEventNotification" ||
      frame.body.case === "autonomousTurnDoneNotification"
    ) {
      return toBinary(
        RpcFrameSchema,
        create(RpcFrameSchema, {
          id: frame.id,
          body: { case: "notification", value: encodeNotification(frame.body) },
        }),
      );
    }
    if (
      frame.body.case === "typedMethodRequest" ||
      frame.body.case === "typedMethodResponse"
    ) {
      throw new TypeError(
        "wire: typed method frame 必须通过 encodeRpcMethodRequest/Response 编码",
      );
    }
    if (frame.body.case === "cancel") {
      return encodeRpcCancel(frame.id, frame.body.requestId);
    }
    if (frame.body.case === "error") {
      throw new TypeError("wire: RPC error frame 只能由应答端编码");
    }
    // 请求/应答的载荷只有 method_id + encoded_payload 一条路,由
    // encodeRpcMethodRequest/Response 负责;这里穷尽后应当无剩余分支。
    const unreachable: never = frame.body;
    throw new TypeError(
      `wire: 无法编码的 RPC frame ${JSON.stringify(unreachable)}`,
    );
  },
  decode(payload: Uint8Array): ProtobufRpcFrame {
    const frame = fromBinary(RpcFrameSchema, payload);
    const typed = decodeTypedMethodFrame(frame);
    if (typed !== undefined) return typed;
    if (frame.body.case === "error") {
      return {
        id: frame.id,
        body: {
          case: "error",
          code: frame.body.value.code,
          message: frame.body.value.message,
          details: frame.body.value.details,
        },
      };
    }
    if (frame.body.case === "cancel") {
      return {
        id: frame.id,
        body: { case: "cancel", requestId: frame.body.value.requestId },
      };
    }
    if (frame.body.case === "notification") {
      return { id: frame.id, body: decodeNotification(frame.body.value) };
    }
    // request / response 只在 method_id 为 0 时走到这里 —— 没有对端会那样发帧。
    throw new TypeError(
      `wire: 无法解码的 RPC frame ${frame.body.case === undefined ? "(空 body)" : frame.body.case}`,
    );
  },
};
