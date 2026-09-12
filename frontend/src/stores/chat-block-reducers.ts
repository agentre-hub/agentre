// chat-block-reducers.ts holds the pure `(blocks, payload) => blocks` transforms that
// chat-streams-store.ts applies to a LiveStream's ChatBlockData[]. They carry no store
// wiring (no Date.now(), no zustand set()) — extracting them here is a prerequisite for
// a later task that unifies them with the equivalent reducers on the other frontend host.
//
// Every function here is pure over its `blocks` argument: given the same blocks and
// payload it returns the same result, and never mutates the input array. Functions that
// can be a no-op (no matching block, invalid payload) return `null` — the same sentinel
// chat-streams-store's `updateStream` already uses to skip a state update.
import type { chat_svc, view } from "../../wailsjs/go/models";
import type {
  ChatBlockData,
  ChatBlockSubagentData,
  ExecApprovalData,
  ToolApprovalData,
} from "./chat-streams-store";

/**
 * findLastBlockIndex 倒序找最近一个满足 pred 的 live block。同一 id 在一轮里通常
 * 只出现一次,但倒序更稳 —— 能正确处理流式过程中尚未匹配到的边界态。
 */
export function findLastBlockIndex(
  blocks: ChatBlockData[],
  pred: (b: ChatBlockData) => boolean,
): number {
  for (let i = blocks.length - 1; i >= 0; i--) {
    if (pred(blocks[i])) return i;
  }
  return -1;
}

/** replaceBlock 返回把第 idx 块换成 next 的新数组。 */
export function replaceBlock(
  blocks: ChatBlockData[],
  idx: number,
  next: ChatBlockData,
): ChatBlockData[] {
  const out = [...blocks];
  out[idx] = next;
  return out;
}

type LiveToolUseInput = Omit<ChatBlockData, "type" | "subagent"> & {
  subagent?: ChatBlockSubagentData;
};

/** appendToolUseBlock 把一个 tool_use block 追加到尾部。 */
export function appendToolUseBlock(
  blocks: ChatBlockData[],
  block: LiveToolUseInput,
): ChatBlockData[] {
  return [...blocks, { ...block, type: "tool_use" } as ChatBlockData];
}

/** appendToolResultBlock 把一个 tool_result block 追加到尾部。 */
export function appendToolResultBlock(
  blocks: ChatBlockData[],
  block: Omit<ChatBlockData, "type">,
): ChatBlockData[] {
  return [...blocks, { ...block, type: "tool_result" } as ChatBlockData];
}

/**
 * upsertPlanBlock 只保留本轮 turn 的最新一张 plan block:已有一张
 * canonical.kind==="plan.update" 的 plan block 就原地替换,否则追加。
 */
export function upsertPlanBlock(
  blocks: ChatBlockData[],
  text: string,
  canonical?: view.CanonicalDTO,
): ChatBlockData[] {
  const nextBlock: ChatBlockData = { type: "plan", text, canonical };
  const targetIdx = findLastBlockIndex(
    blocks,
    (b) => b.type === "plan" && b.canonical?.kind === "plan.update",
  );
  return targetIdx >= 0
    ? replaceBlock(blocks, targetIdx, nextBlock)
    : [...blocks, nextBlock];
}

/**
 * mergeSubagentMetaBlocks 把 subagent_started/progress/done/model 事件携带的元数据合并
 * 到对应外层 Agent tool_use block 上(按 toolUseId 匹配最近一个)。字段做浅 merge;runs
 * 是完整快照,出现时整段替换,undefined/省略时保留旧值。
 */
export function mergeSubagentMetaBlocks(
  blocks: ChatBlockData[],
  toolUseId: string,
  meta: ChatBlockSubagentData,
): ChatBlockData[] | null {
  if (!toolUseId) return null;
  const targetIdx = findLastBlockIndex(
    blocks,
    (b) => b.type === "tool_use" && b.toolUseId === toolUseId,
  );
  if (targetIdx < 0) return null;
  const target = blocks[targetIdx];
  const { runs, ...patch } = meta;
  const merged: ChatBlockData = {
    ...target,
    subagent: {
      ...(target.subagent ?? {}),
      ...patch,
      ...(runs !== undefined ? { runs } : {}),
    } as chat_svc.ChatBlockSubagent,
  };
  return replaceBlock(blocks, targetIdx, merged);
}

/**
 * markAskUserQuestionAnsweredBlocks 按 requestId 找到对应 block,更新
 * Answered/Answers/Skipped 字段(乐观更新,或后端 echo 回来的确认)。
 */
export function markAskUserQuestionAnsweredBlocks(
  blocks: ChatBlockData[],
  payload: chat_svc.ChatBlockAskUserQuestion,
  canonical?: view.CanonicalDTO,
): ChatBlockData[] | null {
  if (!payload || !payload.requestId) return null;
  const targetIdx = findLastBlockIndex(
    blocks,
    (b) =>
      b.type === "ask_user_question" &&
      b.askUserQuestion?.requestId === payload.requestId,
  );
  if (targetIdx < 0) return null;
  const existing = blocks[targetIdx];
  const merged: ChatBlockData = {
    ...existing,
    askUserQuestion: {
      ...(existing.askUserQuestion ?? payload),
      ...payload,
    } as chat_svc.ChatBlockAskUserQuestion,
    canonical: canonical ?? existing.canonical,
  };
  return replaceBlock(blocks, targetIdx, merged);
}

/** appendToolPermissionRequestBlock 追加一张工具审批卡片。 */
export function appendToolPermissionRequestBlock(
  blocks: ChatBlockData[],
  payload: chat_svc.ChatBlockToolPermission,
  canonical?: view.CanonicalDTO,
): ChatBlockData[] {
  return [
    ...blocks,
    {
      type: "tool_permission_request",
      toolPermission: payload,
      canonical,
    } as ChatBlockData,
  ];
}

