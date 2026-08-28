import { useMemo } from "react";
import { useNavigate } from "react-router-dom";
import type { MentionRef, TranscriptPorts } from "@agentre-hub/agentre-ui";

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
// 用 `satisfies` 而不是类型标注 `: TranscriptPorts`：标注会把每个可选成员
// widen 成 `... | undefined`，展开进下面的完整端口面时就再也证明不了「一个不缺」。
// `satisfies` 同样校验形状，但保住字面量的精确成员类型。
export const desktopTranscriptPorts = {
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
    // 转录里的一条路径来自当时那次工具调用，转录本身没有「当前工作根」的概念：
    // 空串 = 会话 cwd，保持这个接缝本轮之前的解析口径不变。
    const view = await WorkspaceFsReadFile(sessionId, "", path);

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
} satisfies TranscriptPorts;

/**
 * 桌面端完整的端口面 —— 应用根用它，不要直接用上面的常量。
 *
 * 为什么单独一层：其余端口都是 Wails 调用，可以做成模块级常量；而
 * `openMention`（点 @提及去哪）是**路由**问题，`navigate` 只能从 hook 拿，
 * 模块级常量表达不了。依赖只有 navigate（react-router 保证其稳定），
 * 所以 TranscriptRenderContext 的稳定性不受影响、行级 memo 不会被击穿。
 *
 * `satisfies Required<TranscriptPorts>` 是这层的重点：桌面端是全能力宿主，
 * 包里的可选端口对它应当**一个不缺**（可选是给 agentre-server 那种缺能力的
 * 宿主留的）。漏接一个的表现是「按钮悄悄消失、点了没反应」，肉眼极难发现 ——
 * 这条标注让它变成编译期错误。新增可选端口而桌面端没接，`tsc` 当场红。
 */
export function useDesktopTranscriptPorts(): TranscriptPorts {
  const navigate = useNavigate();

  return useMemo(
    () =>
      ({
        ...desktopTranscriptPorts,
        openMention: (ref: MentionRef) =>
          navigate(ref.kind === "agent" ? "/org" : "/projects"),
      }) satisfies Required<TranscriptPorts>,
    [navigate],
  );
}
