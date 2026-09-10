import type { ChatStreamEvent } from "@/hooks/use-chat-stream";
import { isNoticeOnlyMessage } from "@/lib/notice-message";
import type { LiveStream } from "@/stores/chat-streams-store";

import type { SvcChatMessage } from "./optimistic";

import type { TranscriptLiveContent } from "../chat";

function upsertMessage(
  messages: SvcChatMessage[],
  next: SvcChatMessage,
): SvcChatMessage[] {
  const updated = [...messages];
  const idx = updated.findIndex((m) => m.id === next.id);
  if (idx >= 0) updated[idx] = next;
  else updated.push(next);
  return updated;
}

function applySteerConsumed(
  messages: SvcChatMessage[],
  event: ChatStreamEvent,
): SvcChatMessage[] {
  const additions = [
    ...(event.userMessages ?? []),
    ...(event.assistantMessage ? [event.assistantMessage] : []),
  ];
  const additionIDs = new Set(additions.map((m) => m.id));
  const next = messages.filter((m) => !additionIDs.has(m.id));

  let anchorIdx = -1;
  if (event.previousAssistantMessage) {
    anchorIdx = next.findIndex(
      (m) => m.id === event.previousAssistantMessage!.id,
    );
    if (anchorIdx >= 0) {
      next[anchorIdx] = event.previousAssistantMessage;
    } else {
      next.push(event.previousAssistantMessage);
      anchorIdx = next.length - 1;
    }
  }

  const insertAt = anchorIdx >= 0 ? anchorIdx + 1 : next.length;
  next.splice(insertAt, 0, ...additions);
  return next;
}

// applyStreamError 把 error 事件的文案落到「出错的那一轮」。事件自带 final message
// 时调用方直接 upsertMessage，走不到这里。
function applyStreamError(
  messages: SvcChatMessage[],
  error?: string,
): SvcChatMessage[] {
  if (!error) return messages;

  // 末条**真实** assistant:供应商切换 notice 的旁白行跳过(与 use-chat-session /
  // ChatTranscript / 后端 lastTurnAssistantIndex 同一口径)。轮中切换供应商会把它排在
  // 在跑的那条之后,errorText 落到旁白行 = 出错的那一轮看着没出错、旁白行反而红了。
  let idx = -1;
  for (let i = messages.length - 1; i >= 0; i--) {
    if (isNoticeOnlyMessage(messages[i])) continue;
    if (messages[i].role === "assistant") {
      idx = i;
      break;
    }
  }
  if (idx < 0) return messages;

  const updated = [...messages];
  updated[idx] = { ...updated[idx], errorText: error } as SvcChatMessage;
  return updated;
}

// liveContentByMessageId 把该会话此刻在流的内容摊成「assistant 消息 id → 流式内容」。
// 渲染路径与补齐行数快照路径共用一份 —— 后者拿的是 store 的即时快照(见
// registerTranscriptRowCounter 处的注释),两处若各拼各的,数出来的行与画出来的行
// 就会在某个细节上分家。
function liveContentByMessageId(
  sessionStreams: ReadonlyMap<number, LiveStream> | null,
): Map<number, TranscriptLiveContent> {
  const out = new Map<number, TranscriptLiveContent>();
  if (!sessionStreams) return out;
  for (const s of sessionStreams.values()) {
    out.set(s.assistantMessageId, {
      liveTail: s.liveDelta,
      liveThinking: s.liveThinking,
      liveThinkingStartedAt: s.streamStartedAt,
      liveBlocks: s.liveBlocks,
      liveRetry: s.liveRetry,
      liveTurn: {
        startedAt: s.streamStartedAt,
        firstTokenAt: s.firstTokenAt,
        generationMs: s.generationMs,
        burstStartedAt: s.burstStartedAt,
        promptTokens: s.liveUsage?.promptTokens ?? 0,
        completionTokens: s.turnCompletionTokens,
        cachedTokens: s.liveUsage?.cachedTokens ?? 0,
        cacheCreationTokens: s.liveUsage?.cacheCreationTokens ?? 0,
        reasoningTokens: s.turnReasoningTokens,
        model: "",
        liveText: s.liveDelta,
      },
    });
  }
  return out;
}

export {
  applySteerConsumed,
  applyStreamError,
  liveContentByMessageId,
  upsertMessage,
};
