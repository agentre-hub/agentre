import * as React from "react";
import { useTranslation } from "react-i18next";

import { splitErrorDetail } from "@/lib/error-detail";
import { useChatStreamsStore } from "@/stores/chat-streams-store";

import type { SetChatPanelNotice } from "./notice";
import {
  markSessionRunning,
  optimisticAssistantPlaceholder,
  optimisticUser,
  textOfChatMessage,
  type SvcChatMessage,
} from "./optimistic";

import {
  DeleteChatSession,
  EditChatMessage,
  RegenerateChatMessage,
  RenameChatSession,
} from "../../../../wailsjs/go/app/App";

type PendingRename = {
  id: number;
  draft: string;
};

// 「编辑用户消息」：点编辑后把目标消息文本直接载入 Composer。带 sessionId 在切换会话
// 时自动失效，免得弄个 useEffect 在会话切换时手动 setState 一遍。
type EditingMessage = {
  sessionId: number;
  messageId: number;
  text: string;
};

type UseMessageActionsOptions = {
  sessionId: number;
  messages: SvcChatMessage[];
  setMessages: React.Dispatch<React.SetStateAction<SvcChatMessage[]>>;
  isModeSwitchable: boolean;
  permissionModeValue: string;
  followTranscriptBottom: () => void;
  openStream: ReturnType<typeof useChatStreamsStore.getState>["openStream"];
  onSessionDeleted?: () => void;
  onSidebarShouldReload?: () => void;
  setNotice: SetChatPanelNotice;
};

type MessageActions = {
  pendingRegenId: number | null;
  setPendingRegenId: React.Dispatch<React.SetStateAction<number | null>>;
  handleRegenerate: (messageId: number) => void;
  confirmRegenerate: () => Promise<void>;
  pendingDeleteId: number | null;
  setPendingDeleteId: React.Dispatch<React.SetStateAction<number | null>>;
  handleDelete: (id: number) => void;
  confirmDelete: () => Promise<void>;
  pendingRename: PendingRename | null;
  setPendingRename: React.Dispatch<React.SetStateAction<PendingRename | null>>;
  confirmRename: () => Promise<void>;
  activeEditing: EditingMessage | null;
  setEditingMessage: React.Dispatch<
    React.SetStateAction<EditingMessage | null>
  >;
  handleEdit: (messageId: number) => void;
  confirmEdit: (newText: string) => Promise<void>;
};

