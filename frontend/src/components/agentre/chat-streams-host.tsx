import * as React from "react";
import { useShallow } from "zustand/react/shallow";

import { clientLog } from "@/lib/client-log";
import {
  streamForMessage,
  useChatStreamsStore,
} from "@/stores/chat-streams-store";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useSessionConnStore } from "@/stores/session-conn-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { isResolvedAskState } from "./ask-event-state";
import { openCatchUpWindow, recordCatchUp } from "./chat-panel-catchup-state";
import { StreamSubscriber } from "./stream-subscriber";

import type { chat_svc } from "../../../wailsjs/go/models";
import type { ChatStreamEvent } from "@/hooks/use-chat-stream";
import type { AgentStatus } from "@/stores/types";

function bumpSessionTabToAfterPinned(sessionId: number): void {
  const tabsState = useChatTabsStore.getState();
  const tab = tabsState.tabs.find(
    (t) => t.meta.kind === "session" && t.meta.sessionId === sessionId,
  );
  if (!tab) return;
  tabsState.bumpToAfterPinned(tab.id);
}

// ChatStreamsHost 是「无 DOM 的全局订阅器」。挂在 App 顶层、Routes 同级,
// 跨路由 不会 unmount —— 即使 /chat 被切走,这里的 <StreamSubscriber> 依然
// 持续从 Wails EventsOn 收事件、写到 zustand store。ChatPanel 切回来时
// 直接从 store 读 liveBlocks/liveDelta 还原完整流式视图。
//
// 这里不该有任何业务判断,只做「Wails event → store action」一次翻译。
export function ChatStreamsHost(): React.ReactElement | null {
  // 只订阅「当前有哪些流」这一身份集合(sessionId + assistantMessageId + 事件流名),不订阅流内容。
  // appendLiveText 等每个 chunk 都重建 streams Map,但 host 只用 name 挂 EventsOn
  // 订阅 + 用 (sessionId, assistantMessageId) 路由事件 —— 这些在一条流的整个生命周期里都不变。把身份
  // 编码成 `${sessionId}\n${assistantMessageId}\n${name}` 的字符串数组(原始值,可逐项浅比较),
  // 内容变、身份集合不变时浅相等 → host 不再每 chunk 重渲染整棵订阅树;流增删时
  // 数组变 → 正常重挂订阅。两个 id 为数字、name 为后端事件名,\n 不会出现在三者中。
  //
  // 注意这里是 **两层** 遍历:一个会话可同时有用户轮 / 自主续轮 / 后台 subagent 活动轮
  // 三条流在跑,每条都要各自挂一个 EventsOn。早先按 sessionId 单条订阅,后台流一起来就会
  // 把用户轮的订阅顶掉,那一轮剩下的事件全丢(sess-1950)。
  const streamKeys = useChatStreamsStore(
    useShallow((s) =>
      Array.from(s.streams.values()).flatMap((perMessage) =>
        Array.from(
          perMessage.values(),
          (st) => `${st.sessionId}\n${st.assistantMessageId}\n${st.name}`,
        ),
      ),
    ),
  );
  // 有流在跑的会话集合 —— 连接态订阅按会话(而不是按流)挂一条。放在这个跨路由
  // 长存的宿主上而不是 ChatPanel:连接态每次变化只发一帧,挂在会随路由/标签页
  // 销毁的组件上,重新挂载的转录流就只剩打字指示器,而真实情况是网断了。
  const liveSessionIds = useChatStreamsStore(
    useShallow((s) => Array.from(s.streams.keys())),
  );
  const appendLiveText = useChatStreamsStore((s) => s.appendLiveText);
  const appendLiveThinking = useChatStreamsStore((s) => s.appendLiveThinking);
  const appendLiveToolUse = useChatStreamsStore((s) => s.appendLiveToolUse);
  const appendLiveToolResult = useChatStreamsStore(
    (s) => s.appendLiveToolResult,
  );
  const appendLivePlanUpdate = useChatStreamsStore(
    (s) => s.appendLivePlanUpdate,
  );
  const mergeSubagentMeta = useChatStreamsStore((s) => s.mergeSubagentMeta);
  const setLiveRetry = useChatStreamsStore((s) => s.setLiveRetry);
  const clearLiveRetry = useChatStreamsStore((s) => s.clearLiveRetry);
  const finishStream = useChatStreamsStore((s) => s.finishStream);
  const consumeSteer = useChatStreamsStore((s) => s.consumeSteer);
  const appendLiveAskUserQuestion = useChatStreamsStore(
    (s) => s.appendLiveAskUserQuestion,
  );
  const markAskUserQuestionAnswered = useChatStreamsStore(
    (s) => s.markAskUserQuestionAnswered,
  );
  const appendLiveToolPermissionRequest = useChatStreamsStore(
    (s) => s.appendLiveToolPermissionRequest,
  );
  const markToolPermissionResolved = useChatStreamsStore(
    (s) => s.markToolPermissionResolved,
  );
  const appendLiveToolApproval = useChatStreamsStore(
    (s) => s.appendLiveToolApproval,
  );
  const markToolApprovalResolved = useChatStreamsStore(
    (s) => s.markToolApprovalResolved,
  );
  const appendLiveExecApproval = useChatStreamsStore(
    (s) => s.appendLiveExecApproval,
  );
  const markExecApprovalResolved = useChatStreamsStore(
    (s) => s.markExecApprovalResolved,
  );
  const noteOutputActivity = useChatStreamsStore((s) => s.noteOutputActivity);
  const patchLiveUsage = useChatStreamsStore((s) => s.patchLiveUsage);
  const patchLiveContextWindow = useChatStreamsStore(
    (s) => s.patchLiveContextWindow,
  );
  const appendLiveCompactBoundary = useChatStreamsStore(
    (s) => s.appendLiveCompactBoundary,
  );
  const setLiveCompacting = useChatStreamsStore((s) => s.setLiveCompacting);

  const handleEvent = React.useCallback(
    (sessionId: number, assistantMessageId: number, ev: ChatStreamEvent) => {
      // retry 是「正在等下次尝试」的瞬时态;下面这些进展事件到达就意味着上一次重试已经成功
      // 拿到了新内容,RetryNoticeCard 该撤掉。store 内部 null→null 是 referential no-op,
      // 这里只要在事件入口顺手调一次就够了。done/error/closed/aborted/steer_consumed
      // 走各自的 finish/consume 路径已经隐式清空 liveRetry,无需重复调用。
      switch (ev.kind) {
        case "chunk":
          if (ev.delta) {
            clearLiveRetry(sessionId, assistantMessageId);
            appendLiveText(sessionId, assistantMessageId, ev.delta);
          }
          return;
        case "thinking":
          if (ev.delta) {
            clearLiveRetry(sessionId, assistantMessageId);
            appendLiveThinking(sessionId, assistantMessageId, ev.delta);
          }
          return;
        case "output_activity":
          // 纯计时信号：没有内容、不清 retry chip（模型开口的证据是正文/工具，
          // 不是「开始产出一个块」）。只记首 token。
          noteOutputActivity(sessionId, assistantMessageId);
          return;
        case "tool_use":
          // toolUseId / toolName 任一存在才算有效 —— 与旧版 applyLiveToolUse 行为一致。
          if (!ev.toolUseId && !ev.toolName) return;
          clearLiveRetry(sessionId, assistantMessageId);
          appendLiveToolUse(sessionId, assistantMessageId, {
            toolUseId: ev.toolUseId,
            toolName: ev.toolName,
            toolInput: ev.toolInput,
            canonical: ev.canonical,
            parentToolUseId: ev.parentToolUseId,
            subagentRunId: ev.subagentRunId,
            subagent: ev.subagent,
          });
          return;
        case "tool_result":
          if (!ev.toolUseId && typeof ev.toolResult === "undefined") return;
          clearLiveRetry(sessionId, assistantMessageId);
          // toolResultMeta 必传 —— claudecode TaskCreate 的真实 task id 走这里
          // 透出(meta.task.id),backend 的 task_aggregator 消费后合成 canonical.
          // PlanUpdate 推回前端。漏掉 → backend 拿不到 id → task-progress 列表里
          // 该任务永远停在 pending 且 TaskUpdate 找不到 id 落空。
          appendLiveToolResult(sessionId, assistantMessageId, {
            toolUseId: ev.toolUseId,
            text: ev.toolResult ?? "",
            isError: !!ev.isError,
            parentToolUseId: ev.parentToolUseId,
            subagentRunId: ev.subagentRunId,
            toolResultMeta: ev.toolResultMeta,
          });
          return;
        case "subagent_started":
        case "subagent_progress":
        case "subagent_done":
          if (ev.toolUseId && ev.subagent) {
            clearLiveRetry(sessionId, assistantMessageId);
            mergeSubagentMeta(
              sessionId,
              assistantMessageId,
              ev.toolUseId,
              ev.subagent,
            );
          }
          return;
        case "subagent_model":
          // 只带 toolUseId + model 两个字段(不复用整份 Subagent 快照)——避免浅合并
          // 把已累计的 toolUses/totalTokens/status 覆盖成空值(R4)。
          if (ev.toolUseId && ev.model) {
            clearLiveRetry(sessionId, assistantMessageId);
            mergeSubagentMeta(sessionId, assistantMessageId, ev.toolUseId, {
              model: ev.model,
            } as chat_svc.ChatBlockSubagent);
          }
          return;
        case "retry":
          setLiveRetry(sessionId, assistantMessageId, {
            attempt: ev.retryAttempt ?? 0,
            maxAttempts: ev.retryMaxAttempts ?? 0,
            message: ev.retryMessage ?? "",
            details: ev.retryDetails ?? "",
            at: ev.retryAt ?? Date.now(),
          });
          return;
        case "ask_user_question":
          if (!ev.askUserQuestion) return;
          clearLiveRetry(sessionId, assistantMessageId);
          if (isResolvedAskState(ev.askUserQuestion)) {
            markAskUserQuestionAnswered(
              sessionId,
              assistantMessageId,
              ev.askUserQuestion,
              ev.canonical,
            );
          } else {
            bumpSessionTabToAfterPinned(sessionId);
            appendLiveAskUserQuestion(
              sessionId,
              assistantMessageId,
              ev.askUserQuestion,
              ev.canonical,
            );
          }
          return;
        case "plan_update":
          clearLiveRetry(sessionId, assistantMessageId);
          appendLivePlanUpdate(
            sessionId,
            assistantMessageId,
            ev.delta ?? "",
            ev.canonical,
          );
          return;
        case "tool_permission_request":
          if (!ev.toolPermission) return;
          clearLiveRetry(sessionId, assistantMessageId);
          // ev.canonical 必须随事件落到 store —— 后端 dispatcher_emitter 已经为
          // tool.permission / plan.approve_request 计算好 canonical, 漏传会让
          // CanonicalToolRouter fallback 到 RawToolCard (空标题 + 简化 overlay)。
          if (ev.toolPermission.resolved) {
            markToolPermissionResolved(
              sessionId,
              assistantMessageId,
              ev.toolPermission,
              ev.canonical,
            );
          } else {
            bumpSessionTabToAfterPinned(sessionId);
            appendLiveToolPermissionRequest(
              sessionId,
              assistantMessageId,
              ev.toolPermission,
              ev.canonical,
            );
          }
          return;
        case "tool_approval": {
          // 内置写工具审批。pending=新卡,其余=同 requestId 决议更新。
          if (!ev.requestId) return;
          clearLiveRetry(sessionId, assistantMessageId);
          const payload = {
            toolKey: ev.toolKey ?? "",
            requestId: ev.requestId,
            toolName: ev.toolName ?? "",
            toolInput: ev.toolInput,
            status: ev.status ?? "",
            result: ev.result,
          };
          if (ev.status === "pending") {
            bumpSessionTabToAfterPinned(sessionId);
            appendLiveToolApproval(sessionId, assistantMessageId, payload);
          } else {
            markToolApprovalResolved(sessionId, assistantMessageId, payload);
          }
          return;
        }
        case "exec_approval": {
          if (!ev.execApproval?.id) return;
          clearLiveRetry(sessionId, assistantMessageId);
          if (ev.execApproval.status === "pending") {
            bumpSessionTabToAfterPinned(sessionId);
            appendLiveExecApproval(
              sessionId,
              assistantMessageId,
              ev.execApproval,
            );
          } else {
            // Resolution/expiry closes only the approval lifecycle. The turn
            // remains subscribed until a distinct done/error/aborted event.
            markExecApprovalResolved(
              sessionId,
              assistantMessageId,
              ev.execApproval,
            );
          }
          return;
        }
        case "session_status": {
          if (!ev.sessionStatus) return;
          // session_status patch 一般只带 agentStatus + needsAttention,
          // permissionMode / contextWindow 可能单独到达。contextWindow 写 live stream,
          // 其它事件保留 store 里之前的 permissionMode/status,避免被未携带字段清空。
          if ((ev.sessionStatus.contextWindow ?? 0) > 0) {
            patchLiveContextWindow(
              sessionId,
              assistantMessageId,
              ev.sessionStatus.contextWindow ?? 0,
            );
          }
          const nextStatus = ev.sessionStatus.agentStatus;
          const hasStatus = !!nextStatus;
          const hasMode = !!ev.sessionStatus.permissionMode;
          if (!hasStatus && !hasMode) return;
          const prev = useSessionStatusStore.getState().statuses.get(sessionId);
          // 诊断: 收到 agentStatus="error" 但本 sid 仍有活跃 LiveStream entry,
          // 说明后端在 events channel 关闭前就推了 error 帧 (理论上不该发生 ——
          // 末端 emit 走在 StreamError 之前但 StreamClosed 之后流就应当结束)。
          // 命中即埋根因证据, 一并打 prev/next/streamActive 让排查不用回放事件。
          if (hasStatus && nextStatus === "error") {
            const live = streamForMessage(
              useChatStreamsStore.getState(),
              sessionId,
              assistantMessageId,
            );
            if (live) {
              clientLog.warn(
                "chat-streams-host",
                "session_status agentStatus=error received while LiveStream is still active",
                {
                  sessionId,
                  prevAgentStatus: prev?.agentStatus,
                  nextAgentStatus: nextStatus,
                  needsAttention: ev.sessionStatus.needsAttention,
                  streamAgeMs: Date.now() - live.streamStartedAt,
                },
              );
            }
          }
          if (
            hasStatus &&
            (ev.sessionStatus.needsAttention ||
              nextStatus === "running" ||
              nextStatus === "waiting" ||
              nextStatus === "error")
          ) {
            bumpSessionTabToAfterPinned(sessionId);
          }
          useSessionStatusStore.getState().upsert(sessionId, {
            // Wails boundary: backend sends agentStatus as string; cast to AgentStatus.
            agentStatus: (hasStatus
              ? nextStatus
              : (prev?.agentStatus ?? "idle")) as AgentStatus,
            needsAttention: hasStatus
              ? ev.sessionStatus.needsAttention
              : (prev?.needsAttention ?? false),
            permissionMode:
              ev.sessionStatus.permissionMode || prev?.permissionMode,
            bgRunning: hasStatus
              ? (ev.sessionStatus.bgRunning ?? false)
              : (prev?.bgRunning ?? false),
          });
          return;
        }
        case "usage":
          // turn 内每次模型 API call 边界后端推一条 per-call usage 快照；store
          // 原子写 liveUsage + 可选 contextWindow，让 Composer 实时刷新。不动 liveRetry：
          // usage 帧本身不算「正在重试」的成功信号（chunk/tool_use 才算）。
          if (!ev.usage) return;
          patchLiveUsage(sessionId, assistantMessageId, ev.usage);
          return;
        case "compact_boundary":
          // claudecode CLI 通报上下文已压缩 (manual /compact 或 auto 阈值触发)。
          // 落一个 type=compact_boundary 的 live block,transcript 据此渲染分隔卡
          // + 折叠之前的旧消息。trigger / preTokens 不一定有,UI 退化即可。
          // store 内部同步把 liveCompacting 清回 false,不必再单独发 status:""。
          if (!ev.compact) return;
          clearLiveRetry(sessionId, assistantMessageId);
          appendLiveCompactBoundary(sessionId, assistantMessageId, {
            preTokens: ev.compact.preTokens,
            trigger: ev.compact.trigger,
            at: ev.compact.at,
          });
          return;
        case "runtime_status":
          // claudecode CLI 的会话级运行状态过渡 (manual /compact 或 auto 阈值开始
          // 时一帧 compacting:true,压缩结束由 compact_boundary 自动清旗)。
          if (!ev.runtimeStatus) return;
          setLiveCompacting(
            sessionId,
            assistantMessageId,
            !!ev.runtimeStatus.compacting,
          );
          return;
        case "steer_consumed":
          consumeSteer(sessionId, assistantMessageId, ev);
          return;
        case "done":
        case "error":
        case "closed":
        case "aborted":
          if (ev.kind !== "closed") {
            bumpSessionTabToAfterPinned(sessionId);
          }
          finishStream(sessionId, assistantMessageId, ev);
          return;
      }
    },
    [
      appendLiveText,
      appendLiveThinking,
      noteOutputActivity,
      appendLiveToolUse,
      appendLiveToolResult,
      appendLivePlanUpdate,
      mergeSubagentMeta,
      setLiveRetry,
      clearLiveRetry,
      finishStream,
      consumeSteer,
      appendLiveAskUserQuestion,
      markAskUserQuestionAnswered,
      appendLiveToolPermissionRequest,
      markToolPermissionResolved,
      appendLiveToolApproval,
      markToolApprovalResolved,
      appendLiveExecApproval,
      markExecApprovalResolved,
      patchLiveUsage,
      patchLiveContextWindow,
      appendLiveCompactBoundary,
      setLiveCompacting,
    ],
  );

  return (
    <>
      {streamKeys.map((key) => {
        const [rawSessionId, rawMessageId, ...rest] = key.split("\n");
        const sessionId = Number(rawSessionId);
        const assistantMessageId = Number(rawMessageId);
        const streamName = rest.join("\n");
        return (
          <StreamSubscriber
            key={`${sessionId}:${assistantMessageId}`}
            streamName={streamName}
            onEvent={(ev) => handleEvent(sessionId, assistantMessageId, ev)}
          />
        );
      })}
      {liveSessionIds.map((sessionId) => (
        <SessionConnSubscriber key={sessionId} sessionId={sessionId} />
      ))}
    </>
  );
}

