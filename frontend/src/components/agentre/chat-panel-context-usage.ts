import type { ChatStreamUsage } from "@/hooks/use-chat-stream";
import { computeContextUsage } from "@agentre-hub/agentre-ui";

import type { chat_svc } from "../../../wailsjs/go/models";

type SvcChatMessage = chat_svc.ChatMessage;

/**
 * Composer 底栏「上下文用量」的桌面适配层。
 *
 * 三条取值规则本身已搬进共享包（`computeContextUsage`，浏览器宿主同用一份）：
 * contextWindow 不为正就整块不渲染、流式 usage 优先、否则从尾部往前找首条带
 * token 的 assistant。这里只把 Wails 生成的 `chat_svc.ChatMessage` 与桌面独有的
 * 流式 usage 接上去 —— 浏览器宿主的会话镜像没有流式 usage 这条通道。
 */
export function computeComposerContextUsage(
  messages: SvcChatMessage[],
  contextWindow: number,
  liveUsage?: ChatStreamUsage | null,
): { used: number; max: number } | undefined {
  return computeContextUsage(messages, contextWindow, liveUsage);
}
