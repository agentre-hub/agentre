import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import { LoadChatSession } from "../../wailsjs/go/app/App";
import type { chat_svc } from "../../wailsjs/go/models";
import { clientLog } from "@/lib/client-log";
import { isNoticeOnlyMessage } from "@/lib/notice-message";
import { samePayload } from "@/lib/same-payload";
import {
  hasSessionStream,
  primaryStream,
  streamForMessage,
  useChatStreamsStore,
} from "@/stores/chat-streams-store";
import { useSessionConnStore } from "@/stores/session-conn-store";
import { useSessionMetaStore } from "@/stores/session-meta-store";
import {
  normalizeSessionSnapshot,
  useSessionStatusStore,
} from "@/stores/session-status-store";
import { useSessionWithOverlays } from "./use-session-with-overlays";
import type { AgentStatus, SessionConnectionState } from "@/stores/types";

export type ChatSessionDetail = chat_svc.ChatSessionDetail & {
  deviceID?: string;
  deviceName?: string;
  online?: boolean;
  cwd?: string;
};
export type ChatMessage = chat_svc.ChatMessage;

// reconcileLoadedMessages 把 LoadSession 快照与「快照被取走之后才插进来的行」合并。
//
// LoadSession 是异步 DB 快照。自主续轮 / 后台活动轮不经前端发起,可能正好在快照
// 已取走、响应还没回来的窗口里起手 —— chat-panel 的 onAutonomousEvent 收到
// autonomous_started 会就地插入这一轮的 assistant 行并 openStream。此时整表覆盖
// 就把那一行抹掉了,而 liveBlocks 是按 assistantMessageId 挂的:宿主行没了,
// transcript-rows 的 buildRows 只遍历 displayMessages,这一轮后续到达的流式正文、
// AskUserQuestion 审批卡就全部静默丢弃,且不会自愈(sess-2916:会话胶囊翻「审批」
// 而转录里一张卡都没有 —— agentStatus 有 normalizeSessionSnapshot 挡旧快照,
// messages 没有对应护栏;doneTick 守卫只认 turn 结束,起手这一侧不设防)。
//
// 只补回「发起 load 时还不存在、快照里也没有」的行。发起时就已在表里、快照却不含
// 的行是后端真删了(编辑 / 重跑会截断后续消息),必须跟着快照消失 —— 否则被截断的
// 历史会被复活。
function reconcileLoadedMessages(
  prev: ChatMessage[],
  loaded: ChatMessage[],
  idsBeforeLoad: ReadonlySet<number>,
): ChatMessage[] {
  const loadedIds = new Set(loaded.map((m) => m.id));
  const insertedDuringLoad = prev.filter(
    (m) => !loadedIds.has(m.id) && !idsBeforeLoad.has(m.id),
  );
  if (insertedDuringLoad.length === 0) return loaded;
  return [...loaded, ...insertedDuringLoad];
}

// preserveMessageIdentity 让「内容没变的消息」保住它上一轮的对象引用。
//
// 每轮 turn 收尾都会 reload 一次全量历史,而转录的行缓存是
// WeakMap<消息对象, 行[]>(agentre-ui 的 transcript-rows),键就是消息对象本身。
// 整表换成 Wails 新反序列化出来的对象 → 全表 cache miss、行级 memo 全被击穿,
// 用户看到的就是「每轮结束整段转录重刷一遍」。绝大多数轮次里,除了刚落定的那条
// assistant 之外没有任何一行变过,把它们的引用还回去,缓存就能整片存活。
//
// 整表都没变时连数组引用一起保留 —— 下游按 messages 数组身份做记忆化。
// 比较口径与侧栏三个数据源同源(samePayload),序列化不了就退化成「不相等」,
// 后果只是多重渲一次。
function preserveMessageIdentity(
  prev: ChatMessage[],
  next: ChatMessage[],
): ChatMessage[] {
  if (prev.length === 0) return next;
  const prevByID = new Map(prev.map((m) => [m.id, m]));
  let changed = prev.length !== next.length;
  const merged = next.map((m, i) => {
    const old = prevByID.get(m.id);
    if (!old || !samePayload(old, m)) {
      changed = true;
      return m;
    }
    // 内容一致但位置挪了(编辑/重跑截断后重排),数组本身仍要换新。
    if (prev[i] !== old) changed = true;
    return old;
  });
  return changed ? merged : prev;
}

