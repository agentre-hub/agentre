import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import {
  LoadChatMessageBlocks,
  LoadChatSession,
  LoadChatSessionBlocksByType,
} from "../../wailsjs/go/app/App";
import type { chat_svc } from "../../wailsjs/go/models";
import { clientLog } from "@/lib/client-log";
import { isNoticeOnlyMessage } from "@agentre-hub/agentre-ui";
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

// DERIVED_VIEW_BLOCK_TYPES 是派生视图算得出结论所需要的块类型。
//
// 读路径只随 LoadChatSession 下发最近一个窗口的完整正文(spec 2026-08-27 决策 6),
// 而后台任务面板、大纲、「变更」与文件预览的 diff 重放都要看**整条会话**:
//   - tool_use 带着后台任务的 subagent 元数据、编辑类工具名与 canonical 载荷;
//   - text 是大纲每一轮的标题。
// 少取哪一类,对应的视图就会静默算错(漏任务 / 大纲残缺 / 变更少几次调用),而不是
// 显式地少一段历史 —— 这正是决策 6 拒绝「只返回最近 N 轮」的理由。
// 上下文占用不在这里:它只读消息元数据上的 token 计数列,元数据本来就全量下发。
export const DERIVED_VIEW_BLOCK_TYPES = ["text", "tool_use"];

// hasUnloadedBlocks:表里还有消息只拿到了元数据(正文还没取)。
function hasUnloadedBlocks(messages: readonly ChatMessage[]): boolean {
  return messages.some((m) => m.blocksLoaded === false);
}

// refreshRetainedDerivedBlocks:给「保住的旧正文」那些行换上新鲜的派生视图块。
//
// retainLoadedBlocks 把用户往上取回来的正文塞回去时会把 blocksLoaded 翻成 true,
// 而 mergeDerivedViewBlocks 只处理 blocksLoaded===false —— 这些行因此被按类型点查
// 整个跳过,里面还在跑的后台 subagent 卡片就冻在「翻上去那一刻」的状态,不再更新。
//
// 这里按 toolUseId 逐块覆盖而不是整段替换:保住的那份是**完整但旧**的正文(转录要
// 靠它渲染工具结果),点查回来的是**新鲜但只有点名类型**的子集,两者只在同一个
// toolUseId 上重合。会随时间变的正是这些块(subagent 的状态与进度),text 块落库后
// 不再改,覆盖与否都一样。
function refreshRetainedDerivedBlocks(
  messages: ChatMessage[],
  byType: readonly ChatMessage[],
  retainedIds: ReadonlySet<number>,
): ChatMessage[] {
  if (retainedIds.size === 0) return messages;
  const freshById = new Map(byType.map((m) => [m.id, m.blocks ?? []]));
  return messages.map((m) => {
    if (!retainedIds.has(m.id)) return m;
    const fresh = freshById.get(m.id);
    if (!fresh || fresh.length === 0) return m;
    const freshByToolUseId = new Map(
      fresh
        .filter((b) => b.toolUseId)
        .map((b) => [b.toolUseId as string, b] as const),
    );
    if (freshByToolUseId.size === 0) return m;
    let changed = false;
    const blocks = (m.blocks ?? []).map((b) => {
      if (!b.toolUseId) return b;
      const next = freshByToolUseId.get(b.toolUseId);
      if (!next || next === b) return b;
      changed = true;
      return next;
    });
    return changed ? ({ ...m, blocks } as ChatMessage) : m;
  });
}

// mergeDerivedViewBlocks 把「按类型点查」的结果合进只有元数据的那些消息。
//
// 只动 blocksLoaded===false 的行:窗口内那些消息手上是**完整**正文,拿点名类型的
// 子集去覆盖,转录立刻就少掉工具结果。合进去的仍是子集,所以 blocksLoaded 保持假。
//
// scope 是这次读真正交回来的那些消息(增量读;null = 全量读,每一行都交回了)。
// 范围外的行是手上原样保住的,它们早先补过的派生块仍然有效 —— 增量点查本来就不
// 交回它们,按「没交回 = 没有块」去覆盖就把整条会话的派生视图抹空了。
function mergeDerivedViewBlocks(
  messages: ChatMessage[],
  byType: ChatMessage[],
  scope: ReadonlySet<number> | null,
): ChatMessage[] {
  const blocksById = new Map(byType.map((m) => [m.id, m.blocks ?? []]));
  return messages.map((m) =>
    m.blocksLoaded === false && (scope === null || scope.has(m.id))
      ? ({ ...m, blocks: blocksById.get(m.id) ?? [] } as ChatMessage)
      : m,
  );
}