// SessionConnSubscriber 订阅一个会话的连接态流 "chat:conn:<sessionId>"
// (后端 chat_svc.ConnStateStreamName),把 connection_state 事件翻成 store 写入。
// 卸载(= 该会话最后一条流结束)时清掉记录:留着旧的 reconnecting 会泄漏到下一轮。
//
// 补齐摘要记在这里而不是 ChatPanel 上:补齐可能发生在用户切走路由、甚至这个 tab
// 还没打开的时候,记在会被销毁的组件上等于没记。它也不随本组件卸载而清 —— 那份
// 摘要要活到用户真的回到转录区底部为止。断连与恢复这两发都经这里,补齐窗口的开与
// 合因此也在同一处,不用第二个组件去猜「刚才那次断连是从哪一行开始的」。
function SessionConnSubscriber({
  sessionId,
}: {
  sessionId: number;
}): React.ReactElement | null {
  const setConnState = useSessionConnStore((s) => s.setConnState);
  const clear = useSessionConnStore((s) => s.clear);
  React.useEffect(() => () => clear(sessionId), [clear, sessionId]);
  return (
    <StreamSubscriber
      streamName={`chat:conn:${sessionId}`}
      onEvent={(ev) => {
        if (ev.kind !== "connection_state" || !ev.connectionState) return;
        // 补齐窗口的两端都在这里:跌出 connected 那一发开窗(快照此刻的转录行数),
        // 回到 connected 那一发落定(做差)。caughtUpCount 只当闸门 —— 它是重放的
        // 通知条数(一条长回复上千条),控件上的数字是行数差。
        if (ev.connectionState === "connected") {
          recordCatchUp(
            sessionId,
            ev.caughtUpCount ?? 0,
            ev.pendingDecisions ?? 0,
          );
        } else {
          openCatchUpWindow(sessionId);
        }
        setConnState(sessionId, ev.connectionState);
      }}
    />
  );
}
