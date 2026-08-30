import type {
  RuntimeRunResponse,
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
    sessionId: safeNumber(value.sessionId, "RunAck.sessionId"),
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

/** 把真实 Protobuf session.list 应答转成页面使用的 domain wire。 */
export function sessionListFromProtobuf(
  value: SessionListResponse,
): SessionListResult {
  return decodeSessionListResult({
    sessions: value.sessions.map((session) => ({
      sessionId: safeNumber(session.sessionId, "SessionSummary.sessionId"),
      lifecycleState: session.lifecycleState,
      latestSeq: safeNumber(session.latestSeq, "SessionSummary.latestSeq"),
      ...(session.peerFingerprint
        ? { peerFingerprint: session.peerFingerprint }
        : {}),
      ...(session.agentId !== 0n
        ? { agentId: safeNumber(session.agentId, "SessionSummary.agentId") }
        : {}),
      ...(session.title ? { title: session.title } : {}),
      ...(session.agentSyncId ? { agentSyncId: session.agentSyncId } : {}),
      ...(session.providerSessionId
        ? { providerSessionId: session.providerSessionId }
        : {}),
      ...(session.cwd ? { cwd: session.cwd } : {}),
      ...(session.projectSyncId ? { projectSyncId: session.projectSyncId } : {}),
      ...(session.backendType ? { backendType: session.backendType } : {}),
      ...(session.waitingForInput ? { waitingForInput: true } : {}),
      ...(session.lastMessageAt !== 0n
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
  });
}
