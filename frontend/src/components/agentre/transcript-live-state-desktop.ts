import type { TranscriptLiveState } from "@agentre-hub/agentre-ui";

import {
  hasSessionStream,
  useChatStreamsStore,
} from "@/stores/chat-streams-store";

import type { chat_svc } from "../../../wailsjs/go/models";

/**
 * 桌面端的转录实时流适配层 —— 共享包与 `chat-streams-store` 之间唯一的接缝。
 *
 * 与 `transcript-ports-desktop.ts` 同为模块级常量：包里 `useIsStreamActive`
 * 会把 `useIsStreamActive` 成员当 hook 调用，每次渲染换新对象会让 hook 的调用
 * 来源漂移。
 *
 * 读侧用 selector（流起停要触发重渲染），写侧用 `getState()`（乐观更新是动作，
 * 不需要订阅，也不该让调用方跟着 store 重渲染）。
 */
export const desktopTranscriptLiveState: TranscriptLiveState = {
  useIsStreamActive(sessionId) {
    // 这个成员按契约只在渲染期被当作 hook 调用（见包里的 live-state.tsx），
    // 所以在这里用 useChatStreamsStore 是合法的。
    return useChatStreamsStore((s) =>
      sessionId ? hasSessionStream(s, sessionId) : false,
    );
  },

  markToolPermissionResolved({
    sessionId,
    assistantMessageId,
    toolPermission,
  }) {
    useChatStreamsStore
      .getState()
      .markToolPermissionResolved(
        sessionId,
        assistantMessageId,
        toolPermission as chat_svc.ChatBlockToolPermission,
      );
  },
};