// mergeChangedMessages 把增量读交回的消息合进手上已持有的那份,拼出「全量读会给的
// 那张表」:按 messageIds(本会话当前全部消息,seq 升序)逐个取 —— 交回了就用交回
// 的(新增 / 刚收尾 / 块后来被改写过),没交回就用手上那条(自游标以来没变过)。
// 不在 messageIds 里的手上消息就此消失(编辑重发 / 截断删掉的)。
//
// 返回 null 表示拼不出来:某个 id 既不在手上也没交回 —— 有消息不经版本推进就进了
// 会话(恢复流程),只能以游标 0 全量重读。
function mergeChangedMessages(
  held: readonly ChatMessage[],
  changed: readonly ChatMessage[],
  messageIds: readonly number[] | undefined,
): ChatMessage[] | null {
  if (!messageIds) return null;
  const changedById = new Map(changed.map((m) => [m.id, m]));
  const heldById = new Map(held.map((m) => [m.id, m]));
  const merged: ChatMessage[] = [];
  for (const id of messageIds) {
    const m = changedById.get(id) ?? heldById.get(id);
    if (!m) return null;
    merged.push(m);
  }
  return merged;
}

// cursorAfterLoad 算下一次增量读的游标。
//
// 游标的含义是「版本不超过它的改动手上都有」。响应的 version 满足这一点,唯独被
// retainLiveMessageBlocks 冻住的那些行例外:手上留的是开流那一刻的旧正文,比它们
// 交回的版本旧。游标退到它们的版本之下,流收尾后的下一次增量读才会把它们的最终
// 正文再取一次 —— 否则那段旧正文就永远留在转录里。缺版本的一律退回全量(0)。
function cursorAfterLoad(
  respVersion: number | undefined,
  messages: readonly ChatMessage[],
  frozenIds: ReadonlySet<number>,
): number {
  let cursor = respVersion ?? 0;
  for (const m of messages) {
    if (!frozenIds.has(m.id)) continue;
    cursor = m.version ? Math.min(cursor, m.version - 1) : 0;
  }
  return Math.max(cursor, 0);
}

// retainLoadedBlocks 保住「用户已经往上取回来」的那些正文。
//
// 每轮收尾都会 reload,而 LoadChatSession 永远只带最近一个窗口的完整正文 —— 不保,
// 用户往上翻了半天的历史会在下一轮结束时整段缩回窗口,看着就像消息被删了。
// 只对**本次快照说没取到、而手上已经取到**的消息生效;后端真删掉的行不在快照里,
// 因此不会被复活(reconcileLoadedMessages 的边界照旧成立)。
//
// 返回值同时带出**保住了哪些行**:这些行的 blocksLoaded 被翻成了 true,按类型点查
// 认不出它们还需要补数,得由 refreshRetainedDerivedBlocks 单独接手。
function retainLoadedBlocks(
  prev: readonly ChatMessage[],
  loaded: ChatMessage[],
): { messages: ChatMessage[]; retainedIds: Set<number> } {
  const retainedIds = new Set<number>();
  if (prev.length === 0) return { messages: loaded, retainedIds };
  const prevById = new Map(prev.map((m) => [m.id, m]));
  const messages = loaded.map((m) => {
    if (m.blocksLoaded !== false) return m;
    const old = prevById.get(m.id);
    if (!old || old.blocksLoaded === false) return m;
    retainedIds.add(m.id);
    return { ...m, blocks: old.blocks, blocksLoaded: true } as ChatMessage;
  });
  return { messages, retainedIds };
}

