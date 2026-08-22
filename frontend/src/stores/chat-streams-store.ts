import { create } from "zustand";

import type { ChatStreamEvent, ChatStreamUsage } from "@/hooks/use-chat-stream";

import type { chat_svc, view } from "../../wailsjs/go/models";
import { useSessionStatusStore, type DoneEvent } from "./session-status-store";
import { useQueuedMessagesStore } from "./queued-messages-store";

// ChatBlockData 是 Wails 生成的 chat_svc.ChatBlock 的「纯数据形态」——去掉自动注入的
// convertValues 方法，方便前端用对象字面量构造 / 在 store 内拼装。Wails 实际下行的
// ChatBlock 实例（含 convertValues）也结构性满足这个类型，因此渲染路径同时接受两者。
export type ChatBlockData = Omit<chat_svc.ChatBlock, "convertValues">;
export type ChatBlockSubagentData = Omit<
  chat_svc.ChatBlockSubagent,
  "convertValues"
>;
type LiveToolUseInput = Omit<ChatBlockData, "type" | "subagent"> & {
  subagent?: ChatBlockSubagentData;
};

// ToolApprovalData 是 agent 内置写工具(org / workflow 等)审批卡片的纯
// 数据形态,逐字对齐后端 chat_svc.ChatBlockToolApproval(去掉 wails 注入的 convertValues)。
// 流事件 payload 与持久化/overlay block.toolApproval 都是这个形状,store/card 共用一份类型。
export type ToolApprovalData = Omit<
  chat_svc.ChatBlockToolApproval,
  "convertValues"
>;

// ExecApprovalData mirrors the presentation-safe OpenClaw Gateway approval
// projection. It deliberately contains no token, environment, or systemRunPlan.
export type ExecApprovalData = Omit<
  chat_svc.ChatBlockExecApproval,
  "convertValues"
>;

export type RetryNotice = {
  attempt: number;
  maxAttempts: number;
  message: string;
  details: string;
  at: number;
};

// LiveStream 是「该 session 当前正在跑的一轮 turn 的全部前端可见状态」。
// 把它放到全局 store(而不是 ChatPanel 内部 state)的原因:
//   - 用户切到 /projects 时 ChatPage 整棵 unmount,自管 state 会被销毁、
//     <StreamSubscriber> 一并 EventsOff,后端继续推但前端再收不到。
//   - 流式期间到达的 tool_use / tool_result 必须有个地方落,否则切回来时即使重新订阅也丢历史。
//
// 字段含义:
//   - name: Wails 事件流名(后端 SendResponse.Stream),供 ChatStreamsHost 挂 EventsOn。
//   - liveDelta: 尾部还没冻结成 TextBlock 的文字。遇到 tool_use 时整段冻进 liveBlocks,清空。
//   - liveBlocks: 已按真实顺序冻结的文字 / tool_use / tool_result。渲染时摆在 persisted blocks
//     之后、liveDelta 之前。
//   - liveThinking: 单独累计的思考链。Anthropic 协议要求 thinking 一轮一个,在 turn 开头,
//     所以前端也不穿插,统一让 renderer 摆到 liveBlocks 前面。
export type LiveStream = {
  name: string;
  sessionId: number;
  assistantMessageId: number;
  streamStartedAt: number;
  liveDelta: string;
  liveThinking: string;
  liveBlocks: ChatBlockData[];
  liveRetry: RetryNotice | null;
  // liveUsage 由后端 usage 事件推上来：turn 内每次模型 API call 边界一条，
  // 携带 当前 assistant 消息的 per-call token 快照。Composer 进度条优先用它
  // 覆盖 messages 扫描结果，实现 turn 内随工具循环阶梯式刷新「已用上下文」。
  // stream 结束销毁 entry 后回落到 messages-based 计算（持久化 token 列已是
  // 最终值）。
  liveUsage: ChatStreamUsage | null;
  // liveContextWindow 是 runtime 在 turn 内探到的模型窗口。usage 事件可与
  // liveUsage 原子写入；独立 session_status patch 仍用于非 usage 来源与轮末刷新。
  // LoadSession 初始 contextWindow 可能为 0；这里让 Composer 在 turn 内显示真实窗口。
  liveContextWindow: number;
  // liveCompacting 由后端 runtime_status 事件驱动:claudecode CLI 在 /compact 启动
  // (manual 或 auto) 时推 status:"compacting",chat_svc 翻译成 RuntimeStatus
  // {compacting:true}。前端据此把 typing indicator 替换为"正在压缩上下文…" chip。
  // 在 appendLiveCompactBoundary / finishStream / consumeSteer 自动清回 false,
  // 不依赖 CLI 再推一帧 status:"" 来清旗。
  liveCompacting: boolean;
  // 本轮计时（与后端 turn/timing.go 同口径）：firstTokenAt 第一次 chunk/thinking
  // （TTFT）；burstStartedAt 非 null 表示生成表正在走，值是本次开表时刻；
  // generationMs 是已经收口的各次 API call 生成窗口之和；callStartedAt 是当前这次
  // call 最早可能开始生成的时刻（兜底窗口的左端）；sawVisibleToken 记这次 call 有没有
  // 吐过字；pendingTools 是正在执行、把这一跳按住的外层工具。
  // 分母 = 各次 call 的生成窗口之和：等首 token 的排队不算，工具执行不算。
  firstTokenAt: number | null;
  burstStartedAt: number | null;
  callStartedAt: number | null;
  sawVisibleToken: boolean;
  generationMs: number;
  pendingTools: string[];
  // turnCompletionTokens / turnReasoningTokens 按 usage 帧累加（每帧是该次 API call
  // 的快照，不是增量）。liveUsage 仍是最近一帧，Composer 上下文条读它的 totalInputTokens。
  turnCompletionTokens: number;
  turnReasoningTokens: number;
};

