import type {
  RuntimeRunResponse,
  SessionCountsResponse,
  SessionListResponse,
} from "./gen/agentre/wire/wire_pb";
import {
  decodeRunAck,
  decodeSessionListResult,
  type RunAck,
  type SessionListResult,
} from "./codec.gen";

function safeNumber(value: bigint | number, what: string): number {
  const result = Number(value);
  if (!Number.isSafeInteger(result)) {
    throw new TypeError(`wire: ${what} is not a safe integer`);
  }
  return result;
}

/** 把真实 Protobuf runtime.run 应答转成页面使用的 domain wire。 */
export function runAckFromProtobuf(value: RuntimeRunResponse): RunAck {
  return decodeRunAck({
    conversationId: value.conversationId,
    ...(value.providerSessionId
      ? { providerSessionId: value.providerSessionId }
      : {}),
    ...(value.launchPermissionMode
      ? { launchPermissionMode: value.launchPermissionMode }
      : {}),
    ...(value.providerFallbackKey
      ? { providerFallbackKey: value.providerFallbackKey }
      : {}),
  });
}

/**
 * 把真实 Protobuf session.counts 应答转成三个普通数字。
 *
 * 它不过 codec.gen 的解码器:那份产物是按 wire.go 的结构生成的,而这三个数在线上
 * 就是三个 int64,除了「别悄悄丢精度」之外没有别的契约。
 */
export function sessionCountsFromProtobuf(value: SessionCountsResponse): {
  total: number;
  running: number;
  waiting: number;
} {
  return {
    total: safeNumber(value.total, "SessionCountsResponse.total"),
    running: safeNumber(value.running, "SessionCountsResponse.running"),
    waiting: safeNumber(value.waiting, "SessionCountsResponse.waiting"),
  };
}

/** 把真实 Protobuf session.list 应答转成页面使用的 domain wire。 */
export function sessionListFromProtobuf(
  value: SessionListResponse,
): SessionListResult {
  return decodeSessionListResult({
    sessions: value.sessions.map((session) => ({
      conversationId: session.conversationId,
      lifecycleState: session.lifecycleState,
      latestSeq: safeNumber(session.latestSeq, "SessionSummary.latestSeq"),
      ...(session.peerFingerprint
        ? { peerFingerprint: session.peerFingerprint }
        : {}),
      ...(session.agentId !== undefined && session.agentId !== 0n
        ? { agentId: safeNumber(session.agentId, "SessionSummary.agentId") }
        : {}),
      ...(session.title ? { title: session.title } : {}),
      ...(session.agentSyncId ? { agentSyncId: session.agentSyncId } : {}),
      ...(session.providerSessionId
        ? { providerSessionId: session.providerSessionId }
        : {}),
      ...(session.cwd ? { cwd: session.cwd } : {}),
      ...(session.projectSyncId
        ? { projectSyncId: session.projectSyncId }
        : {}),
      ...(session.backendType ? { backendType: session.backendType } : {}),
      ...(session.waitingForInput ? { waitingForInput: true } : {}),
      ...(session.lastMessageAt !== undefined && session.lastMessageAt !== 0n
        ? {
            lastMessageAt: safeNumber(
              session.lastMessageAt,
              "SessionSummary.lastMessageAt",
            ),
          }
        : {}),
      ...(session.providerKey ? { providerKey: session.providerKey } : {}),
      ...(session.modelKey ? { modelKey: session.modelKey } : {}),
    })),
    // 翻页那三格。零值一律不填:不认得分页的老对端解出来就是零值,而把 total 写成
    // 0 会让调用方的「查看全部 N」变成「查看全部 0」——「没说」和「一条都没有」
    // 不是一回事,前者该退回调用方手上的条数。
    ...(value.cursor ? { cursor: value.cursor } : {}),
    ...(value.hasMore ? { hasMore: true } : {}),
    ...(value.total !== undefined && value.total !== 0n
      ? { total: safeNumber(value.total, "SessionListResponse.total") }
      : {}),
  });
}
