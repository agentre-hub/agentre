import * as React from "react";

import { useChatStream, type ChatStreamEvent } from "@/hooks/use-chat-stream";
import { splitErrorDetail } from "@/lib/error-detail";
import {
  streamForMessage,
  useChatStreamsStore,
} from "@/stores/chat-streams-store";

import {
  flipSubagentStatusInMessages,
  mergeSubagentMetaInMessages,
} from "../background-tasks/flip-subagent-status";

import { markSessionRunning, markSessionStatus } from "./optimistic";
import type { SetChatPanelNotice } from "./notice";

import type { chat_svc } from "../../../../wailsjs/go/models";

type SvcChatMessage = chat_svc.ChatMessage;

type UseAutonomousTurnEventsOptions = {
  sessionId: number;
  openStream: ReturnType<typeof useChatStreamsStore.getState>["openStream"];
  setMessages: React.Dispatch<React.SetStateAction<SvcChatMessage[]>>;
  setNotice: SetChatPanelNotice;
};

// ── 自主续轮(后台任务完成,CLI 自主跑的一轮)的会话级旁路订阅 ──
// per-turn 流名只有用户 Send 才会从后端响应里拿到;自主轮没有这个入口,所以后端
// 经会话级事件 "chat:autonomous:<sessionId>"(后端 AutonomousEventPrefix)推一帧
// StreamAutonomousStarted。收到后:乐观翻 running + 插入新 assistant 行 + openStream
// 订阅该自主轮的 per-turn 流,让它像普通 turn 一样实时渲染。后续 chunk/done 与
// StreamDone→reloadSession 都复用既有路径,自主轮无需任何特殊渲染分支。
// completedTask: 若事件携带后台任务身份,立即把对应 live tool_use block 的
// subagent.status 翻成终态,刷新后台任务面板胶囊。
// subagent_activity_started: 后台 subagent 开始产生内部活动(本地 claudecode 专有)。
// 重开发起消息已有的 per-turn 流把嵌套块渲染回 AgentSpawnCard;不新建消息行,
// session 保持 idle 态。remote claudecode 目前不发此事件。
function useAutonomousTurnEvents({
  sessionId,
  openStream,
  setMessages,
  setNotice,
}: UseAutonomousTurnEventsOptions): void {
  const onAutonomousEvent = React.useCallback(
    (ev: ChatStreamEvent) => {
      // subagent_activity_started: 后台 subagent 开始产生内部活动。
      // 只重开发起消息的流（openStream 绑到已存在的 launchMessageId）；
      // 不插入新消息行，不把 session 翻成 running（后台工作保持 idle 态）。
      if (ev.kind === "subagent_activity_started") {
        if (!ev.stream || !ev.launchMessageId) return;
        // 发起消息上已经有流(后台 subagent 与用户轮共用同一条 assistant 消息时
        // 就是这样)就什么都不做:两边的流名都是后端的 StreamName(sid, launchMsgID),
        // 本来就是同一条,重开只会把在跑那一轮已经流到屏幕、还没落库的 liveBlocks
        // 连同计时 / 用量一起清零 —— 这一轮当场少掉一大段(sess-3396)。
        if (
          streamForMessage(
            useChatStreamsStore.getState(),
            sessionId,
            ev.launchMessageId,
          )
        ) {
          return;
        }
        openStream({
          name: ev.stream,
          sessionId,
          assistantMessageId: ev.launchMessageId,
          streamStartedAt: Date.now(),
        });
        return;
      }
      // subagent_started/progress/done:后端在 per-turn 流之外镜像到会话级流的那一份。
      // 空闲态后台 subagent 的派遣卡早已落进 messages，ChatStreamsHost 那条只翻
      // liveBlocks 的路径必然落空 —— 卡片上的工具数 / token 会一直停在派遣那一刻
      // (sess-2275)。这里就地合并进 messages，与 completedTask 翻状态同一套做法。
      if (
        ev.kind === "subagent_started" ||
        ev.kind === "subagent_progress" ||
        ev.kind === "subagent_done"
      ) {
        if (!ev.toolUseId || !ev.subagent) return;
        const { toolUseId, subagent } = ev;
        setMessages((prev) =>
          mergeSubagentMetaInMessages(prev, toolUseId, subagent),
        );
        return;
      }
      // subagent_model:同上,会话级流镜像的模型事件(R2)只带 toolUseId + model 两个
      // 字段(不复用整份 Subagent 快照)——避免浅合并把已累计的 toolUses/totalTokens/
      // status 覆盖成空值(R4)。
      if (ev.kind === "subagent_model") {
        if (!ev.toolUseId || !ev.model) return;
        const { toolUseId, model } = ev;
        setMessages((prev) =>
          mergeSubagentMetaInMessages(prev, toolUseId, {
            model,
          } as chat_svc.ChatBlockSubagent),
        );
        return;
      }
      // autonomous_finished:自主轮 / 后台 subagent 活动轮收尾时会话级流补发的终态兜底。
      // per-turn 流的 openStream(ChatPanel)与 EventsOn 订阅(ChatStreamsHost)跨 render 解耦,
      // 短轮的 per-turn done/closed 可能赶在订阅注册前发完被漏掉 → LiveStream 永远留在 store
      // → streaming 卡死(发不出消息 / 空 assistant 行不回填)。会话级流常驻订阅、无此 race,
      // 据 launchMessageId 兜底 finishStream。幂等:per-turn done 已被收到时该流已不在,
      // streamForMessage 命中空直接跳过,不重复 bumpDone。
      if (ev.kind === "autonomous_finished") {
        const mid = ev.launchMessageId;
        if (!mid) return;
        const streamsState = useChatStreamsStore.getState();
        if (streamForMessage(streamsState, sessionId, mid)) {
          streamsState.finishStream(sessionId, mid, { kind: "done" });
        }
        return;
      }
      // error:自主续轮的 assistant 消息落库最终失败,后端已把会话翻 error、丢弃这一轮
      // 并中断 CLI 那一轮(见 docs/specs/2026-08-07-autonomous-turn-resilience.md
      // 「自主续轮落库失败时的可观察结果」)。失败的正是**建 assistant 行**那次写,所以
      // 这一轮既没有消息行也没有 per-turn 流 —— ChatStreamsHost 按 assistantMessageId
      // 收口的 error 路径接不住,只能就地渲染成 composer 上方的 notice。文案是后端
      // mapTurnError 给的动态文本(与用户发起的轮次同一套),不进 i18n。
      if (ev.kind === "error") {
        if (!ev.error) return;
        const { msg, detail } = splitErrorDetail(ev.error);
        setNotice({ kind: "error", text: msg, detail });
        markSessionStatus(sessionId, "error");
        return;
      }
      if (ev.kind !== "autonomous_started") {
        return;
      }
      // 先翻转后台任务状态 (completedTask 可能在没有 assistantMessage 时也存在)。
      // 后台任务完成是跨轮的:发起它的主轮早已结束,那条 tool_use block 已从 liveBlocks
      // 落进 messages。mergeSubagentMeta 只翻 liveBlocks(覆盖极少数仍在流的竞态),真正
      // 命中的是 messages —— 必须一并翻,否则面板胶囊 + 行内 pill 永远 spin (bug #2)。
      if (ev.completedTask?.toolUseId) {
        const { toolUseId, status, summary } = ev.completedTask;
        // 发起该后台任务的那条 tool_use 可能还挂在本会话任意一条在流的消息上
        // (哪条不确定,后台任务是跨轮的),逐条流试着翻一遍;真正命中的多半是
        // 下面的 messages。store 对未知 toolUseId 是 no-op。
        const streamsState = useChatStreamsStore.getState();
        for (const mid of streamsState.streams.get(sessionId)?.keys() ?? []) {
          streamsState.mergeSubagentMeta(sessionId, mid, toolUseId, {
            status,
            summary,
          } as chat_svc.ChatBlockSubagent);
        }
        setMessages((prev) =>
          flipSubagentStatusInMessages(prev, toolUseId, status, summary),
        );
      }
      if (!ev.assistantMessage || !ev.stream) {
        return;
      }
      const amsg = ev.assistantMessage;
      markSessionRunning(sessionId);
      openStream({
        name: ev.stream,
        sessionId,
        assistantMessageId: amsg.id,
        streamStartedAt: Date.now(),
      });
      setMessages((prev) => {
        if (prev.some((m) => m.id === amsg.id)) return prev;
        // R18:浏览器在空闲会话上「开新一轮」跑起的一轮,daemon 把发起方用户消息随
        // StreamAutonomousStarted 的 userMessages 带出 —— 先插 user 行再插 assistant,
        // 否则桌面端看到的又是「没有提问的回复」。来源标识在消息 DTO 上,转录渲染层
        // 复用 chat.message.fromDevice 的 inline pill(本机消息无 sourceDevice,零变化)。
        const userMsgs = ev.userMessages ?? [];
        const additions = [...userMsgs, amsg];
        return [...prev, ...additions];
      });
    },
    [sessionId, openStream, setMessages, setNotice],
  );
  useChatStream(
    sessionId ? `chat:autonomous:${sessionId}` : null,
    onAutonomousEvent,
  );
}

export { useAutonomousTurnEvents };