// State.streams 是 **两层** Map:sessionId → (assistantMessageId → LiveStream)。
//
// 一个会话在同一时刻合法地可以有多条流并存,它们各自绑定不同的 assistant 消息:
//   - 用户轮        —— doSend 拿到 SendChatMessage.stream 后 openStream;
//   - 自主续轮      —— 后台任务完成,CLI 自主跑的一轮(chat:autonomous 旁路);
//   - 后台 subagent 活动轮 —— 绑在**已存在**的发起消息上。
// 后两者由后台任务驱动、与用户操作无关,随时可能与用户轮重叠。早先这里是单层
// `Map<sessionId, LiveStream>`,openStream 无条件覆盖 —— 用户在自主续轮流式中再发
// 一条消息就会把自主轮已经流到屏幕、尚未落库的内容整段抹掉(用户可见症状:「已输出
// 内容清空回退」),订阅也被切走导致该轮后续事件无人接收(sess-1950)。
//
// 嵌套而非 `Map<`${sid}:${mid}`, LiveStream>` 复合键:会话级 selector
// (`s.streams.get(sessionId)`)保持 O(1) 且引用稳定 —— 别的会话在流不会让本
// panel 重渲染。
type State = {
  streams: Map<number, Map<number, LiveStream>>;
};

type Actions = {
  openStream: (
    s: Omit<
      LiveStream,
      | "liveDelta"
      | "liveThinking"
      | "liveBlocks"
      | "liveRetry"
      | "liveUsage"
      | "liveContextWindow"
      | "liveCompacting"
      | "firstTokenAt"
      | "burstStartedAt"
      | "callStartedAt"
      | "sawVisibleToken"
      | "generationMs"
      | "pendingTools"
      | "turnCompletionTokens"
      | "turnReasoningTokens"
    >,
  ) => void;
  closeStream: (sessionId: number, assistantMessageId: number) => void;
  appendLiveText: (
    sessionId: number,
    assistantMessageId: number,
    delta: string,
  ) => void;
  appendLiveThinking: (
    sessionId: number,
    assistantMessageId: number,
    delta: string,
  ) => void;
  // 接受不带 type 的 partial:action 会统一 stamp 成 "tool_use" / "tool_result",
  // 避免每个 caller 都重复填同样的字段。
  appendLiveToolUse: (
    sessionId: number,
    assistantMessageId: number,
    block: LiveToolUseInput,
  ) => void;
  appendLiveToolResult: (
    sessionId: number,
    assistantMessageId: number,
    block: Omit<ChatBlockData, "type">,
  ) => void;
  // appendLivePlanUpdate 在 stream 上插入或更新 Codex/Cli runtime 上报的计划。
  // 只保留本轮 turn 的最新一张 plan block,避免 update_plan/plan delta 多次到达
  // 时刷出一串重复计划卡。
  appendLivePlanUpdate: (
    sessionId: number,
    assistantMessageId: number,
    text: string,
    canonical?: view.CanonicalDTO,
  ) => void;
  // mergeSubagentMeta 把 subagent_started/progress/done/model 事件携带的元数据合并到
  // 对应外层 Agent tool_use block 上（按 toolUseId 匹配 liveBlocks 里最近一个）。
  // 字段做浅 merge；runs 是完整快照，出现时整段替换，undefined/省略时保留旧值。
  // subagent_model 只带 model 一个字段，避免其它空字段清掉已累计进度。
  mergeSubagentMeta: (
    sessionId: number,
    assistantMessageId: number,
    toolUseId: string,
    meta: ChatBlockSubagentData,
  ) => void;
  // appendLiveAskUserQuestion 在 stream 上插入 AskUserQuestion 卡片：与 tool_use
  // 类似先 flush liveDelta 把文字定型，再追加一个 type:"ask_user_question" block。
  // canonical 由后端 dispatcher_emitter 同步附上(canonical.UserAsk),让
  // CanonicalToolRouter 在 live 路径与 replay 路径走同一份卡片。
  appendLiveAskUserQuestion: (
    sessionId: number,
    assistantMessageId: number,
    payload: chat_svc.ChatBlockAskUserQuestion,
    canonical?: view.CanonicalDTO,
  ) => void;
  // markAskUserQuestionAnswered 按 requestId 找到对应 block（live 或重渲染历史），
  // 更新 Answered/Answers/Skipped 字段。用户提交答案时调用，做乐观更新。
  markAskUserQuestionAnswered: (
    sessionId: number,
    assistantMessageId: number,
    payload: chat_svc.ChatBlockAskUserQuestion,
    canonical?: view.CanonicalDTO,
  ) => void;
  // appendLiveToolPermissionRequest 在 stream 上插入工具审批卡片，与
  // appendLiveAskUserQuestion 同款 flushText 流程。
  // canonical 必须随 payload 一起落到 block 上,否则 CanonicalToolRouter 不认识
  // kind=tool.permission 会 fallback 到 RawToolCard (空标题"tool" + 简化 overlay)。
  appendLiveToolPermissionRequest: (
    sessionId: number,
    assistantMessageId: number,
    payload: chat_svc.ChatBlockToolPermission,
    canonical?: view.CanonicalDTO,
  ) => void;
  // markToolPermissionResolved 按 requestId 找到对应 block 更新决策态。
  // 用户点 Allow/Deny 时调用做乐观更新；后端确认到来时再次调以兜底。
  // canonical 可选:乐观更新路径没有完整 canonical 也能跑(store 内部按 existing
  // canonical 合成 resolved/allowed 标志);后端 echo 路径直接传整份新 canonical。
  markToolPermissionResolved: (
    sessionId: number,
    assistantMessageId: number,
    payload: chat_svc.ChatBlockToolPermission,
    canonical?: view.CanonicalDTO,
  ) => void;
  // appendLiveToolApproval 在 stream 上插入内置写工具审批卡片(status:"pending"),
  // 与 appendLiveToolPermissionRequest 同款 flushText 流程。审批卡不走
  // CanonicalToolRouter,直接按 block.type==="tool_approval" 由 transcript 路由。
  appendLiveToolApproval: (
    sessionId: number,
    assistantMessageId: number,
    payload: ToolApprovalData,
  ) => void;
  // markToolApprovalResolved 按 toolApproval.requestId 找到对应 block 覆盖 status/result。
  // 后端 emit approved/denied/expired 决议更新(同 requestId)时调用;未知 requestId no-op。
  markToolApprovalResolved: (
    sessionId: number,
    assistantMessageId: number,
    payload: ToolApprovalData,
  ) => void;
  // OpenClaw exec approvals are a lifecycle separate from tool completion.
  // Requested cards flush pending text; terminal events merge by Gateway id.
  appendLiveExecApproval: (
    sessionId: number,
    assistantMessageId: number,
    payload: ExecApprovalData,
  ) => void;
  markExecApprovalResolved: (
    sessionId: number,
    assistantMessageId: number,
    payload: ExecApprovalData,
  ) => void;
  // patchLiveUsage 把后端推来的 per-call usage 快照写到 LiveStream.liveUsage 上；
  // usage.contextWindow>0 时同一次 state 更新写入 liveContextWindow，保证收到的任一
  // usage 都有匹配分母。turn 结束 entry 被销毁后回落到 messages 扫描。无 stream
  // entry 时静默丢弃；Pi 会在每个 usage 快照重带 contextWindow，后续帧可兜回。
  patchLiveUsage: (
    sessionId: number,
    assistantMessageId: number,
    usage: ChatStreamUsage,
  ) => void;
  // appendLiveCompactBoundary 把后端 compact_boundary 事件落成一个 live block。
  // 先 flush liveDelta(让边界前已经流出的文本固化为 text block),再插入
  // type=compact_boundary 块,之后流出的内容会落在边界之后。
  appendLiveCompactBoundary: (
    sessionId: number,
    assistantMessageId: number,
    compact: { preTokens?: number; trigger?: "auto" | "manual"; at: number },
  ) => void;
  // patchLiveContextWindow 写入 runtime mid-turn 探到的模型窗口大小。
  patchLiveContextWindow: (
    sessionId: number,
    assistantMessageId: number,
    contextWindow: number,
  ) => void;
  // setLiveCompacting 设置/清空 liveCompacting 旗。chat-streams-host 在收到
  // runtime_status 事件时按 ev.runtimeStatus.compacting 调用。无 stream entry 时
  // 静默丢弃,避免 race (status 帧先于 openStream)。
  setLiveCompacting: (
    sessionId: number,
    assistantMessageId: number,
    compacting: boolean,
  ) => void;
  setLiveRetry: (
    sessionId: number,
    assistantMessageId: number,
    retry: RetryNotice,
  ) => void;
  // clearLiveRetry 把 liveRetry 置空。retry 是「正在等下次尝试」的瞬时态,下一个非 retry
  // 的进展事件(chunk/thinking/tool_use 等)到达 = 重试已成功 = 状态过期。由 ChatStreamsHost
  // 在那些事件入口顺手调一次,避免回复内容已经流出来了 RetryNoticeCard 还挂着。
  // 无 stream 或 liveRetry 本就是 null 时是 referential no-op,不触发 zustand 重渲染。
  clearLiveRetry: (sessionId: number, assistantMessageId: number) => void;
  // finishStream 统一处理 done/error/closed:写入 lastDoneEvent 缓存、bump tick、删掉**这一条**
  // stream entry;该会话再无流在跑时才清空排队。给 host 一处调用,避免散落多分支。
  finishStream: (
    sessionId: number,
    assistantMessageId: number,
    event: ChatStreamEvent,
  ) => void;
  // consumeSteer 处理后端确认已消费的排队消息：清掉对应 queue chip，
  // 把 live stream 切到新的 assistant 占位，并 bump tick 让 ChatPanel reload 消息段。
  consumeSteer: (
    sessionId: number,
    assistantMessageId: number,
    event: ChatStreamEvent,
  ) => void;
};