// retainLiveMessageBlocks:一条消息只要还有活跃 LiveStream,它的持久化正文就冻在
// 「开流那一刻」的样子。
//
// 转录按「持久化正文 ++ liveBlocks」拼一条消息(见 chat-streams-store 的 LiveStream
// 注释),liveBlocks 只装还没落库的尾巴。但后端在轮内就把已发出的块整段
// replaceBlocks 落库了 —— 轮中途的一次 reload 会把 liveBlocks 已经持有的那一段又
// 当成持久化正文发回来,两路各画一遍。重复的 tool_use 还会算出同一个 uiStateKey,
// 虚拟行撞 key 后测量缓存互相覆盖,行位置错乱、中间空出一大片(sess-3396)。
//
// 冻住这一段,重复就无从产生;流一收尾(finishStream 删掉 entry)下一次 load 自然
// 拿回完整正文。窗口外还没取回正文的消息(blocksLoaded === false)不冻 —— 那份
// 「旧快照」是空的,冻了等于把正文抹掉。
//
// 返回值同时带出**冻住了哪些行**:它们手上的正文比交回的版本旧,下一次增量读的
// 游标不能越过它们(见 cursorAfterLoad)。
//
// 手上没有旧副本(tab 首次挂载,流是它没挂载时开的)但流开在消息刚建出、正文还空的那一刻
// (startsEmpty)时,「开流那一刻的样子」就是空正文:冻在空上,liveBlocks 独自画全这一轮。
function retainLiveMessageBlocks(
  prev: readonly ChatMessage[],
  loaded: ChatMessage[],
  liveStreams: ReadonlyMap<number, { startsEmpty?: boolean }>,
): { messages: ChatMessage[]; frozenIds: Set<number> } {
  const frozenIds = new Set<number>();
  if (liveStreams.size === 0) return { messages: loaded, frozenIds };
  const prevById = new Map(prev.map((m) => [m.id, m]));
  const messages = loaded.map((m) => {
    const stream = liveStreams.get(m.id);
    if (!stream) return m;
    const old = prevById.get(m.id);
    if (!old && stream.startsEmpty) {
      frozenIds.add(m.id);
      return {
        ...m,
        blocks: [] as typeof m.blocks,
        blocksLoaded: true,
      } as ChatMessage;
    }
    if (!old || old.blocksLoaded === false) return m;
    frozenIds.add(m.id);
    return { ...m, blocks: old.blocks, blocksLoaded: true } as ChatMessage;
  });
  return { messages, frozenIds };
}