/**
 * markToolPermissionResolvedBlocks 按 requestId 找到对应 block 更新决策态,并同步
 * canonical(乐观更新路径按 existing canonical 只推进 resolved/allowed/alwaysAllow 三个
 * 标志位;后端 echo 路径直接用调用方传入的整份新 canonical 覆盖)。
 */
export function markToolPermissionResolvedBlocks(
  blocks: ChatBlockData[],
  payload: chat_svc.ChatBlockToolPermission,
  canonical?: view.CanonicalDTO,
): ChatBlockData[] | null {
  if (!payload || !payload.requestId) return null;
  const targetIdx = findLastBlockIndex(
    blocks,
    (b) =>
      b.type === "tool_permission_request" &&
      b.toolPermission?.requestId === payload.requestId,
  );
  if (targetIdx < 0) return null;
  const existing = blocks[targetIdx];
  const mergedSidecar = {
    ...(existing.toolPermission ?? payload),
    ...payload,
  } as chat_svc.ChatBlockToolPermission;
  let mergedCanonical = canonical ?? existing.canonical;
  if (
    !canonical &&
    mergedCanonical?.kind === "tool.permission" &&
    mergedCanonical.toolPermission
  ) {
    mergedCanonical = {
      ...mergedCanonical,
      toolPermission: {
        ...mergedCanonical.toolPermission,
        resolved: !!payload.resolved,
        allowed: !!payload.allowed,
        alwaysAllow: !!payload.alwaysAllow,
      },
    } as view.CanonicalDTO;
  } else if (
    !canonical &&
    mergedCanonical?.kind === "plan.approve_request" &&
    mergedCanonical.planApprove
  ) {
    mergedCanonical = {
      ...mergedCanonical,
      planApprove: {
        ...mergedCanonical.planApprove,
        resolved: !!payload.resolved,
        allowed: !!payload.allowed,
      },
    } as view.CanonicalDTO;
  }
  const merged: ChatBlockData = {
    ...existing,
    toolPermission: mergedSidecar,
    canonical: mergedCanonical,
  };
  return replaceBlock(blocks, targetIdx, merged);
}

/** appendToolApprovalBlock 追加一张内置写工具审批卡片(status:"pending")。 */
export function appendToolApprovalBlock(
  blocks: ChatBlockData[],
  payload: ToolApprovalData,
): ChatBlockData[] {
  return [
    ...blocks,
    { type: "tool_approval", toolApproval: payload } as ChatBlockData,
  ];
}

/** markToolApprovalResolvedBlocks 按 toolApproval.requestId 找到对应 block 覆盖 status/result。 */
export function markToolApprovalResolvedBlocks(
  blocks: ChatBlockData[],
  payload: ToolApprovalData,
): ChatBlockData[] | null {
  if (!payload || !payload.requestId) return null;
  const targetIdx = findLastBlockIndex(
    blocks,
    (b) =>
      b.type === "tool_approval" &&
      b.toolApproval?.requestId === payload.requestId,
  );
  if (targetIdx < 0) return null;
  const existing = blocks[targetIdx];
  const merged: ChatBlockData = {
    ...existing,
    toolApproval: {
      ...(existing.toolApproval ?? payload),
      ...payload,
    } as ToolApprovalData,
  };
  return replaceBlock(blocks, targetIdx, merged);
}

/**
 * upsertExecApprovalBlock 按 payload.id 找到既有 exec_approval block 原地更新,否则追加
 * 一张新的。调用方须自行决定是否需要在追加分支前 flush 待定文字/思考——本函数只管
 * blocks 数组本身,不知道 LiveStream 的其余字段。
 */
export function upsertExecApprovalBlock(
  blocks: ChatBlockData[],
  payload: ExecApprovalData,
): ChatBlockData[] | null {
  if (!payload?.id) return null;
  const idx = findLastBlockIndex(
    blocks,
    (block) =>
      block.type === "exec_approval" && block.execApproval?.id === payload.id,
  );
  if (idx >= 0) {
    const existing = blocks[idx];
    return replaceBlock(blocks, idx, {
      ...existing,
      execApproval: {
        ...(existing.execApproval ?? payload),
        ...payload,
      } as ExecApprovalData,
    });
  }
  return [
    ...blocks,
    { type: "exec_approval", execApproval: payload } as ChatBlockData,
  ];
}

/** markExecApprovalResolvedBlocks 按 payload.id 找到对应 block 覆盖 execApproval。 */
export function markExecApprovalResolvedBlocks(
  blocks: ChatBlockData[],
  payload: ExecApprovalData,
): ChatBlockData[] | null {
  if (!payload?.id) return null;
  const targetIdx = findLastBlockIndex(
    blocks,
    (block) =>
      block.type === "exec_approval" && block.execApproval?.id === payload.id,
  );
  if (targetIdx < 0) return null;
  const existing = blocks[targetIdx];
  return replaceBlock(blocks, targetIdx, {
    ...existing,
    execApproval: {
      ...(existing.execApproval ?? payload),
      ...payload,
    } as ExecApprovalData,
  });
}

/** appendCompactBoundaryBlock 追加一个 compact_boundary block。 */
export function appendCompactBoundaryBlock(
  blocks: ChatBlockData[],
  compact: { preTokens?: number; trigger?: "auto" | "manual"; at: number },
): ChatBlockData[] {
  return [
    ...blocks,
    {
      type: "compact_boundary",
      compact: {
        preTokens: compact.preTokens,
        trigger: compact.trigger,
        at: compact.at,
      },
    } as ChatBlockData,
  ];
}
