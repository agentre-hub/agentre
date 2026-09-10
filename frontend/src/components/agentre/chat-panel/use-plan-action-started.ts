// plan action 起跑:把这一轮的乐观占位与流登记塞进本地状态。
//
// 不做 memo:它经 useTranscriptCallbacks 的 useEvent 代理进转录(每渲染更新 ref、对外恒是
// 同一个稳定引用),父侧引用变不变都到不了行组件 —— 所以这里直接返回一个新闭包,
// 与原先写在组件体里逐字同义。
import * as React from "react";

import type { PlanActionStream } from "@agentre-hub/agentre-ui";

import type { ChatMessage } from "@/hooks/use-chat-session";
import type { Actions } from "@/stores/chat-streams/types";

import {
  markSessionRunning,
  optimisticAssistantPlaceholder,
  optimisticUser,
} from "./optimistic";

export function usePlanActionStarted({
  followTranscriptBottom,
  setMessages,
  openStream,
  onSidebarShouldReload,
}: {
  followTranscriptBottom: () => void;
  setMessages: React.Dispatch<React.SetStateAction<ChatMessage[]>>;
  openStream: Actions["openStream"];
  onSidebarShouldReload?: () => void;
}): (resp: PlanActionStream, userText: string) => void {
  return function handlePlanActionStarted(
    resp: PlanActionStream,
    userText: string,
  ) {
    if (!resp.stream || !resp.sessionId || !resp.assistantMessageId) return;
    followTranscriptBottom();
    setMessages((prev) => {
      const next = [...prev];
      if (!next.some((m) => m.id === resp.userMessageId)) {
        next.push(optimisticUser(resp.userMessageId, resp.sessionId, userText));
      }
      if (!next.some((m) => m.id === resp.assistantMessageId)) {
        next.push(
          optimisticAssistantPlaceholder(
            resp.assistantMessageId,
            resp.sessionId,
          ),
        );
      }
      return next;
    });
    markSessionRunning(resp.sessionId);
    openStream({
      name: resp.stream,
      sessionId: resp.sessionId,
      assistantMessageId: resp.assistantMessageId,
      streamStartedAt: Date.now(),
    });
    onSidebarShouldReload?.();
  };
}
