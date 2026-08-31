import * as React from "react";
import { useTranslation } from "react-i18next";

import { splitErrorDetail } from "@/lib/error-detail";
import type { useChatSession } from "@/hooks/use-chat-session";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useChatStreamsStore } from "@/stores/chat-streams-store";
import { useQueuedMessagesStore } from "@/stores/queued-messages-store";

import {
  isChatSteerNoActiveError,
  isChatStopNoActiveError,
  isExactCompactCommand,
  isExactNewCommand,
  parseGoalCommand,
  type GoalCommand,
} from "./commands";
import type { SetChatPanelNotice } from "./notice";
import {
  markSessionRunning,
  optimisticAssistantPlaceholder,
  optimisticUser,
} from "./optimistic";

import type { ChatComposerHandle, ChatComposerSubmit } from "../chat";

import {
  CancelQueuedChatMessage,
  ClearChatGoal,
  CompactChatSession,
  EnqueueChatMessage,
  GetChatGoal,
  GetChatLaunchCommand,
  PeerRunFresh,
  SendChatMessage,
  SetChatGoal,
  StartChatGoal,
  StopChatMessage,
} from "../../../../wailsjs/go/app/App";
import { chat_svc } from "../../../../wailsjs/go/models";

import { copyTextWithToast } from "@agentre-hub/agentre-ui";

type ChatSession = ReturnType<typeof useChatSession>;
type ChatAgentItem = chat_svc.ChatAgentItem;

// 改选后实际生效档的目标种类与设备身份（R18）：空会话态首发时据此决定走本地 Send
// 还是 peer RunFresh（桌面派发）。由 NewSessionExecTargetLine 报上来。
type EffectiveExecTarget = {
  kind: "local" | "desktop" | "daemon";
  deviceId: string;
  deviceName: string;
  backendType: string;
  llmProviderKey: string;
  llmModelKey: string;
};

type UseChatActionsOptions = {
  sessionId: number;
  session: ChatSession["session"];
  newSessionAgent?: ChatAgentItem | null;
  newSessionContext?: { projectId?: number };
  setMessages: ChatSession["setMessages"];
  reloadSession: ChatSession["reload"];
  openStream: ReturnType<typeof useChatStreamsStore.getState>["openStream"];
  followTranscriptBottom: () => void;
  composerRef: React.RefObject<ChatComposerHandle | null>;
  setSendInFlight: React.Dispatch<React.SetStateAction<boolean>>;
  setNotice: SetChatPanelNotice;
  onSessionCreated?: (sessionId: number, agentId: number) => void;
  onPeerSessionCreated?: (peer: {
    fingerprint: string;
    // 派到远端桌面端的那条对话按 conversation_id 寻址，不是本机 chat_sessions.id。
    conversationId: string;
    title: string;
    deviceName: string;
  }) => void;
  onSidebarShouldReload?: () => void;
  streaming: boolean;
  activeBackendType: string;
  isModeSwitchable: boolean;
  permissionModeValue: string;
  supportsImageInput: boolean;
  supportsCompactRPC: boolean;
  execTargetOverride: number | null;
  effectiveTarget: EffectiveExecTarget | null;
  providerKey: string;
  modelKey: string;
  /** activeEditing !== null。编辑态下回车走 confirmEdit，不起新一轮。 */
  editing: boolean;
  confirmEdit: (newText: string) => Promise<void>;
};

type ChatActions = {
  doSend: (
    targetSessionId: number,
    agentId: number,
    message: ChatComposerSubmit,
    permissionModeOverride?: string,
  ) => Promise<void>;
  doStop: (sid: number) => Promise<void>;
  doCancelQueued: (sid: number, queuedId: string) => Promise<void>;
  handleCopyLaunchCommand: (sid: number) => Promise<void>;
  handleComposerSubmit: (message: ChatComposerSubmit | string) => void;
};

