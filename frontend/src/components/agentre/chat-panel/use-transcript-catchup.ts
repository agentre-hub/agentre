// 补齐落定后的「跳到最新」:转录行数上报与销账。
//
// 摘要由 ChatStreamsHost 在补齐落定那一发记下,这里只负责供转录行数、读与销账。
//
// 行数取数口:补齐窗口两端各调一次(掉线时快照、落定时做差),不是每帧算,所以现场 build
// 一次即可。两个数据源的取法不同:
//   - messages 走 ref —— 它只在 reload 落定时换,慢一拍也是同一份;
//   - 在流的内容直接读 store 的 getState() —— 补齐落定那一发连接态事件到达时,重放的内容
//     早已进了 store 但 React 还没重渲,吃渲染期的 liveByMessageId 会数到补齐前的旧值,
//     差出来恒等于 0。
// 这里不复现转录区的折叠(压缩前旧消息)与本地命令行:两者在窗口两端同增同减,做差时抵消,
// 补齐本身也产不出它们。
//
// 销账条件是「人回到了底部」而不是「点了控件」:自己滚回底部同样意味着补齐内容已经看过,
// 不销账的话下次往上翻会撞见一枚早就过期的控件。贴底时本就沿用既有的贴底跟随,控件也永远
// 不出现(渲染条件与销账条件是同一个 showBackToBottom)。
import * as React from "react";

import { buildTranscriptRows } from "@agentre-hub/agentre-ui";

import type { ChatMessage } from "@/hooks/use-chat-session";
import {
  sessionStreamMap,
  useChatStreamsStore,
} from "@/stores/chat-streams-store";

import {
  clearCatchUp,
  registerTranscriptRowCounter,
  useCatchUpSummary,
  type CatchUpSummary,
} from "../chat-panel-catchup-state";
import { liveContentByMessageId } from "./stream-view";

// EMPTY_AUTONOMOUS_IDS:行数快照不关心「哪条消息是自主续轮」——那只影响首行要不要
// 挂 banner,不改行数。渲染路径自己会算真值。
const EMPTY_AUTONOMOUS_IDS: ReadonlySet<number> = new Set<number>();

export function useTranscriptCatchUp({
  sessionId,
  messages,
  showBackToBottom,
}: {
  sessionId: number;
  messages: ChatMessage[];
  showBackToBottom: boolean;
}): CatchUpSummary | null {
  const messagesRef = React.useRef(messages);
  React.useEffect(() => {
    messagesRef.current = messages;
  }, [messages]);
  React.useEffect(() => {
    if (sessionId <= 0) return;
    return registerTranscriptRowCounter(
      sessionId,
      () =>
        buildTranscriptRows({
          displayMessages: messagesRef.current,
          autonomousIds: EMPTY_AUTONOMOUS_IDS,
          liveByMessageId: liveContentByMessageId(
            sessionStreamMap(useChatStreamsStore.getState(), sessionId),
          ),
        }).rows.length,
    );
  }, [sessionId]);

  const catchUp = useCatchUpSummary(sessionId);
  React.useEffect(() => {
    if (showBackToBottom || !catchUp) return;
    clearCatchUp(sessionId);
  }, [catchUp, sessionId, showBackToBottom]);

  return catchUp;
}
