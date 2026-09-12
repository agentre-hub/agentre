import { useSessionStatusStore } from "@/stores/session-status-store";

import type { ChatImageAttachment } from "../chat";
import type { AgentStatus } from "../types";

import type { chat_svc } from "../../../../wailsjs/go/models";

type SvcChatMessage = chat_svc.ChatMessage;

// ─── Optimistic message helpers ─────────────────────────────────────────────

function textOfChatMessage(m: SvcChatMessage): string {
  for (const b of m.blocks ?? []) {
    if ((b as { type?: string }).type === "text") {
      return (b as { text?: string }).text ?? "";
    }
  }
  return "";
}

function optimisticUser(
  id: number,
  sid: number,
  text: string,
  images: ChatImageAttachment[] = [],
): SvcChatMessage {
  const blocks: Array<Record<string, unknown>> = [];
  if (text) blocks.push({ type: "text", text });
  for (const image of images) {
    blocks.push({
      type: "image",
      image: {
        dataUrl: image.dataUrl,
        mediaType: image.mediaType,
        name: image.name,
      },
    });
  }
  return {
    id,
    sessionId: sid,
    role: "user",
    blocks,
    model: "",
    promptTokens: 0,
    completionTokens: 0,
    durationMs: 0,
    errorText: "",
    seq: 0,
    createtime: Date.now(),
  } as unknown as SvcChatMessage;
}

// markSessionStatus 乐观补一刀 session 状态 —— 后端在这两条路上都没有
// session_status 事件可跟, 不补的话 tab / toolbar / sidebar 读
// session-status-store 会一直停在上一状态:
//   - "running": Send / Regenerate / Edit 成功返回后。后端落库已经是 running,
//     但 turn 起手不 emit session_status, 不补就停在 idle。
//   - "error": 自主续轮落库失败时后端只把 error 落了库、经会话级流推一条 error
//     事件(那条轮压根没有 per-turn 流), 不补就停在 running 空转。
// permissionMode 取 store 当前值, 避免覆盖刚 set 的 plan/default 等。
function markSessionStatus(sessionId: number, agentStatus: AgentStatus): void {
  if (!sessionId) return;
  const prev = useSessionStatusStore.getState().statuses.get(sessionId);
  useSessionStatusStore.getState().upsert(sessionId, {
    agentStatus,
    needsAttention: false,
    permissionMode: prev?.permissionMode,
  });
}

function markSessionRunning(sessionId: number): void {
  markSessionStatus(sessionId, "running");
}

function optimisticAssistantPlaceholder(
  id: number,
  sid: number,
): SvcChatMessage {
  return {
    id,
    sessionId: sid,
    role: "assistant",
    blocks: [],
    model: "",
    promptTokens: 0,
    completionTokens: 0,
    durationMs: 0,
    errorText: "",
    seq: 0,
    createtime: Date.now(),
  } as unknown as SvcChatMessage;
}

export {
  markSessionRunning,
  markSessionStatus,
  optimisticAssistantPlaceholder,
  optimisticUser,
  textOfChatMessage,
};
export type { SvcChatMessage };