// useMessageActions 收拢转录里针对单条消息 / 单个会话的那几个破坏性动作:重生成、
// 编辑、删除、改名。三者共用「确认弹窗 state + RPC + 乐观截断重排」这一套形状。
function useMessageActions({
  sessionId,
  messages,
  setMessages,
  isModeSwitchable,
  permissionModeValue,
  followTranscriptBottom,
  openStream,
  onSessionDeleted,
  onSidebarShouldReload,
  setNotice,
}: UseMessageActionsOptions): MessageActions {
  const { t } = useTranslation();

  const [pendingRegenId, setPendingRegenId] = React.useState<number | null>(
    null,
  );
  const [pendingDeleteId, setPendingDeleteId] = React.useState<number | null>(
    null,
  );
  // pendingRename 取代旧 window.prompt：null = 未弹窗；非空 = 显示 Dialog + Input。
  // draft 跟随用户在 Input 里的输入；提交时调 RenameChatSession，关闭时清空。
  const [pendingRename, setPendingRename] =
    React.useState<PendingRename | null>(null);
  const [editingMessage, setEditingMessage] =
    React.useState<EditingMessage | null>(null);
  const activeEditing =
    editingMessage && editingMessage.sessionId === sessionId
      ? editingMessage
      : null;

  function handleRegenerate(messageId: number) {
    if (!sessionId) return;
    setPendingRegenId(messageId);
  }

  async function confirmRegenerate() {
    const messageId = pendingRegenId;
    if (messageId == null || !sessionId) {
      setPendingRegenId(null);
      return;
    }
    setPendingRegenId(null);

    const snapshot = messages;
    const targetIdx = snapshot.findIndex((m) => m.id === messageId);
    if (targetIdx < 0) return;
    let userIdx = -1;
    for (let i = targetIdx - 1; i >= 0; i--) {
      if (snapshot[i].role === "user") {
        userIdx = i;
        break;
      }
    }
    if (userIdx < 0) return;
    const userText = textOfChatMessage(snapshot[userIdx]);

    try {
      followTranscriptBottom();
      const resp = await RegenerateChatMessage({
        sessionId,
        messageId,
        permissionMode: isModeSwitchable ? permissionModeValue : "",
      });
      markSessionRunning(resp.sessionId);
      openStream({
        name: resp.stream,
        sessionId: resp.sessionId,
        assistantMessageId: resp.assistantMessageId,
        streamStartedAt: Date.now(),
      });
      setMessages([
        ...snapshot.slice(0, userIdx),
        optimisticUser(resp.userMessageId, resp.sessionId, userText),
        optimisticAssistantPlaceholder(resp.assistantMessageId, resp.sessionId),
      ]);
      onSidebarShouldReload?.();
    } catch (e: unknown) {
      console.error("[chat] regenerate failed", e);
      const { msg, detail } = splitErrorDetail(e);
      setNotice({
        kind: "error",
        text: t("chatPanel.errors.regenerate", { msg }),
        detail,
      });
    }
  }

  async function confirmRename() {
    if (!pendingRename) return;
    const next = pendingRename.draft.trim();
    if (!next) {
      setPendingRename(null);
      return;
    }
    const id = pendingRename.id;
    setPendingRename(null);
    try {
      await RenameChatSession({ sessionId: id, title: next });
      onSidebarShouldReload?.();
    } catch (e: unknown) {
      const { msg, detail } = splitErrorDetail(e);
      console.error("[chat] rename failed", e);
      setNotice({
        kind: "error",
        text: t("chatPanel.errors.rename", { msg }),
        detail,
      });
    }
  }

  function handleDelete(id: number) {
    setPendingDeleteId(id);
  }

  async function confirmDelete() {
    const id = pendingDeleteId;
    if (id == null) return;
    setPendingDeleteId(null);
    await DeleteChatSession({ sessionId: id });
    onSessionDeleted?.();
    onSidebarShouldReload?.();
  }

  function handleEdit(messageId: number) {
    if (!sessionId) return;
    const target = messages.find((m) => m.id === messageId);
    if (!target || target.role !== "user") return;
    setEditingMessage({
      sessionId,
      messageId,
      text: textOfChatMessage(target),
    });
  }

  async function confirmEdit(newText: string) {
    const pending = activeEditing;
    if (pending == null || !sessionId) {
      setEditingMessage(null);
      return;
    }
    const trimmed = newText.trim();
    if (!trimmed) {
      setEditingMessage(null);
      return;
    }
    setEditingMessage(null);

    const snapshot = messages;
    const targetIdx = snapshot.findIndex((m) => m.id === pending.messageId);
    if (targetIdx < 0) return;

    try {
      followTranscriptBottom();
      const resp = await EditChatMessage({
        sessionId,
        messageId: pending.messageId,
        text: trimmed,
        permissionMode: isModeSwitchable ? permissionModeValue : "",
      });
      markSessionRunning(resp.sessionId);
      openStream({
        name: resp.stream,
        sessionId: resp.sessionId,
        assistantMessageId: resp.assistantMessageId,
        streamStartedAt: Date.now(),
      });
      setMessages([
        ...snapshot.slice(0, targetIdx),
        optimisticUser(resp.userMessageId, resp.sessionId, trimmed),
        optimisticAssistantPlaceholder(resp.assistantMessageId, resp.sessionId),
      ]);
      onSidebarShouldReload?.();
    } catch (e: unknown) {
      console.error("[chat] edit failed", e);
      const { msg, detail } = splitErrorDetail(e);
      setNotice({
        kind: "error",
        text: t("chatPanel.errors.edit", { msg }),
        detail,
      });
    }
  }

  return {
    pendingRegenId,
    setPendingRegenId,
    handleRegenerate,
    confirmRegenerate,
    pendingDeleteId,
    setPendingDeleteId,
    handleDelete,
    confirmDelete,
    pendingRename,
    setPendingRename,
    confirmRename,
    activeEditing,
    setEditingMessage,
    handleEdit,
    confirmEdit,
  };
}

export { useMessageActions };
export type { EditingMessage, MessageActions, PendingRename };