export function useChatSession(sessionId: number) {
  const [session, setSession] = useState<ChatSessionDetail | null>(null);
  const [messages, setMessagesState] = useState<ChatMessage[]>([]);
  const [loading, setLoading] = useState(false);
  const [loadingEarlierBlocks, setLoadingEarlierBlocks] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // messagesRef 让 reload 这个异步流程能在 await 之前同步读到当前消息表 ——
  // 上面 reconcileLoadedMessages 要的「发起 load 那一刻的 id 集合」只能这样取,
  // 走 state 依赖会让 reload 随每次消息变化重建并触发重复加载。
  const messagesRef = useRef<ChatMessage[]>([]);
  // earlierInFlightRef 吸收重复触发:转录顶部的按钮与「滚回顶部」的滚动监听都会调
  // loadEarlierBlocks,滚动事件更是一路连发。用 ref 而不是 loadingEarlierBlocks
  // 这个 state —— 同一帧里连着来的几次调用读到的 state 还是旧值。
  const earlierInFlightRef = useRef(false);
  // cursorRef 是增量 reload 的游标:手上这份消息表对应的消息版本,连同它属于哪个
  // 会话(切换会话后旧游标作废,下一次读自然是全量)。它只在落表的同一个 updater
  // 里改写,与 messagesRef 成对推进 —— 先推游标、后落表的空档里起手的 reload 会拿
  // 新游标去配旧消息表,两次之间的改动就再也取不回来。
  const cursorRef = useRef<{ sessionId: number; version: number } | null>(null);
  // loadSeqRef 给每次 reload 编号。只有最后发起的那一次落表:更早发起的响应带着更旧
  // 的快照,晚到时盖掉新结果、再把游标拨回去,手上数据就与游标对不上了。
  const loadSeqRef = useRef(0);
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
    const seq = ++loadSeqRef.current;
    const superseded = () => seq !== loadSeqRef.current;
    if (!sessionId) {
      cursorRef.current = null;
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
      const held = cursorRef.current;
      let sinceVersion = held?.sessionId === sessionId ? held.version : 0;
      const fetchSession = async (since: number) => {
        const req =
          since > 0 ? { sessionId, sinceVersion: since } : { sessionId };
        const startedDoneTick =
          useSessionStatusStore.getState().statuses.get(sessionId)?.doneTick ??
          0;
        const first = await LoadChatSession(req);
        const returnedDoneTick =
          useSessionStatusStore.getState().statuses.get(sessionId)?.doneTick ??
          0;
        if (returnedDoneTick <= startedDoneTick) return first;
        clientLog.warn(
          "use-chat-session",
          "reloading session because a turn finished during LoadChatSession",
          { sessionId, startedDoneTick, returnedDoneTick },
        );
        return LoadChatSession(req);
      };
      let resp = await fetchSession(sinceVersion);
      if (superseded()) return;
      // 增量读:把交回的消息合进手上那份,拼成全量读会给的那张表。changedIds 是
      // 这次真正交回来的消息,派生视图的补数只需要照顾它们(null = 全量,全部)。
      let loadedMessages = resp.messages ?? [];
      let changedIds: Set<number> | null = null;
      if (sinceVersion > 0) {
        const merged = mergeChangedMessages(
          messagesRef.current,
          loadedMessages,
          resp.messageIds,
        );
        if (merged) {
          changedIds = new Set(loadedMessages.map((m) => m.id));
          loadedMessages = merged;
        } else {
          clientLog.warn(
            "use-chat-session",
            "incremental reload cannot account for every message; reloading in full",
            { sessionId, sinceVersion },
          );
          sinceVersion = 0;
          resp = await fetchSession(0);
          if (superseded()) return;
          loadedMessages = resp.messages ?? [];
        }
      }
      // loadedMessages 可能在下方 activeStream 分支被替换(剥离 overlay pending
      // tool_approval 块),setMessages 统一挪到该分支之后执行。会话详情也跟着
      // 消息一起落(见末尾):详情一落面板就收起骨架,而派生块点查还要再等一轮。
      // 「这次 load 之前就已经在流的消息」—— 必须在下面 activeStream 分支
      // openStream 之前取,那条流刚开、liveBlocks 还是空的,它要的正是这份新快照。
      const liveBeforeLoad = new Map(
        useChatStreamsStore.getState().streams.get(sessionId) ?? [],
      );
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
        // 它排在在跑的 assistant 之后,若被当成末条 assistant,在跑那一条上的 pending 审批
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
          // pending tool_approval 块搬进 liveBlocks(单一真相源):
          // 悬而未决的审批卡挂起时就已落进在跑那条 assistant,投影里带着它。若留在
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
      // 用户此前往上取回来的那些正文不能因为一次 reload 又缩回窗口。
      const retained = retainLoadedBlocks(messagesRef.current, loadedMessages);
      loadedMessages = retained.messages;
      // 在流的消息:正文冻在开流那一刻,别把 liveBlocks 已经持有的那一段再发一遍。
      const frozen = retainLiveMessageBlocks(
        messagesRef.current,
        loadedMessages,
        liveBeforeLoad,
      );
      loadedMessages = frozen.messages;
      // 派生视图的补数:窗口外的消息只拿到了元数据,而后台任务面板 / 大纲 / 变更
      // 要看整条会话。按类型点查把它们需要的那几类块补回去,数据集合与「整条转录
      // 都在本地」时等价。补数失败不影响会话打开 —— 转录本身已经就绪。
      // 增量读时点查带同一个游标,只补这次交回来的那几条;手上原样保住的行早先补过。
      const needsDerivedBlocks =
        retained.retainedIds.size > 0 ||
        loadedMessages.some(
          (m) =>
            m.blocksLoaded === false &&
            (changedIds === null || changedIds.has(m.id)),
        );
      // 点查失败时这次交回的窗口外消息缺着派生块:游标不越过这次的起点,下一次
      // 增量读把它们再交回一遍(全量读失败则下一次照旧全量)。
      let derivedBlocksMissing = false;
      if (needsDerivedBlocks) {
        try {
          const byType = await LoadChatSessionBlocksByType(
            sinceVersion > 0
              ? { sessionId, types: DERIVED_VIEW_BLOCK_TYPES, sinceVersion }
              : { sessionId, types: DERIVED_VIEW_BLOCK_TYPES },
          );
          if (superseded()) return;
          loadedMessages = mergeDerivedViewBlocks(
            loadedMessages,
            byType.messages ?? [],
            changedIds,
          );
          // 保住的那些行 blocksLoaded 已是 true,上一句认不出它们;它们的派生视图
          // 块在这里按 toolUseId 单独换新。
          loadedMessages = refreshRetainedDerivedBlocks(
            loadedMessages,
            byType.messages ?? [],
            retained.retainedIds,
          );
        } catch (e: unknown) {
          derivedBlocksMissing = true;
          clientLog.warn(
            "use-chat-session",
            "derived-view block query failed; panels fall back to the loaded window",
            { sessionId, error: e instanceof Error ? e.message : String(e) },
          );
        }
      }
      const cursor = cursorAfterLoad(
        resp.version,
        loadedMessages,
        frozen.frozenIds,
      );
      const nextCursor = derivedBlocksMissing
        ? Math.min(sinceVersion, cursor)
        : cursor;
      // 详情与消息同一批落下:先落详情会让面板在派生块点查期间既无骨架也无内容
      // (真实大库上这次点查要几秒,转录区整段空白)。
      setSession(resp.session);
      setMessages((prev) => {
        // 游标与消息表在同一个 updater 里推进(见 cursorRef)。
        cursorRef.current =
          nextCursor > 0 ? { sessionId, version: nextCursor } : null;
        return preserveMessageIdentity(
          prev,
          reconcileLoadedMessages(prev, loadedMessages, idsBeforeLoad),
        );
      });
      // 注:不在这里 MarkRead。语义上"用户已读到 lastMessageAt"只能由
      // ChatPanel 根据 active prop 判断 —— 隐藏 tab 也会 mount useChatSession,
      // 在这里无条件 MarkRead 会把用户没看过的 session 标记成已读。
    } catch (e: unknown) {
      if (!superseded()) setError(e instanceof Error ? e.message : String(e));
    } finally {
      if (!superseded()) setLoading(false);
    }
    // setMessages 是 [] 依赖的 useCallback,身份恒定 —— 列进来只为满足
    // exhaustive-deps,reload 仍然只随 sessionId 变化,不会多触发一次加载。
  }, [sessionId, setMessages]);

  // 打开会话(含切换到另一条会话)一律全量读:先作废游标。之后的 reload(轮次收尾、
  // 停止、切换供应商……)有游标就走增量,只取「上次之后变了什么」。
  useEffect(() => {
    cursorRef.current = null;
    void reload();
  }, [reload]);

  // loadEarlierBlocks:向上滚动时把更早一段消息的完整正文取回来。
  //
  // 取的锚点是「手上最早那条正文已就绪的消息」的 seq —— 用它而不是列表首条,是因为
  // 首条多半正是还没取正文的那些之一,拿它当锚点会把已经取到的一段再取一遍。
  const loadEarlierBlocks = useCallback(async () => {
    if (!sessionId || earlierInFlightRef.current) return;
    const current = messagesRef.current;
    if (!hasUnloadedBlocks(current)) return;
    const earliestLoaded = current.find((m) => m.blocksLoaded !== false);
    if (!earliestLoaded) return;
    earlierInFlightRef.current = true;
    setLoadingEarlierBlocks(true);
    try {
      const resp = await LoadChatMessageBlocks({
        sessionId,
        beforeSeq: earliestLoaded.seq,
        // 0 = 用后端的窗口大小(chat_svc.TranscriptBlockWindow),免得前端再抄一份
        // 会漂移的常量。
        limit: 0,
      });
      const loaded = new Map((resp.messages ?? []).map((m) => [m.id, m]));
      if (loaded.size === 0) return;
      setMessages((prev) => {
        // 这份正文是请求发出那一刻的快照:请求在途时一次增量 reload 可能已经落下、把
        // 游标推过了其中某条消息后来的版本。换上这份更旧的正文后,游标得退回它的版本
        // 之下(与 cursorAfterLoad 对冻住的行同一口径),下一次增量读才会再交回那条
        // 消息、把它的派生块换新 —— 否则旧正文就永远留着。同一个 updater 里改游标。
        let floor = Number.POSITIVE_INFINITY;
        const next = prev.map((m) => {
          const fetched = loaded.get(m.id);
          if (!fetched) return m;
          if ((fetched.version ?? 0) < (m.version ?? 0)) {
            floor = Math.min(floor, fetched.version ? fetched.version - 1 : 0);
          }
          return fetched;
        });
        const held = cursorRef.current;
        if (
          floor !== Number.POSITIVE_INFINITY &&
          held?.sessionId === sessionId
        ) {
          const version = Math.min(held.version, floor);
          cursorRef.current = version > 0 ? { sessionId, version } : null;
        }
        return next;
      });
    } catch (e: unknown) {
      clientLog.warn("use-chat-session", "loading earlier blocks failed", {
        sessionId,
        error: e instanceof Error ? e.message : String(e),
      });
    } finally {
      earlierInFlightRef.current = false;
      setLoadingEarlierBlocks(false);
    }
  }, [sessionId, setMessages]);

  // hasEarlierBlocks:还有更早的正文没取回来 —— 转录顶部据此给出继续往上取的入口。
  const hasEarlierBlocks = useMemo(
    () => hasUnloadedBlocks(messages),
    [messages],
  );

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
    hasEarlierBlocks,
    loadingEarlierBlocks,
    loadEarlierBlocks,
  };
}
