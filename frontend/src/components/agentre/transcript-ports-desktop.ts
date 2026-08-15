import type { TranscriptPorts } from "@agentre-ai/agentre-ui";

import { useChatTabsStore } from "@/stores/chat-tabs-store";

import {
  AnswerToolApproval,
  AnswerToolPermission,
  AnswerUserQuestion,
  OpenPath,
  ResolveExecApproval,
  ResolvePlanAction,
  WorkspaceFsReadFile,
} from "../../../wailsjs/go/app/App";
import { chat_svc } from "../../../wailsjs/go/models";
import { BrowserOpenURL } from "../../../wailsjs/runtime/runtime";

/**
 * 桌面端的对话流端口实现 —— 共享包与 Wails 绑定之间唯一的接缝。
 *
 * 这里是**唯一**允许把转录里的动作接到 Wails 上的地方；卡片自身只认
 * `useTranscriptPorts()`。agentre-server 提供另一份实现（同样的接口，
 * 动作走 relay RPC），因此同一套卡片在两端都能用。
 *
 * 模块级常量而不是每次渲染现造：TranscriptRenderContext 的稳定性是
 * 行级 memo 不被击穿的前提，端口对象跟着一起保持同一性。
 */
export const desktopTranscriptPorts: TranscriptPorts = {
  async answerToolPermission(input) {
    await AnswerToolPermission(input as chat_svc.AnswerToolPermissionRequest);
  },

  async answerUserQuestion(input) {
    await AnswerUserQuestion(input as chat_svc.AnswerUserQuestionRequest);
  },

  async answerToolApproval(input) {
    await AnswerToolApproval(
      chat_svc.AnswerToolApprovalRequest.createFrom(input),
    );
  },

  async resolveExecApproval(input) {
    const response = await ResolveExecApproval(
      input as chat_svc.ResolveExecApprovalRequest,
    );

    return { status: response.status, decision: response.decision };
  },

  async resolvePlanAction(input) {
    const response = await ResolvePlanAction(
      input as chat_svc.ResolvePlanActionRequest,
    );

    return {
      sessionId: response.sessionId,
      userMessageId: response.userMessageId,
      assistantMessageId: response.assistantMessageId,
      stream: response.stream,
    };
  },

  async openPath(path) {
    await OpenPath(path);
  },

  openExternalURL(url) {
    BrowserOpenURL(url);
  },

  async readWorkspaceFile(sessionId, path) {
    const view = await WorkspaceFsReadFile(sessionId, path);

    return {
      content: view.content,
      contentType: view.contentType,
      binary: view.binary,
      tooLarge: view.tooLarge,
    };
  },

  attachTerminal(input) {
    useChatTabsStore.getState().attachTerminal(input);
  },
};