// flushLiveSegment 把当前段(thinking → text)冻成 block。thinking 先于 text ——
// 与流序(thinking_delta... text_delta...)一致,同段内 thinking 永远在 text 前。
// 过去只 flush liveDelta(text),liveThinking 是独立字段由 renderer 统一抬到
// liveBlocks 前 —— 工具循环里后几轮的 thinking 被全堆到最顶(用户可见症状:
// 「思考完成过程都在最顶部叠加」)。这里改为在 tool_use/plan/ask 等边界一并
// 把 liveThinking 落成 thinking block,让 liveBlocks 保持真实时间顺序。
// noteVisibleToken 记首 token 并开表。表没在走就从这一刻起算生成 —— 之前那段是
// 等首 token 的排队/prefill，不是生成。工具还挂着就说明它的结果丢了（中断/过滤），
// 模型既然开口了就清账重新起算，否则表被永久按住、分母塌回几十毫秒。
function noteVisibleToken(s: LiveStream, now: number): LiveStream {
  const lostTools = s.pendingTools.length > 0;
  return {
    ...s,
    firstTokenAt: s.firstTokenAt ?? now,
    burstStartedAt: s.burstStartedAt ?? now,
    callStartedAt: lostTools ? now : s.callStartedAt,
    sawVisibleToken: true,
    pendingTools: lostTools ? [] : s.pendingTools,
  };
}