export function useChatSession(sessionId: number) {
  const [session, setSession] = useState<ChatSessionDetail | null>(null);
  const [messages, setMessagesState] = useState<ChatMessage[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // messagesRef 让 reload 这个异步流程能在 await 之前同步读到当前消息表 ——
  // 上面 reconcileLoadedMessages 要的「发起 load 那一刻的 id 集合」只能这样取,
  // 走 state 依赖会让 reload 随每次消息变化重建并触发重复加载。
  const messagesRef = useRef<ChatMessage[]>([]);
  const setMessages = useCallback<Dispatch<SetStateAction<ChatMessage[]>>>(
    (action) => {
      setMessagesState((prev) => {
        const next =
          typeof action === "function"
            ? (action as (p: ChatMessage[]) => ChatMessage[])(prev)
            : action;
        messagesRef.current = next;
        return next;
      });
    },
    [],
  );

  // useSessionWithOverlays 合并 meta + status + read-overlay，作为详情页
  // 运行时态的唯一来源。sessionWithLiveStatus 从此通过 overlay 读取，而不是
  // 直接订阅 session-status-store。
  const overlay = useSessionWithOverlays(sessionId);

  const reload = useCallback(async () => {
    if (!sessionId) {
      setSession(null);
      setMessages([]);
      setLoading(false);
      return;
    }
    setLoading(true);
    setError(null);
    try {
      // 快照的「已知世界」——在第一次 await 之前取,响应回来时据它区分
      // 「load 在途期间新插入的行」与「后端真删了的行」。
      const idsBeforeLoad = new Set(messagesRef.current.map((m) => m.id));
      const startedDoneTick =
        useSessionStatusStore.getState().statuses.get(sessionId)?.doneTick ?? 0;
      let resp = await LoadChatSession({ sessionId });
      const returnedDoneTick =
        useSessionStatusStore.getState().statuses.get(sessionId)?.doneTick ?? 0;
      if (returnedDoneTick > startedDoneTick) {
        clientLog.warn(
          "use-chat-session",
          "reloading session because a turn finished during LoadChatSession",
          {
            sessionId,
            startedDoneTick,
            returnedDoneTick,
          },
        );
        resp = await LoadChatSession({ sessionId });
      }
      setSession(resp.session);
      // loadedMessages 可能在下方 activeStream 分支被替换(剥离 overlay pending
      // tool_approval 块),setMessages 统一挪到该分支之后执行。
      let loadedMessages = resp.messages ?? [];
      // Cache session 的静态字段 (agentColor / agentName / projectId / title /
      // lastMessageAt / lastReadAt) 到 session-meta-store, 让 TabStrip 在不主动
      // LoadSession 的前提下能拿到这些 detail 字段渲染 avatar 色 / 项目色下划线 /
      // tooltip 项目链 + attention 判断。
      //
      // setMeta 是 replace 语义,所以 lastReadAt 必须显式带上 ——
      // 否则会把 chat-agents-store.bulkUpsert 之前写入的服务端值擦掉,attention
      // 判断在客户端 override 缺席时会误判成未读。
      useSessionMetaStore.getState().setMeta(sessionId, {
        agentId: resp.session.agentId,
        agentName: resp.session.agentName,
        agentColor: resp.session.agentColor,
        projectId: resp.session.projectId ?? 0,
        title: resp.session.title,
        lastMessageAt: resp.session.lastMessageAt ?? 0,
        lastReadAt: resp.session.lastReadAt ?? 0,
        permissionModeAtLaunch: resp.session.permissionModeAtLaunch ?? "",
      });
      // 把详情快照里的 agentStatus / needsAttention / permissionMode 灌进
      // session-status-store, 让其它读路径(tab / sidebar / use-tabs-view)拿到
      // 最新值, 不依赖独立 reload。
      //
      // 诊断: LoadChatSession 是异步 DB 快照。若本 sid 仍有活跃 LiveStream 而
      // 详情说 agentStatus="error"/"idle", 大概率是 reload 在 turn 起手前发起、
      // 响应到达时 Send 已经把 DB 翻 "running"。normalizeSessionSnapshot 会忽略
      // 这次旧状态覆盖；这里保留诊断证据。
      const live = primaryStream(useChatStreamsStore.getState(), sessionId);
      if (
        live &&
        resp.session.agentStatus !== "running" &&
        resp.session.agentStatus !== "waiting"
      ) {
        const prev = useSessionStatusStore.getState().statuses.get(sessionId);
        clientLog.warn(
          "use-chat-session",
          "ignored stale LoadChatSession agentStatus while LiveStream is active",
          {
            sessionId,
            prevAgentStatus: prev?.agentStatus,
            loadedAgentStatus: resp.session.agentStatus,
            streamAgeMs: Date.now() - live.streamStartedAt,
          },
        );
      }
      const snapshot = normalizeSessionSnapshot(
        sessionId,
        {
          // Wails boundary: backend sends agentStatus as string; cast to AgentStatus.
          agentStatus: resp.session.agentStatus as AgentStatus,
          needsAttention: resp.session.needsAttention,
          permissionMode: resp.session.permissionMode,
          bgRunning: resp.session.bgRunning ?? false,
        },
        !!live,
      );
      useSessionStatusStore.getState().upsert(sessionId, snapshot);
      // 重挂活跃 turn 的实时流。自主轮 / subagent 子轮等"非前端发起"的 turn 没有 Send
      // 响应入口给出 per-turn 流名,中途打开会话就看不到"生成中"和流式内容 ——
      // LoadSession 在有活跃 turn 时回传 activeStream,这里据此 openStream 续看。
      // 已有活跃 LiveStream 时不覆盖(避免打断正常 Send 已开的流);流名指向在跑的
      // (末条)assistant 消息,StreamDone 经既有路径收口并 reload 回填最终内容。
      if (resp.session.activeStream) {
        const streamsStore = useChatStreamsStore.getState();
        // 找末条**真实** assistant:只承载 notice 的旁白行(供应商切换 notice)跳过 ——
        // 它排在在跑的 assistant 之后,若被当成末条 assistant,overlay 的 pending 审批
        // 就落在这条没有块的旁白行上找不到,既没从 messages 剥离也没搬进 liveBlocks,
        // 用户点批准后 resolved 事件反扫 liveBlocks 落空 → 卡片永远 pending。
        let lastAssistantIdx = -1;
        for (let i = loadedMessages.length - 1; i >= 0; i--) {
          if (isNoticeOnlyMessage(loadedMessages[i])) continue;
          if (loadedMessages[i].role === "assistant") {
            lastAssistantIdx = i;
            break;
          }
        }
        if (lastAssistantIdx >= 0) {
          const lastAssistant = loadedMessages[lastAssistantIdx];
          // overlay pending tool_approval 块搬进 liveBlocks(单一真相源):
          // 后端把内存里悬而未决的审批 overlay 进末条 assistant 消息投影。若留在
          // persisted messages 路径,之后的 resolved 流事件只反扫 liveBlocks →
          // no-op → 卡片永远 pending。这里从消息副本剥离 + 注入 live store,
          // resolved 自然命中;同时避免与流事件已写入的同 requestId live 块双卡
          // (transcript 两路 push 同 identity 行不会自动去重)。注入按 requestId
          // 去重,已有活跃流且 liveBlocks 已含该卡时只剥不注。
          const isPendingToolApproval = (b: chat_svc.ChatBlock) =>
            b.type === "tool_approval" && b.toolApproval?.status === "pending";
          const pendingApprovals = (lastAssistant.blocks ?? []).filter(
            isPendingToolApproval,
          );
          const isPendingExecApproval = (b: chat_svc.ChatBlock) =>
            b.type === "exec_approval" && b.execApproval?.status === "pending";
          const pendingExecApprovals = (lastAssistant.blocks ?? []).filter(
            isPendingExecApproval,
          );
          if (pendingApprovals.length > 0 || pendingExecApprovals.length > 0) {
            loadedMessages = loadedMessages.slice();
            loadedMessages[lastAssistantIdx] = {
              ...lastAssistant,
              blocks: (lastAssistant.blocks ?? []).filter(
                (b) => !isPendingToolApproval(b) && !isPendingExecApproval(b),
              ),
            } as ChatMessage;
          }
          // 已有活跃 LiveStream 时不覆盖(避免打断正常 Send 已开的流)。
          if (
            !hasSessionStream(streamsStore, sessionId) &&
            lastAssistant.id > 0
          ) {
            streamsStore.openStream({
              name: resp.session.activeStream,
              sessionId,
              assistantMessageId: lastAssistant.id,
              streamStartedAt: Date.now(),
            });
          }
          for (const block of pendingApprovals) {
            const approval = block.toolApproval;
            if (!approval?.requestId) continue;
            const liveNow = streamForMessage(
              useChatStreamsStore.getState(),
              sessionId,
              lastAssistant.id,
            );
            const exists = liveNow?.liveBlocks.some(
              (b) =>
                b.type === "tool_approval" &&
                b.toolApproval?.requestId === approval.requestId,
            );
            if (!exists) {
              useChatStreamsStore
                .getState()
                .appendLiveToolApproval(sessionId, lastAssistant.id, approval);
            }
          }
          for (const block of pendingExecApprovals) {
            const approval = block.execApproval;
            if (!approval?.id) continue;
            const liveNow = streamForMessage(
              useChatStreamsStore.getState(),
              sessionId,
              lastAssistant.id,
            );
            const exists = liveNow?.liveBlocks.some(
              (b) =>
                b.type === "exec_approval" &&
                b.execApproval?.id === approval.id,
            );
            if (!exists) {
              useChatStreamsStore
                .getState()
                .appendLiveExecApproval(sessionId, lastAssistant.id, approval);
            }
          }
        }
      }
      // 连接态播种。整页重载会清空 session-conn-store,而这条会话可能正卡在退避
      // 窗口中间(断连不再终结会话,上面的 activeStream 分支照旧把流重挂起来):
      // 不播种,用户在整个窗口里看到的都是普通打字指示器,分不清 agent 在想还是网断了。
      // 只在会话确有活跃流时落笔 —— 断连形态是活信号的一种形态,没有活信号就没有
      // 可修饰的对象;更要紧的是清条目的责任在 chat:conn:<sid> 的订阅者手上,
      // 而它只跟着活跃流挂载,给没有流的会话写条目就是写一条永远清不掉的记录。
      if (
        resp.session.connectionState &&
        hasSessionStream(useChatStreamsStore.getState(), sessionId)
      ) {
        useSessionConnStore
          .getState()
          .seed(
            sessionId,
            resp.session.connectionState as SessionConnectionState,
          );
      }
      setMessages((prev) =>
        preserveMessageIdentity(
          prev,
          reconcileLoadedMessages(prev, loadedMessages, idsBeforeLoad),
        ),
      );
      // 注:不在这里 MarkRead。语义上"用户已读到 lastMessageAt"只能由
      // ChatPanel 根据 active prop 判断 —— 隐藏 tab 也会 mount useChatSession,
      // 在这里无条件 MarkRead 会把用户没看过的 session 标记成已读。
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
    // setMessages 是 [] 依赖的 useCallback,身份恒定 —— 列进来只为满足
    // exhaustive-deps,reload 仍然只随 sessionId 变化,不会多触发一次加载。
  }, [sessionId, setMessages]);

  useEffect(() => {
    void reload();
  }, [reload]);

  // sessionWithLiveStatus 把 LoadSession 拿到的 detail 与 useSessionWithOverlays
  // 当前态合并:运行时翻转(turn 起手乐观 running / waiting 翻转 / 详情 reload 回填)
  // 都从 overlay 读, 详情对象本身的 agentStatus / needsAttention / permissionMode
  // 被 overlay 覆盖。这样所有写路径只对 store 一次写, 详情页 toolbar 跟侧栏 / tab
  // 拿到同一份事实。
  const sessionWithLiveStatus = useMemo(() => {
    if (!session) return null;
    if (!overlay) return session;
    return {
      ...session,
      agentStatus: overlay.agentStatus,
      needsAttention: overlay.needsAttention,
      permissionMode: overlay.permissionMode ?? session.permissionMode,
    };
  }, [session, overlay]);

  return {
    session: sessionWithLiveStatus,
    messages,
    loading,
    error,
    reload,
    setMessages,
  };
}
