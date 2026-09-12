import { useMemo } from "react";
import { useNavigate } from "react-router-dom";
import type { MentionRef, TranscriptPorts } from "@agentre-hub/agentre-ui";

import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useFilePreviewTabsStore } from "@/stores/file-preview-tabs-store";
import { useFileSettingsStore } from "@/stores/file-settings-store";

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

    // 服务层把「文件不存在 / 对端离线」改成了随成功应答回来的结构化原因（预览面
    // 板要据此分出终态与可重试态）。这个端口的消费者只认「成不成」，所以在边界上
    // 把它还原成一次失败 —— 否则内联图片会拿着空 body 当成功渲染出来。
    if (view.unavailable) {
      throw new Error(view.unavailable);
    }

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

  // 设置读取留在这里(ports.ts 已经写明这是宿主的产品决策,包内不知道也不该知道
  // files.open_action):"preview" 时开/复用既有右侧栏预览标签并回 true,调用方
  // (RichLink 的 dispatchClick)据此不再退回 openPath;"external" 时什么都不做,
  // 回 false,调用方退回今天的外部打开路线(byte-identical,含 line:col 后缀)。
  // 读 .getState() 而不是订阅:这是一次性的点击响应,不需要 ports 对象本身随
  // 设置变化重建。
  //
  // 入口模式是 "directory":转录里点一条路径要看的是「这个文件**现在**长什么样」
  // (spec「入口与可用性」),面板据此走 readFile 读工作区正文。不能是 "session"
  // ——那是侧栏「本次会话」档的工具 diff,一次取数都不打。
  previewFile(sessionId, path, anchor?) {
    const { openAction } = useFileSettingsStore.getState().settings;
    if (openAction === "external") return false;
    useFilePreviewTabsStore
      .getState()
      .openPreview(sessionId, path, "directory", anchor);
    return true;
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
        openMention: (ref: MentionRef) => {
          if (ref.kind === "agent") return navigate("/org");
          if (ref.kind === "project") return navigate("/projects");
          // 设备的去处是设置里的设备面板 —— 那是这台机器在桌面端唯一的落脚页。
          navigate("/settings", { state: { settingsPage: "remote-devices" } });
        },
      }) satisfies Required<TranscriptPorts>,
    [navigate],
  );
}