/**
 * endCall 收口当前这次 API call 的生成窗口。
 *
 * 吐过字 → 窗口是 [首个增量, 现在]；一个字没吐就甩了工具调用 → 没有增量能框窗口，
 * 退回按这一跳的墙钟算（它的 output token 照样进分子，分母给 0 就是白嫖）。正卡在
 * 工具执行里则什么都不加 —— 那段不是生成。
 */
function endCall(s: LiveStream, now: number): LiveStream {
  let generationMs = s.generationMs;
  if (s.burstStartedAt != null) {
    generationMs += Math.max(0, now - s.burstStartedAt);
  } else if (
    !s.sawVisibleToken &&
    s.pendingTools.length === 0 &&
    s.callStartedAt != null
  ) {
    generationMs += Math.max(0, now - s.callStartedAt);
  }
  return {
    ...s,
    generationMs,
    burstStartedAt: null,
    sawVisibleToken: false,
    callStartedAt: now,
  };
}

/** suspendClock 收口这一跳：toolUseId 开始执行，模型这一跳说完了。 */
function suspendClock(
  s: LiveStream,
  toolUseId: string,
  now: number,
): LiveStream {
  const ended = endCall(s, now);
  return ended.pendingTools.includes(toolUseId)
    ? ended
    : { ...ended, pendingTools: [...ended.pendingTools, toolUseId] };
}

/** resumeClock 工具结果回来：下一跳从这一刻起算（并行工具全部回齐才算）。 */
function resumeClock(
  s: LiveStream,
  toolUseId: string,
  now: number,
): LiveStream {
  if (!s.pendingTools.includes(toolUseId)) return s;
  const pendingTools = s.pendingTools.filter((id) => id !== toolUseId);
  return {
    ...s,
    pendingTools,
    callStartedAt: pendingTools.length === 0 ? now : s.callStartedAt,
    sawVisibleToken: pendingTools.length === 0 ? false : s.sawVisibleToken,
  };
}

function flushLiveSegment(s: LiveStream): LiveStream {
  if (s.liveDelta.length === 0 && s.liveThinking.length === 0) return s;
  const nextBlocks = [...s.liveBlocks];
  if (s.liveThinking.length > 0) {
    nextBlocks.push({
      type: "thinking",
      text: s.liveThinking,
    } as ChatBlockData);
  }
  if (s.liveDelta.length > 0) {
    nextBlocks.push({ type: "text", text: s.liveDelta } as ChatBlockData);
  }
  return { ...s, liveBlocks: nextBlocks, liveDelta: "", liveThinking: "" };
}

// ── 两层 Map 的读写辅助 ────────────────────────────────────────────────────────
// 所有 action 都经 updateStream 改一条流,嵌套 Map 的不可变拷贝只在这一处做。

/** sessionStreamMap 取某会话的全部在流(引用稳定,可直接喂 zustand selector)。 */
export function sessionStreamMap(
  state: State,
  sessionId: number,
): Map<number, LiveStream> | null {
  return state.streams.get(sessionId) ?? null;
}

/** streamForMessage 取绑在某条 assistant 消息上的流。 */
export function streamForMessage(
  state: State,
  sessionId: number,
  assistantMessageId: number,
): LiveStream | null {
  return state.streams.get(sessionId)?.get(assistantMessageId) ?? null;
}