// useChatActions 收拢 composer / 头部触发的那一族会话 RPC:首发与续发、压缩、目标
// (goal) 命令、排队与撤销、软中断、复制启动命令,以及把回车分派到它们身上的那段
// 路由。它们共用「fire-and-forget + 失败落成 notice」这一套形状,彼此又互相调用
// (goal set 完接着 doSend、enqueue 撞上轮结束回退成 doSend),拆开只会把闭包摊薄。
function useChatActions({
  sessionId,
  session,
  newSessionAgent,
  newSessionContext,
  setMessages,
  reloadSession,
  openStream,
  followTranscriptBottom,
  composerRef,
  setSendInFlight,
  setNotice,
  onSessionCreated,
  onPeerSessionCreated,
  onSidebarShouldReload,
  streaming,
  activeBackendType,
  isModeSwitchable,
  permissionModeValue,
  supportsImageInput,
  supportsCompactRPC,
  execTargetOverride,
  effectiveTarget,
  providerKey,
  modelKey,
  editing,
  confirmEdit,
}: UseChatActionsOptions): ChatActions {
  const { t } = useTranslation();

  async function doSend(
    targetSessionId: number,
    agentId: number,
    message: ChatComposerSubmit,
    permissionModeOverride?: string,
  ) {
    const text = message.text.trim();
    const images = message.images ?? [];
    // 发送消息时强制跟随到底部，无论用户当前在哪里
    followTranscriptBottom();
    setSendInFlight(true);
    // 调用点都是 void doSend(...) fire-and-forget；这里必须自吞错误成 notice，
    // 否则 RPC 失败时 UI 完全无声（用户在 composer 干瞪眼）。doEnqueue 的 fallback
    // 也走这里，set notice 后不 rethrow，正好顶替 doEnqueue 原本的 setNotice。
    try {
      // R18 桌面派发：空会话态且生效目标是另一台桌面端（kind=desktop）时，新建会话
      // 走 peer RunFresh（对端真实会话 id 回传），不走本地 EnsureChatSession/Send。
      // agentred 与本机行为不变（R22）。
      if (
        targetSessionId === 0 &&
        effectiveTarget?.kind === "desktop" &&
        effectiveTarget.deviceId
      ) {
        const ack = await PeerRunFresh({
          fingerprint: effectiveTarget.deviceId,
          agentId,
          projectId: newSessionContext?.projectId ?? 0,
          title: text,
          text,
          permissionMode:
            permissionModeOverride ??
            (isModeSwitchable ? permissionModeValue : ""),
          ...(providerKey ? { providerKey, modelKey } : {}),
        } as Parameters<typeof PeerRunFresh>[0]);
        onPeerSessionCreated?.({
          fingerprint: effectiveTarget.deviceId,
          conversationId: ack?.conversationId ?? "",
          title: text,
          deviceName: effectiveTarget.deviceName,
        });
        onSidebarShouldReload?.();
        return;
      }
      // 新建会话路径：把项目上下文带上（仅 targetSessionId=0 时生效）；
      // 已存在会话续发：projectId 在 Send 端被忽略，传 0 也无害。
      const sendPayload: Record<string, unknown> = {
        sessionId: targetSessionId,
        agentId,
        text,
        projectId:
          targetSessionId === 0 ? (newSessionContext?.projectId ?? 0) : 0,
        permissionMode:
          permissionModeOverride ??
          (isModeSwitchable ? permissionModeValue : ""),
        // 新建会话首发前预选：瞬态 ModelTarget 随 SendRequest.ProviderKey/ModelKey 透传,
        // 与 Session 一同落库（spec 2026-08-11「新建与已有会话流程」）。已有会话后端忽略
        // 该字段（改 target 走 SetChatSessionModelTarget）。
        ...(targetSessionId === 0 && providerKey
          ? { providerKey, modelKey }
          : {}),
        // R15a 手动指定执行目标：同一条规则，仅新建会话生效；0/未选时不传，
        // 后端按 R15 顺序自动挑第一个可用的档。
        ...(targetSessionId === 0 && execTargetOverride
          ? { execTargetOverride }
          : {}),
      };
      if (images.length > 0) {
        sendPayload.images = images.map((image) => ({
          name: image.name,
          dataUrl: image.dataUrl,
        }));
      }
      const resp = await SendChatMessage(
        chat_svc.SendRequest.createFrom(sendPayload),
      );
      // 新建会话路径：通知父级把 selectedSessionId 切到新 id。
      if (targetSessionId === 0 && resp.sessionId) {
        onSessionCreated?.(resp.sessionId, agentId);
      }
      setMessages((prev) => [
        ...prev,
        optimisticUser(resp.userMessageId, resp.sessionId, text, images),
        optimisticAssistantPlaceholder(resp.assistantMessageId, resp.sessionId),
      ]);
      // 乐观写 running: 后端 Send 已把 sess.AgentStatus="running" 落库, 但 turn
      // 起手没 emit session_status 事件, tab / 详情 toolbar 单纯读 store 会停在
      // 上一轮的 idle。这里同步翻成 running, 让所有订阅者一帧内看到运行态。
      markSessionRunning(resp.sessionId);
      openStream({
        name: resp.stream,
        sessionId: resp.sessionId,
        assistantMessageId: resp.assistantMessageId,
        streamStartedAt: Date.now(),
      });
      // 创建新会话时后端在 RPC 内已写入 AgentStatus="running" 并落库，
      // 立刻 reload 让左侧 sidebar 同步出现新会话 + running 状态，不用等 turn 结束。
      onSidebarShouldReload?.();
    } catch (e: unknown) {
      const { msg, detail } = splitErrorDetail(e);
      console.error("[chat] send failed", e);
      // 发送失败:ChatComposer 已清空输入框,这里把文本 + 图片原样放回(草稿保留),
      // 并给 notice 挂 Retry / Discard 动作。Retry 用同一份 message 重新 doSend;
      // Discard 清掉恢复的草稿。msg(headline)拼进 notice 文本让用户知道为什么失败,
      // cause(detail)按既有规则只在存在时渲染成详情块。
      composerRef.current?.restoreDraft(text, images);
      const retryMessage: ChatComposerSubmit =
        images.length > 0 ? { text, images } : { text };
      setNotice({
        kind: "error",
        text: `${t("chatPanel.sendRetry.restored")} · ${msg}`,
        detail,
        actions: {
          retry: () => {
            setNotice(null);
            void doSend(
              targetSessionId,
              agentId,
              retryMessage,
              permissionModeOverride,
            );
          },
          discard: () => {
            composerRef.current?.clearDraft();
            setNotice(null);
          },
        },
      });
    } finally {
      setSendInFlight(false);
    }
  }

  async function doCompact(sid: number) {
    if (!sid) return;
    try {
      followTranscriptBottom();
      const resp = await CompactChatSession({ sessionId: sid });
      setMessages((prev) => {
        if (prev.some((m) => m.id === resp.assistantMessageId)) return prev;
        return [
          ...prev,
          optimisticAssistantPlaceholder(
            resp.assistantMessageId,
            resp.sessionId,
          ),
        ];
      });
      markSessionRunning(resp.sessionId);
      openStream({
        name: resp.stream,
        sessionId: resp.sessionId,
        assistantMessageId: resp.assistantMessageId,
        streamStartedAt: Date.now(),
      });
      onSidebarShouldReload?.();
    } catch (e: unknown) {
      const { msg, detail } = splitErrorDetail(e);
      console.error("[chat] compact failed", e);
      setNotice({
        kind: "error",
        text: t("chatPanel.errors.compact", { msg }),
        detail,
      });
    }
  }

  async function doGoal(sid: number, agentId: number, cmd: GoalCommand) {
    if (!sid) return;
    try {
      if (cmd.kind === "get") {
        const resp = await GetChatGoal({ sessionId: sid });
        const goal = resp.goal;
        setNotice({
          kind: "info",
          text: goal
            ? t("chatPanel.goal.current", {
                objective: goal.objective,
                status: goal.status,
                tokens: goal.tokensUsed ?? 0,
              })
            : t("chatPanel.goal.empty"),
        });
        return;
      }
      if (cmd.kind === "clear") {
        await ClearChatGoal({ sessionId: sid });
        setNotice({ kind: "info", text: t("chatPanel.goal.cleared") });
        return;
      }
      const payload =
        cmd.kind === "set"
          ? { sessionId: sid, objective: cmd.objective, status: "active" }
          : { sessionId: sid, status: cmd.status };
      const resp = await SetChatGoal(payload);
      setNotice({
        kind: "info",
        text: resp.goal
          ? t("chatPanel.goal.updatedWithObjective", {
              objective: resp.goal.objective,
            })
          : t("chatPanel.goal.updated"),
      });
      if (cmd.kind === "set") {
        await doSend(sid, agentId, { text: cmd.objective });
      }
    } catch (e: unknown) {
      const { msg, detail } = splitErrorDetail(e);
      console.error("[chat] goal failed", e);
      setNotice({
        kind: "error",
        text: t("chatPanel.errors.goal", { msg }),
        detail,
      });
    }
  }

  async function doStartGoal(
    agentId: number,
    cmd: Extract<GoalCommand, { kind: "set" }>,
  ) {
    try {
      const resp = await StartChatGoal({
        agentId,
        projectId: newSessionContext?.projectId ?? 0,
        objective: cmd.objective,
        status: "active",
        permissionMode: isModeSwitchable ? permissionModeValue : "",
      });
      if (resp.sessionId) {
        onSessionCreated?.(resp.sessionId, agentId);
      }
      onSidebarShouldReload?.();
      setNotice({
        kind: "info",
        text: resp.goal
          ? t("chatPanel.goal.updatedWithObjective", {
              objective: resp.goal.objective,
            })
          : t("chatPanel.goal.updated"),
      });
      if (resp.sessionId) {
        await doSend(resp.sessionId, agentId, { text: cmd.objective });
      }
    } catch (e: unknown) {
      const { msg, detail } = splitErrorDetail(e);
      console.error("[chat] start goal failed", e);
      setNotice({
        kind: "error",
        text: t("chatPanel.errors.goal", { msg }),
        detail,
      });
    }
  }

  function notifyCompactNeedsSession() {
    setNotice({
      kind: "info",
      text: t("chatPanel.compact.needsSession"),
    });
  }

  function notifyCompactWaitForTurn() {
    setNotice({ kind: "info", text: t("chatPanel.compact.waitForTurn") });
  }

  // doEnqueue：streaming 中按回车走这里。把新消息推到当前 turn 的排队队列，
  // 等 AI 跑到下一个安全点（claudecode PreToolUse hook / codex turn/steer RPC 即刻 /
  // builtin cago safe-point）才注入。
  async function doEnqueue(sid: number, agentId: number, text: string) {
    try {
      const resp = await EnqueueChatMessage({ sessionId: sid, text });
      useQueuedMessagesStore.getState().append(sid, {
        id: resp.queuedId,
        text,
        cancellable: resp.cancellable,
      });
    } catch (e: unknown) {
      const { msg, detail } = splitErrorDetail(e);
      if (isChatSteerNoActiveError(msg)) {
        // turn 已结束（done/closed 事件即将到 / 已到），按普通 send 重新起一轮。
        await doSend(sid, agentId, { text });
        return;
      }
      console.error("[chat] enqueue failed", e);
      setNotice({
        kind: "error",
        text: t("chatPanel.errors.enqueue", { msg }),
        detail,
      });
    }
  }

  async function doCancelQueued(sid: number, queuedId: string) {
    try {
      const resp = await CancelQueuedChatMessage({ sessionId: sid, queuedId });
      useQueuedMessagesStore.getState().consume(sid, resp.removed);
    } catch (e: unknown) {
      const { msg, detail } = splitErrorDetail(e);
      console.error("[chat] cancel queued failed", e);
      setNotice({
        kind: "error",
        text: t("chatPanel.errors.cancelQueued", { msg }),
        detail,
      });
    }
  }

  // doStop 软中断当前 turn。后端会按 backend 分别走 control_request{interrupt}
  // /turn/interrupt/ctx-cancel，子进程都保留，发个 StreamAborted 事件让 store
  // bump tick → reload 拿 partial 内容。这里不做乐观 UI，等 aborted 事件回来。
  async function doStop(sid: number) {
    try {
      await StopChatMessage({ sessionId: sid });
      // 「重启遗孤」会话(DB 卡在 running/waiting 但本地无活跃 stream):后端已把它
      // reconcile 回 idle,但这类会话没有活跃 stream 不会推 aborted 事件,doneTick
      // effect 也不会触发 reload —— 必须主动 reload 才能让那颗一直亮着的「停止」按钮
      // 回灰。正常活跃 turn 的 abort 仍由 aborted 事件驱动 reload,这里多一次读无害。
      await reloadSession();
    } catch (e: unknown) {
      const { msg, detail } = splitErrorDetail(e);
      // turn 已自然完成与点击 Stop 发生 race —— 后端 activeCancels 已经清掉了，
      // 不算错，静默即可（用户的意图是「让这轮停下」，结果已经停了）。但前端视图可能
      // 还停在 running,reload 一次把 DB 的终态拉回来,避免按钮一直亮着点了没反应。
      if (isChatStopNoActiveError(msg)) {
        console.warn("[chat] stop race-lost (turn already finished)");
        await reloadSession();
        return;
      }
      console.error("[chat] stop failed", e);
      setNotice({
        kind: "error",
        text: t("chatPanel.errors.stop", { msg }),
        detail,
      });
    }
  }

  async function handleCopyLaunchCommand(sid: number) {
    try {
      const resp = await GetChatLaunchCommand({ sessionId: sid });
      await copyTextWithToast(resp.command, {
        errorTitle: t("chatPanel.launchCommand.copyFailed"),
        successTitle: t("chatPanel.launchCommand.copyDone"),
        successDescription: t("chatPanel.launchCommand.copyDescription"),
      });
    } catch (e: unknown) {
      const { msg, detail } = splitErrorDetail(e);
      console.error("[chat] copy launch command failed", e);
      setNotice({
        kind: "error",
        text: t("chatPanel.errors.copyLaunchCommand", { msg }),
        detail,
      });
    }
  }

  // handleComposerSubmit 是回车的唯一出口:斜杠命令 / 编辑态 / 排队 / 首发 / 续发
  // 在这里分道,分完各走上面那几个 do*。
  function handleComposerSubmit(message: ChatComposerSubmit | string) {
    const submit: ChatComposerSubmit =
      typeof message === "string" ? { text: message } : message;
    const text = submit.text.trim();
    const images = submit.images ?? [];
    if (images.length > 0 && !supportsImageInput) {
      setNotice({
        kind: "error",
        text: t("chatPanel.errors.imageUnsupported"),
      });
      return;
    }
    if (editing) {
      void confirmEdit(text);
      return;
    }
    // `/new`:沿用当前会话(或未首发新 tab)的 agent / 项目,新开一个全新
    // 空白会话 tab 并跳转过去;当前会话完全不受影响(不发消息、不压缩)。
    // 真正的 DB 会话由新 tab 首发消息时惰性创建,与「+ / ⌘N 新建会话」一致。
    if (isExactNewCommand(text)) {
      const newAgentId = session?.agentId ?? newSessionAgent?.id ?? 0;
      if (newAgentId > 0) {
        const newProjectId =
          session?.projectId ?? newSessionContext?.projectId ?? 0;
        useChatTabsStore
          .getState()
          .openNewSession(newProjectId, newAgentId, "");
      }
      return;
    }
    const goalCommand =
      activeBackendType === "codex" ? parseGoalCommand(text) : null;
    if (goalCommand) {
      if (images.length > 0) {
        setNotice({
          kind: "error",
          text: t("chatPanel.goal.imageUnsupported"),
        });
        return;
      }
      if (streaming) {
        setNotice({
          kind: "info",
          text: t("chatPanel.goal.waitForTurn"),
        });
        return;
      }
      if (!sessionId) {
        if (newSessionAgent && goalCommand.kind === "set") {
          void doStartGoal(newSessionAgent.id, goalCommand);
          return;
        }
        setNotice({
          kind: "info",
          text: t("chatPanel.goal.needsSession"),
        });
        return;
      }
      void doGoal(sessionId, session?.agentId ?? 0, goalCommand);
      return;
    }
    if (supportsCompactRPC && isExactCompactCommand(text)) {
      if (!sessionId) {
        notifyCompactNeedsSession();
        return;
      }
      if (streaming) {
        notifyCompactWaitForTurn();
        return;
      }
      if (images.length > 0) {
        setNotice({
          kind: "error",
          text: t("chatPanel.compact.imageUnsupported"),
        });
        return;
      }
      void doCompact(sessionId);
      return;
    }
    // 新建会话首发：targetSessionId=0，由 doSend 内的 RPC 返回真实 sessionId
    // 并通过 onSessionCreated 回填到父 store；此时 composer 不会卸载（结构稳定）。
    if (!sessionId && newSessionAgent) {
      void doSend(0, newSessionAgent.id, submit);
      return;
    }
    if (streaming && sessionId > 0) {
      if (images.length > 0) {
        setNotice({
          kind: "error",
          text: t("chatPanel.errors.imageWhileStreaming"),
        });
        return;
      }
      // streaming 中：按回车走 Enqueue，把消息排队等下一个安全点注入。
      void doEnqueue(sessionId, session?.agentId ?? 0, text);
      return;
    }
    void doSend(sessionId, session?.agentId ?? 0, submit);
  }

  return {
    doSend,
    doStop,
    doCancelQueued,
    handleCopyLaunchCommand,
    handleComposerSubmit,
  };
}

export { useChatActions };
export type { EffectiveExecTarget };
