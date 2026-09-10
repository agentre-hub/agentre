// 在流那本账的纯函数:首 token 记账、工具空档停表/开表、分段冻结,以及按会话/消息取值。
//
// 全部是 (旧值, 现在) → 新值 的形状,不认识 store 也不改 store —— 变更点只在 store
// 的 action 里发生(见 updateStream 的注释)。

import { ChatBlockData, LiveStream, type State } from "./types";

// flushLiveSegment 把当前段(thinking → text)冻成 block。thinking 先于 text ——
// 与流序(thinking_delta... text_delta...)一致,同段内 thinking 永远在 text 前。
// 过去只 flush liveDelta(text),liveThinking 是独立字段由 renderer 统一抬到
// liveBlocks 前 —— 工具循环里后几轮的 thinking 被全堆到最顶(用户可见症状:
// 「思考完成过程都在最顶部叠加」)。这里改为在 tool_use/plan/ask 等边界一并
// 把 liveThinking 落成 thinking block,让 liveBlocks 保持真实时间顺序。
// noteVisibleToken 记首 token，并在表被按住时重新开表：模型开口说话 = 工具空档必然
// 已经结束。这同时是「工具结果因中断/过滤没回来」时的自愈，免得表被永久按住、分母
// 塌回几十毫秒。
export function noteVisibleToken(s: LiveStream, now: number): LiveStream {
  return {
    ...s,
    firstTokenAt: s.firstTokenAt ?? now,
    burstStartedAt: s.burstStartedAt ?? now,
    pendingTools: s.pendingTools.length === 0 ? s.pendingTools : [],
  };
}

// noteFirstToken 只记首 token，不碰表。给「模型确实在产出输出 token，但产出的东西
// 用户看不见」的信号用（output_activity；没有该事件的后端由 tool_use 兜底）。
//
// 与 noteVisibleToken 的区别是刻意的：那条要清挂账 + 重新开表（可见正文 = 工具空档
// 必然已结束的自愈），这条只补一个时间戳，不让新信号动到已经钉死的 tok/s 分母口径。
// 与后端 turn/timing.go 的 NoteOutputTokenAt 同口径。
//
// 没有它，一跳纯工具调用（一个字都不吐）时首 token 会一路推迟到模型终于开口说正文
// 那一刻：sess-3241 里 190.1s 的一轮报出 166.6s 的首 token，而在那之前整整 23 跳里
// 界面上的「首 token」就是一个不断增长的整轮耗时、tok/s 干脆不显示。

// noteFirstToken 只记首 token，不碰表。给「模型确实在产出输出 token，但产出的东西
// 用户看不见」的信号用（output_activity；没有该事件的后端由 tool_use 兜底）。
//
// 与 noteVisibleToken 的区别是刻意的：那条要清挂账 + 重新开表（可见正文 = 工具空档
// 必然已结束的自愈），这条只补一个时间戳，不让新信号动到已经钉死的 tok/s 分母口径。
// 与后端 turn/timing.go 的 NoteOutputTokenAt 同口径。
//
// 没有它，一跳纯工具调用（一个字都不吐）时首 token 会一路推迟到模型终于开口说正文
// 那一刻：sess-3241 里 190.1s 的一轮报出 166.6s 的首 token，而在那之前整整 23 跳里
// 界面上的「首 token」就是一个不断增长的整轮耗时、tok/s 干脆不显示。
export function noteFirstToken(s: LiveStream, now: number): LiveStream {
  return s.firstTokenAt == null ? { ...s, firstTokenAt: now } : s;
}

export function pauseBurst(s: LiveStream, now: number): LiveStream {
  if (s.burstStartedAt == null) return s;
  return {
    ...s,
    generationMs: s.generationMs + Math.max(0, now - s.burstStartedAt),
    burstStartedAt: null,
  };
}

/** suspendClock 停表：toolUseId 开始执行，这段工具空档不算。 */

export function suspendClock(
  s: LiveStream,
  toolUseId: string,
  now: number,
): LiveStream {
  const paused = pauseBurst(s, now);
  return paused.pendingTools.includes(toolUseId)
    ? paused
    : { ...paused, pendingTools: [...paused.pendingTools, toolUseId] };
}

/** resumeClock 开表：并行工具全部回齐才真的开表。 */

export function resumeClock(
  s: LiveStream,
  toolUseId: string,
  now: number,
): LiveStream {
  if (!s.pendingTools.includes(toolUseId)) return s;
  const pendingTools = s.pendingTools.filter((id) => id !== toolUseId);
  return {
    ...s,
    pendingTools,
    burstStartedAt:
      pendingTools.length === 0 ? (s.burstStartedAt ?? now) : s.burstStartedAt,
  };
}

export function flushLiveSegment(s: LiveStream): LiveStream {
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

export function updateStream(
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

export function dropStream(
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
