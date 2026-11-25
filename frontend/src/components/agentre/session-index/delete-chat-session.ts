import { useSessionRootStore } from "@/stores/session-root-store";
import { useSessionTerminalsStore } from "@/stores/session-terminals-store";

import { useWebPreviewStore } from "../tool-windows/web-preview-store";

import { DeleteChatSession } from "../../../../wailsjs/go/app/App";

/**
 * 删除一个会话，以及只属于它的前端资源：会话终端工具窗里的终端（标签随之消失，
 * 由本会话开的终端进程跟着结束——与关闭终端标签同一条路；后端删除会话时也会关掉
 * 它开的终端），以及它的工作根状态与网页预览标签。会话列表与对话页的「删除」都走
 * 这里，删除成功之后的清理只有这一处。删除失败时原样抛出，什么都不动。
 */
export async function deleteChatSession(sessionId: number): Promise<void> {
  await DeleteChatSession({ sessionId } as never);
  useSessionTerminalsStore.getState().closeSession(sessionId);
  useSessionRootStore.getState().forget(sessionId);
  useWebPreviewStore.getState().forget(sessionId);
}