/**
 * primaryStream 取会话的「主流」= 最近一次 openStream 的那条。
 *
 * 会话级读数(composer 进度条的 liveUsage / liveContextWindow、typing indicator 的
 * liveCompacting、停止按钮的 canStop)都读它:这些是「这个会话此刻在干什么」的
 * 概览,不需要 per-message 精度。逐条消息的流式内容走 streamForMessage。
 */
export function primaryStream(
  state: State,
  sessionId: number,
): LiveStream | null {
  const perMessage = state.streams.get(sessionId);
  if (!perMessage || perMessage.size === 0) return null;
  let best: LiveStream | null = null;
  for (const s of perMessage.values()) {
    if (!best || s.streamStartedAt >= best.streamStartedAt) best = s;
  }
  return best;
}

/** hasSessionStream 判会话是否有任意一条流在跑。 */
export function hasSessionStream(state: State, sessionId: number): boolean {
  const perMessage = state.streams.get(sessionId);
  return !!perMessage && perMessage.size > 0;
}

/**
 * updateStream 对一条流做不可变更新。updater 返回 null 或原引用 → 整体 no-op
 * (返回原 state,不重建 Map,zustand 不会触发多余重渲染)。
 */
function updateStream(
  state: State,
  sessionId: number,
  assistantMessageId: number,
  updater: (cur: LiveStream) => LiveStream | null,
): State {
  const perMessage = state.streams.get(sessionId);
  const cur = perMessage?.get(assistantMessageId);
  if (!perMessage || !cur) return state;
  const updated = updater(cur);
  if (!updated || updated === cur) return state;
  const nextPerMessage = new Map(perMessage);
  nextPerMessage.set(assistantMessageId, updated);
  const streams = new Map(state.streams);
  streams.set(sessionId, nextPerMessage);
  return { streams };
}

/** dropStream 删掉一条流;会话最后一条被删时连会话的空 Map 一起摘掉。 */
function dropStream(
  state: State,
  sessionId: number,
  assistantMessageId: number,
): State {
  const perMessage = state.streams.get(sessionId);
  if (!perMessage || !perMessage.has(assistantMessageId)) return state;
  const streams = new Map(state.streams);
  const nextPerMessage = new Map(perMessage);
  nextPerMessage.delete(assistantMessageId);
  if (nextPerMessage.size === 0) streams.delete(sessionId);
  else streams.set(sessionId, nextPerMessage);
  return { streams };
}

/**
 * findLastBlockIndex 倒序找最近一个满足 pred 的 live block。同一 id 在一轮里通常
 * 只出现一次,但倒序更稳 —— 能正确处理流式过程中尚未匹配到的边界态。
 */
function findLastBlockIndex(
  blocks: ChatBlockData[],
  pred: (b: ChatBlockData) => boolean,
): number {
  for (let i = blocks.length - 1; i >= 0; i--) {
    if (pred(blocks[i])) return i;
  }
  return -1;
}

/** replaceBlock 返回把第 idx 块换成 next 的新数组。 */
function replaceBlock(
  blocks: ChatBlockData[],
  idx: number,
  next: ChatBlockData,
): ChatBlockData[] {
  const out = [...blocks];
  out[idx] = next;
  return out;
}

