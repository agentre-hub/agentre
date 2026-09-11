// 流式会话的 store:一帧一帧到达的正文/思考/工具调用攒成 LiveStream,按会话与消息
// 索引,供聊天面板一行一行地读。
//
// 词汇在 chat-streams/types.ts,那份账的纯函数在 chat-streams/live.ts。
//
// 下面那段再导出是**有意的**:25 个消费者一直从 "@/stores/chat-streams-store" 取那些
// 类型与取值函数,拆文件不该让它们跟着改 import 路径。

import { create } from "zustand";

import {
  appendCompactBoundaryBlock,
  appendToolApprovalBlock,
  appendToolPermissionRequestBlock,
  appendToolResultBlock,
  appendToolUseBlock,
  markAskUserQuestionAnsweredBlocks,
  markExecApprovalResolvedBlocks,
  markToolApprovalResolvedBlocks,
  markToolPermissionResolvedBlocks,
  mergeSubagentMetaBlocks,
  upsertExecApprovalBlock,
  upsertPlanBlock,
  findLastBlockIndex,
} from "./chat-block-reducers";
import { useSessionStatusStore, type DoneEvent } from "./session-status-store";
import { useQueuedMessagesStore } from "./queued-messages-store";

import {
  noteVisibleToken,
  noteFirstToken,
  suspendClock,
  resumeClock,
  flushLiveSegment,
  hasSessionStream,
  updateStream,
  dropStream,
} from "./chat-streams/live";
import { type State, Actions } from "./chat-streams/types";

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
        // turn 开始即开表（工具空档由 tool 事件停/开）。
        burstStartedAt: Date.now(),
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

  noteOutputActivity: (sessionId, assistantMessageId) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) =>
        cur.firstTokenAt == null ? noteFirstToken(cur, Date.now()) : null,
      ),
    ),

  appendLiveToolUse: (sessionId, assistantMessageId, block) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) => {
        // 内层（subagent 内部）工具不碰表 —— 派遣它的那个外层 Task 调用已经把表
        // 按住了，内层再加减一遍只会在孤儿帧上留下按死表的挂账。
        //
        // 停表前先兜底记一次首 token：工具调用摆在这里，模型显然早就在产出 token
        // 了。claudecode 有更早更准的 output_activity 事件，先到先得；没有等价帧的
        // 后端（codex / piagent）靠这一条（sess-3241）。
        const now = Date.now();
        const segment = flushLiveSegment(cur);
        const flushed =
          block.parentToolUseId || !block.toolUseId
            ? segment
            : suspendClock(noteFirstToken(segment, now), block.toolUseId, now);
        return {
          ...flushed,
          liveBlocks: appendToolUseBlock(flushed.liveBlocks, block),
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
          liveBlocks: appendToolResultBlock(resumed.liveBlocks, block),
        };
      }),
    ),

  appendLiveCompactBoundary: (sessionId, assistantMessageId, compact) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) => {
        const flushed = flushLiveSegment(cur);
        return {
          ...flushed,
          liveBlocks: appendCompactBoundaryBlock(flushed.liveBlocks, compact),
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
        return {
          ...flushed,
          liveBlocks: upsertPlanBlock(flushed.liveBlocks, planText, canonical),
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
        const liveBlocks = markAskUserQuestionAnsweredBlocks(
          cur.liveBlocks,
          payload,
          canonical,
        );
        return liveBlocks ? { ...cur, liveBlocks } : null;
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
          liveBlocks: appendToolPermissionRequestBlock(
            flushed.liveBlocks,
            payload,
            canonical,
          ),
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
        const liveBlocks = markToolPermissionResolvedBlocks(
          cur.liveBlocks,
          payload,
          canonical,
        );
        return liveBlocks ? { ...cur, liveBlocks } : null;
      }),
    ),

  appendLiveToolApproval: (sessionId, assistantMessageId, payload) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) => {
        if (!payload || !payload.requestId) return null;
        const flushed = flushLiveSegment(cur);
        return {
          ...flushed,
          liveBlocks: appendToolApprovalBlock(flushed.liveBlocks, payload),
        };
      }),
    ),

  markToolApprovalResolved: (sessionId, assistantMessageId, payload) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) => {
        const liveBlocks = markToolApprovalResolvedBlocks(
          cur.liveBlocks,
          payload,
        );
        return liveBlocks ? { ...cur, liveBlocks } : null;
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
        // 只有真的要追加新卡片(找不到既有条目)才 flush:更新既有卡片状态不该把
        // 尚未流完的正文提前冻结成 block。
        const base = existingIdx >= 0 ? cur : flushLiveSegment(cur);
        const liveBlocks = upsertExecApprovalBlock(base.liveBlocks, payload);
        return liveBlocks ? { ...base, liveBlocks } : null;
      }),
    ),

  markExecApprovalResolved: (sessionId, assistantMessageId, payload) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) => {
        const liveBlocks = markExecApprovalResolvedBlocks(
          cur.liveBlocks,
          payload,
        );
        return liveBlocks ? { ...cur, liveBlocks } : null;
      }),
    ),

  mergeSubagentMeta: (sessionId, assistantMessageId, toolUseId, meta) =>
    set((state) =>
      updateStream(state, sessionId, assistantMessageId, (cur) => {
        const liveBlocks = mergeSubagentMetaBlocks(
          cur.liveBlocks,
          toolUseId,
          meta,
        );
        return liveBlocks ? { ...cur, liveBlocks } : null;
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
          // 新 assistant 段开始 → 计时重新开走。
          burstStartedAt: Date.now(),
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

// ── 原先住在本文件里的那些名字,在这里再导出一次 ──────────────────────────────
//
// 25 个消费者一直从 "@/stores/chat-streams-store" 取这些类型与取值函数。拆文件换的是
// 文件布局,不是 import 路径 —— 让 25 处调用方跟着改,是把搬运的成本转嫁给读者。
//
// 只再导出**本来就导出**的那些:`State` / `Actions` / `LiveToolUseInput` 从前是模块私有的,
// 现在仍只被本文件与 chat-streams/ 内部用。
export type {
  ChatBlockData,
  ChatBlockSubagentData,
  ExecApprovalData,
  LiveStream,
  RetryNotice,
  ToolApprovalData,
} from "./chat-streams/types";
export {
  hasSessionStream,
  primaryStream,
  sessionStreamMap,
  streamForMessage,
} from "./chat-streams/live";