export const useChatStreamsStore = create<State & Actions>((set) => ({
  streams: new Map(),

  openStream: (s) =>
    set((state) => {
      const streams = new Map(state.streams);
      // 只 set 自己那条 —— 同会话其它流(自主续轮 / 后台 subagent 活动轮)原样保留。
      const perMessage = new Map(streams.get(s.sessionId) ?? []);
      perMessage.set(s.assistantMessageId, {
        ...s,
        liveDelta: "",
        liveThinking: "",
        liveBlocks: [],
        liveRetry: null,
        liveUsage: null,
        liveContextWindow: 0,
        liveCompacting: false,
        firstTokenAt: null,
        // 表要等模型真的开口才走；这里只记下这一跳最早可能开始生成的时刻。
        burstStartedAt: null,
        callStartedAt: Date.now(),
        sawVisibleToken: false,
        generationMs: 0,
        pendingTools: [],
        turnCompletionTokens: 0,
        turnReasoningTokens: 0,
      });
      streams.set(s.sessionId, perMessage);
      return { streams };
    }),

  closeStream: (sessionId, assistantMessageId) =>
    set((state) => dropStream(state, sessionId, assistantMessageId)),

  appendLiveText: (sessionId, assistantMessageId, delta) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) =>
        delta
          ? noteVisibleToken(
              { ...cur, liveDelta: cur.liveDelta + delta },
              Date.now(),
            )
          : null,
      ),
    ),

  appendLiveThinking: (sessionId, assistantMessageId, delta) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) =>
        delta
          ? noteVisibleToken(
              { ...cur, liveThinking: cur.liveThinking + delta },
              Date.now(),
            )
          : null,
      ),
    ),

  patchLiveUsage: (sessionId, assistantMessageId, usage) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) => {
        // 同值短路：所有 token 字段一致就不重建 Map，避免 zustand 触发多余重渲染。
        // 消息 id 也比一下 —— turn 内换 assistant 段（steer_consumed）时它会变。
        const prev = cur.liveUsage;
        const contextWindow = usage.contextWindow ?? 0;
        const nextContextWindow =
          contextWindow > 0 ? contextWindow : cur.liveContextWindow;
        if (
          prev &&
          prev.messageId === usage.messageId &&
          prev.promptTokens === usage.promptTokens &&
          prev.completionTokens === usage.completionTokens &&
          prev.cachedTokens === usage.cachedTokens &&
          prev.cacheCreationTokens === usage.cacheCreationTokens &&
          prev.reasoningTokens === usage.reasoningTokens &&
          prev.totalInputTokens === usage.totalInputTokens &&
          prev.contextWindow === usage.contextWindow &&
          cur.liveContextWindow === nextContextWindow
        ) {
          return null;
        }
        // 不停表：usage 是某次内部 API call 的收尾，模型紧接着就发工具调用或
        // 下一段正文。停表的唯一理由是工具开始执行（见 appendLiveToolUse）。
        return {
          ...cur,
          liveUsage: usage,
          liveContextWindow: nextContextWindow,
          turnCompletionTokens:
            cur.turnCompletionTokens + (usage.completionTokens ?? 0),
          turnReasoningTokens:
            cur.turnReasoningTokens + (usage.reasoningTokens ?? 0),
        };
      }),
    ),

  patchLiveContextWindow: (sessionId, assistantMessageId, contextWindow) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) =>
        contextWindow > 0 && cur.liveContextWindow !== contextWindow
          ? { ...cur, liveContextWindow: contextWindow }
          : null,
      ),
    ),

  setLiveCompacting: (sessionId, assistantMessageId, compacting) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) =>
        cur.liveCompacting === compacting
          ? null
          : { ...cur, liveCompacting: compacting },
      ),
    ),

  setLiveRetry: (sessionId, assistantMessageId, retry) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) => ({
        ...cur,
        liveRetry: retry,
      })),
    ),

  clearLiveRetry: (sessionId, assistantMessageId) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) =>
        cur.liveRetry === null ? null : { ...cur, liveRetry: null },
      ),
    ),

  appendLiveToolUse: (sessionId, assistantMessageId, block) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) => {
        // 内层（subagent 内部）工具不碰表 —— 派遣它的那个外层 Task 调用已经把表
        // 按住了，内层再加减一遍只会在孤儿帧上留下按死表的挂账。
        const segment = flushLiveSegment(cur);
        const flushed =
          block.parentToolUseId || !block.toolUseId
            ? segment
            : suspendClock(segment, block.toolUseId, Date.now());
        return {
          ...flushed,
          liveBlocks: [
            ...flushed.liveBlocks,
            { ...block, type: "tool_use" } as ChatBlockData,
          ],
        };
      }),
    ),

  appendLiveToolResult: (sessionId, assistantMessageId, block) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) => {
        // 工具跑完，模型要接着生成了 → 重新开表（并行工具全部回齐才真开）。
        const resumed =
          block.parentToolUseId || !block.toolUseId
            ? cur
            : resumeClock(cur, block.toolUseId, Date.now());
        return {
          // 故意不 flush liveDelta:tool_use→tool_result 之间通常没有用户可见的文字,
          // 把累积的 liveDelta 留给"下一段文字 + 下次 tool_use"那个 flush 时机。
          ...resumed,
          liveBlocks: [
            ...resumed.liveBlocks,
            { ...block, type: "tool_result" },
          ],
        };
      }),
    ),

  appendLiveCompactBoundary: (sessionId, assistantMessageId, compact) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) => {
        const flushed = flushLiveSegment(cur);
        return {
          ...flushed,
          liveBlocks: [
            ...flushed.liveBlocks,
            {
              type: "compact_boundary",
              compact: {
                preTokens: compact.preTokens,
                trigger: compact.trigger,
                at: compact.at,
              },
            },
          ],
          // compact_boundary 到达 = 压缩流程结束 → 自动清 liveCompacting,
          // 不依赖 CLI 显式再推一帧 status:"" 清旗。
          liveCompacting: false,
        };
      }),
    ),

  appendLivePlanUpdate: (sessionId, assistantMessageId, text, canonical) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) => {
        const hasPlanPayload = text || canonical?.kind === "plan.update";
        const planText = hasPlanPayload
          ? (canonical?.planUpdate?.text ?? text)
          : text;
        if (!planText && canonical?.kind !== "plan.update") return null;
        const flushed = flushLiveSegment(cur);
        const nextBlock: ChatBlockData = {
          type: "plan",
          text: planText,
          canonical,
        };
        const targetIdx = flushed.liveBlocks.findIndex(
          (b) => b.type === "plan" && b.canonical?.kind === "plan.update",
        );
        return {
          ...flushed,
          liveBlocks:
            targetIdx >= 0
              ? replaceBlock(flushed.liveBlocks, targetIdx, nextBlock)
              : [...flushed.liveBlocks, nextBlock],
        };
      }),
    ),

  appendLiveAskUserQuestion: (
    sessionId,
    assistantMessageId,
    payload,
    canonical,
  ) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) => {
        if (!payload || !payload.requestId) return null;
        const flushed = flushLiveSegment(cur);
        return {
          ...flushed,
          liveBlocks: [
            ...flushed.liveBlocks,
            {
              type: "ask_user_question",
              askUserQuestion: payload,
              canonical,
            },
          ],
        };
      }),
    ),

  markAskUserQuestionAnswered: (
    sessionId,
    assistantMessageId,
    payload,
    canonical,
  ) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) => {
        if (!payload || !payload.requestId) return null;
        const targetIdx = findLastBlockIndex(
          cur.liveBlocks,
          (b) =>
            b.type === "ask_user_question" &&
            b.askUserQuestion?.requestId === payload.requestId,
        );
        if (targetIdx < 0) return null;
        const existing = cur.liveBlocks[targetIdx];
        const merged: ChatBlockData = {
          ...existing,
          askUserQuestion: {
            ...(existing.askUserQuestion ?? payload),
            ...payload,
          } as chat_svc.ChatBlockAskUserQuestion,
          canonical: canonical ?? existing.canonical,
        };
        return {
          ...cur,
          liveBlocks: replaceBlock(cur.liveBlocks, targetIdx, merged),
        };
      }),
    ),

  appendLiveToolPermissionRequest: (
    sessionId,
    assistantMessageId,
    payload,
    canonical,
  ) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) => {
        if (!payload || !payload.requestId) return null;
        const flushed = flushLiveSegment(cur);
        return {
          ...flushed,
          liveBlocks: [
            ...flushed.liveBlocks,
            {
              type: "tool_permission_request",
              toolPermission: payload,
              canonical,
            },
          ],
        };
      }),
    ),

  markToolPermissionResolved: (
    sessionId,
    assistantMessageId,
    payload,
    canonical,
  ) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) => {
        if (!payload || !payload.requestId) return null;
        const targetIdx = findLastBlockIndex(
          cur.liveBlocks,
          (b) =>
            b.type === "tool_permission_request" &&
            b.toolPermission?.requestId === payload.requestId,
        );
        if (targetIdx < 0) return null;
        const existing = cur.liveBlocks[targetIdx];
        const mergedSidecar = {
          ...(existing.toolPermission ?? payload),
          ...payload,
        } as chat_svc.ChatBlockToolPermission;
        // canonical 同步更新,避免乐观更新 race —— 新卡读 canonical 为 truth。
        // 调用方传了完整 canonical(后端 echo 路径)直接覆盖;否则按 existing canonical
        // 拷出新对象,只推进 resolved/allowed/alwaysAllow 三个标志位(乐观更新路径)。
        // toolPermission/planApprove 在对应 kind 下必填(后端 dispatcher_emitter 保证),
        // 但 wails 生成 TS 类型为 optional + class 含 convertValues,所以用 cast
        // 构造纯数据对象(运行时只用字段,不调 convertValues)。
        let mergedCanonical = canonical ?? existing.canonical;
        if (
          !canonical &&
          mergedCanonical?.kind === "tool.permission" &&
          mergedCanonical.toolPermission
        ) {
          mergedCanonical = {
            ...mergedCanonical,
            toolPermission: {
              ...mergedCanonical.toolPermission,
              resolved: !!payload.resolved,
              allowed: !!payload.allowed,
              alwaysAllow: !!payload.alwaysAllow,
            },
          } as view.CanonicalDTO;
        } else if (
          !canonical &&
          mergedCanonical?.kind === "plan.approve_request" &&
          mergedCanonical.planApprove
        ) {
          mergedCanonical = {
            ...mergedCanonical,
            planApprove: {
              ...mergedCanonical.planApprove,
              resolved: !!payload.resolved,
              allowed: !!payload.allowed,
            },
          } as view.CanonicalDTO;
        }
        const merged: ChatBlockData = {
          ...existing,
          toolPermission: mergedSidecar,
          canonical: mergedCanonical,
        };
        return {
          ...cur,
          liveBlocks: replaceBlock(cur.liveBlocks, targetIdx, merged),
        };
      }),
    ),

  appendLiveToolApproval: (sessionId, assistantMessageId, payload) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) => {
        if (!payload || !payload.requestId) return null;
        const flushed = flushLiveSegment(cur);
        return {
          ...flushed,
          liveBlocks: [
            ...flushed.liveBlocks,
            {
              type: "tool_approval",
              toolApproval: payload,
            },
          ],
        };
      }),
    ),

  markToolApprovalResolved: (sessionId, assistantMessageId, payload) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) => {
        if (!payload || !payload.requestId) return null;
        const targetIdx = findLastBlockIndex(
          cur.liveBlocks,
          (b) =>
            b.type === "tool_approval" &&
            b.toolApproval?.requestId === payload.requestId,
        );
        // 未知 requestId no-op:不重建 Map,避免触发多余重渲染。
        if (targetIdx < 0) return null;
        const existing = cur.liveBlocks[targetIdx];
        const merged: ChatBlockData = {
          ...existing,
          toolApproval: {
            ...(existing.toolApproval ?? payload),
            ...payload,
          } as ToolApprovalData,
        };
        return {
          ...cur,
          liveBlocks: replaceBlock(cur.liveBlocks, targetIdx, merged),
        };
      }),
    ),

  appendLiveExecApproval: (sessionId, assistantMessageId, payload) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) => {
        if (!payload?.id) return null;
        const existingIdx = findLastBlockIndex(
          cur.liveBlocks,
          (block) =>
            block.type === "exec_approval" &&
            block.execApproval?.id === payload.id,
        );
        if (existingIdx >= 0) {
          const existing = cur.liveBlocks[existingIdx];
          return {
            ...cur,
            liveBlocks: replaceBlock(cur.liveBlocks, existingIdx, {
              ...existing,
              execApproval: {
                ...(existing.execApproval ?? payload),
                ...payload,
              } as ExecApprovalData,
            }),
          };
        }
        const flushed = flushLiveSegment(cur);
        return {
          ...flushed,
          liveBlocks: [
            ...flushed.liveBlocks,
            { type: "exec_approval", execApproval: payload },
          ],
        };
      }),
    ),

  markExecApprovalResolved: (sessionId, assistantMessageId, payload) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) => {
        if (!payload?.id) return null;
        const targetIdx = findLastBlockIndex(
          cur.liveBlocks,
          (block) =>
            block.type === "exec_approval" &&
            block.execApproval?.id === payload.id,
        );
        if (targetIdx < 0) return null;
        const existing = cur.liveBlocks[targetIdx];
        return {
          ...cur,
          liveBlocks: replaceBlock(cur.liveBlocks, targetIdx, {
            ...existing,
            execApproval: {
              ...(existing.execApproval ?? payload),
              ...payload,
            } as ExecApprovalData,
          }),
        };
      }),
    ),

  mergeSubagentMeta: (sessionId, assistantMessageId, toolUseId, meta) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) => {
        if (!toolUseId) return null;
        const targetIdx = findLastBlockIndex(
          cur.liveBlocks,
          (b) => b.type === "tool_use" && b.toolUseId === toolUseId,
        );
        if (targetIdx < 0) return null;
        const target = cur.liveBlocks[targetIdx];
        const { runs, ...patch } = meta;
        const merged: ChatBlockData = {
          ...target,
          subagent: {
            ...(target.subagent ?? {}),
            ...patch,
            ...(runs !== undefined ? { runs } : {}),
          } as chat_svc.ChatBlockSubagent,
        };
        return {
          ...cur,
          liveBlocks: replaceBlock(cur.liveBlocks, targetIdx, merged),
        };
      }),
    ),

  finishStream: (sessionId, assistantMessageId, event) =>
    set((state) => {
      const next = dropStream(state, sessionId, assistantMessageId);
      // 排队消息属于「用户那一轮」。只有本会话再没有任何流在跑时才处理 —— 否则
      // 一条自主续轮收尾会把用户排给活跃用户轮的消息误清掉。
      // 不再静默丢弃:回合收尾时还没被 steer_consumed 消费的排队条目挪进 dropped,
      // 由 QueuedMessagesBar 提示用户「恢复为草稿 / 丢弃」,而不是无声清掉。
      if (!hasSessionStream(next, sessionId)) {
        useQueuedMessagesStore.getState().markDropped(sessionId);
      }
      useSessionStatusStore.getState().bumpDone(sessionId, event as DoneEvent);
      return next;
    }),

  consumeSteer: (sessionId, assistantMessageId, event) =>
    set((state) => {
      const perMessage = state.streams.get(sessionId);
      const cur = perMessage?.get(assistantMessageId);
      let streams = state.streams;
      if (perMessage && cur) {
        // steer 把本轮切到新的 assistant 占位 → 换 key 重挂,内容清零重来。
        const nextId = event.assistantMessage?.id ?? cur.assistantMessageId;
        const nextPerMessage = new Map(perMessage);
        nextPerMessage.delete(assistantMessageId);
        nextPerMessage.set(nextId, {
          ...cur,
          assistantMessageId: nextId,
          streamStartedAt: Date.now(),
          liveDelta: "",
          liveThinking: "",
          liveBlocks: [],
          liveRetry: null,
          // 新 assistant 段开始 → 清掉上一段的 compacting chip。
          liveCompacting: false,
          firstTokenAt: null,
          // 新 assistant 段开始 → 计时重来（表仍等模型开口才走）。
          burstStartedAt: null,
          callStartedAt: Date.now(),
          sawVisibleToken: false,
          generationMs: 0,
          pendingTools: [],
          turnCompletionTokens: 0,
          turnReasoningTokens: 0,
        });
        streams = new Map(state.streams);
        streams.set(sessionId, nextPerMessage);
      }

      const ids = event.queuedIds ?? [];
      if (ids.length > 0) {
        useQueuedMessagesStore.getState().consume(sessionId, ids);
      }

      useSessionStatusStore.getState().bumpDone(sessionId, event as DoneEvent);
      return { streams };
    }),
}));
