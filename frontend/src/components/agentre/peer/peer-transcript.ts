// frontend/src/components/agentre/peer/peer-transcript.ts
//
// Peer Tab 的**帧来源**（R19 / R8）：对端桌面端经 peer_svc 推来的 canonical 事件帧
// （wire EventFrame 同形，带 fingerprint / sessionId / seq）在这里去重、累积，归约本身
// 交给共享包的 `reduceFrames`。
//
// 归约为什么不在这里：同一套 28 种 wire 事件的归约此前两个宿主各写了一份，更完整的
// 那份在 agentre-server（归约到共享 TranscriptMessage/TranscriptBlock，含 canonical
// 工具卡 / plan / exec-approval / compact 边界）。本文件从前只认五种块，其余一律落自造
// 的 `raw` —— 而共享包的行模型根本没有 `raw` 这一档，那些块最终渲染成一行
// `(debug) unimplemented block type: raw`，载荷连看都看不到。实现已搬进包
// （packages/agentre-ui/src/transcript/frames.ts），两侧此后只保留各自的帧来源：
// 本文件是 peer_svc 的 Wails 事件，agentre-server 那侧是它的 relay socket。
//
// 本文件因此只剩三件宿主自己的事：
//   1. seq 去重（pull 补齐与实时推送会重复投递同一帧）与帧序列的累积；
//   2. 待决策清单 —— ask_user_question / tool_permission_request 归约出的那两种块
//      **不进 Peer Tab 的转录**：包里那两张卡按下去会调 TranscriptPorts，而桌面端
//      在 App 顶层注入的是**本机**会话的 Wails 绑定，拿远端的 sessionId 去答本地
//      会话是答错人。Peer Tab 自绘可操作卡片走 peer 绑定，见 peer-panel.tsx。
//   3. waitingForInput —— 「对端此刻在等我」这枚灯是 Peer Tab 自己的 UI 状态
//      （决定转录显不显示流式指示），不是转录内容。

import {
  createTranscriptProjector,
  type TranscriptFrame,
  type TranscriptMessage,
  type TranscriptProjector,
} from "@agentre-hub/agentre-ui";
import {
  EventAskUserQuestion,
  EventAskUserQuestionAnswered,
  EventDone,
  EventError,
  EventToolPermissionRequest,
  EventToolPermissionResolved,
  EventUserMessage,
  type EventKind,
} from "@agentre-hub/agentre-wire";

export type PeerEventFrame = {
  fingerprint: string;
  sessionId: number;
  seq?: number;
  event: { kind: EventKind } & Record<string, unknown>;
};

/** Peer Tab 的消息就是共享包的 DTO —— 转录渲染器吃的正是这一份。 */
export type PeerChatMessage = TranscriptMessage;

export type PeerAskQuestion = {
  id?: string;
  question: string;
  header: string;
  multiSelect?: boolean;
  isOther?: boolean;
  isSecret?: boolean;
  options: { label: string; description: string; preview?: string }[];
};

export type PeerDecision =
  | {
      kind: "ask";
      requestId: string;
      questions: PeerAskQuestion[];
      answered?: boolean;
      skipped?: boolean;
    }
  | {
      kind: "permission";
      requestId: string;
      toolName: string;
      input?: Record<string, unknown>;
      resolved?: boolean;
      allowed?: boolean;
    };

export type PeerTranscriptState = {
  /** 已经摘掉待决策卡的转录，直接喂 ChatTranscript。 */
  messages: PeerChatMessage[];
  decisions: PeerDecision[];
  /** 已归约到的最高 seq；≤ 它的帧是重复投递，丢弃。 */
  cursor: number;
  waitingForInput: boolean;
  /** 去重后的帧序列 —— 归约的唯一输入。 */
  frames: readonly TranscriptFrame[];
  /**
   * 增量投影器：只归约新到的那几帧，且只给被改到的那条消息换新身份。整段重算
   * （reduceFrames）每帧都会换掉全部消息对象，而下游 TranscriptRowView 的行缓存
   * 以消息对象为 WeakMap 键 —— 那等于每帧全表 miss。
   *
   * 首帧到达前拿不到 sessionId（store 建状态时还没 attach 完），因此惰性构造。
   */
  projector: TranscriptProjector | null;
};

export const createPeerTranscript = (): PeerTranscriptState => ({
  messages: [],
  decisions: [],
  cursor: 0,
  waitingForInput: false,
  frames: [],
  projector: null,
});

export function reducePeerEvent(
  state: PeerTranscriptState,
  frame: PeerEventFrame,
): PeerTranscriptState {
  // 按 seq 去重：pull 补齐与实时推送可能重复投递同一帧（attach 到拉平之间落库的
  // 帧会被两路都带出来），已归约过的帧（seq ≤ 游标）直接丢弃。
  if (frame.seq != null && frame.seq > 0 && frame.seq <= state.cursor) {
    return state;
  }
  return advance(state, [frame]);
}

// reducePeerPullPage 把一页 journaled 历史喂给同一归约器。每条 notification 的
// Params 是「不含 seq」的帧原样，须把日志行自己的 seq 盖上去（与浏览器同一约定）。
export function reducePeerPullPage(
  state: PeerTranscriptState,
  notifications: Array<{
    seq: number;
    params: { sessionId: number; event: unknown };
  }>,
): PeerTranscriptState {
  const fresh: PeerEventFrame[] = [];
  for (const n of notifications ?? []) {
    const raw = n.params as unknown as {
      sessionId?: number;
      event?: { kind: string };
    };
    if (n.seq > 0 && n.seq <= state.cursor) continue;
    // 这里只能**断言**成 EventKind 而不是校验:日志行来自对端,运行期照样可能
    // 送来一个词表外的字符串(比本仓新的桌面端、坏行)。兜住它的是共享归约器
    // switch 的 default —— 那一档如实落 notice,不吞掉也不抛。
    fresh.push({
      fingerprint: "",
      sessionId: raw?.sessionId ?? 0,
      seq: n.seq,
      event: (raw?.event ?? { kind: "" }) as PeerEventFrame["event"],
    });
  }
  if (fresh.length === 0) return state;
  return advance(state, fresh);
}

function advance(
  state: PeerTranscriptState,
  incoming: PeerEventFrame[],
): PeerTranscriptState {
  const frames = [...state.frames, ...incoming];
  const sessionId = incoming[0]?.sessionId ?? 0;
  const projector = state.projector ?? createTranscriptProjector(sessionId);
  const reduced = projector.project(frames);

  let cursor = state.cursor;
  let waitingForInput = state.waitingForInput;
  for (const frame of incoming) {
    cursor = Math.max(cursor, frame.seq ?? 0);
    waitingForInput = nextWaiting(waitingForInput, frame.event?.kind);
  }

  return {
    messages: visibleMessages(reduced),
    decisions: collectDecisions(reduced),
    cursor,
    waitingForInput,
    frames,
    projector,
  };
}

/**
 * 「对端在等我」这枚灯。ask / 授权请求点亮，答复、结束与新的用户消息熄灭；其余
 * kind 一概不动 —— 遥测帧夹在中间不该把灯打灭。
 */
function nextWaiting(current: boolean, kind: string | undefined): boolean {
  switch (kind) {
    case EventAskUserQuestion:
    case EventToolPermissionRequest:
      return true;
    case EventAskUserQuestionAnswered:
    case EventToolPermissionResolved:
    case EventUserMessage:
    case EventDone:
    case EventError:
      return false;
    default:
      return current;
  }
}

const HOSTED_BY_PANEL = new Set([
  "ask_user_question",
  "tool_permission_request",
]);

/**
 * 摘掉由 Peer Panel 自绘的那两种交互卡后的转录。
 *
 * 缓存以**源消息对象**为键：投影器对没变过的消息交还同一个引用，摘完之后必须还是
 * 同一个引用，否则下游的 WeakMap 行缓存每帧全表 miss —— 投影器省下的那次重建就白做了。
 */
const visibleCache = new WeakMap<TranscriptMessage, TranscriptMessage>();

function visibleMessages(messages: TranscriptMessage[]): PeerChatMessage[] {
  const out: PeerChatMessage[] = [];
  for (const msg of messages) {
    const cached = visibleCache.get(msg);
    if (cached !== undefined) {
      if (cached.blocks.length > 0 || cached.errorText) out.push(cached);
      continue;
    }
    const blocks = msg.blocks.filter((b) => !HOSTED_BY_PANEL.has(b.type));
    const next = blocks.length === msg.blocks.length ? msg : { ...msg, blocks };
    visibleCache.set(msg, next);
    // 摘空之后只剩一条没有正文的助手消息(比如整轮只有一张提问卡)——那会渲染成
    // 一个空气泡。归约器本身也会为纯消息级帧(usage)开一条空消息,同理不显示。
    if (next.blocks.length > 0 || next.errorText) out.push(next);
  }
  return out;
}

/**
 * 待决策清单：从归约结果里把两种交互卡摘出来，按出现顺序排。
 *
 * 判据取块本身而不是另记一份事件账 —— 决议帧回填的是**那张卡**，卡上的
 * answered / resolved 就是权威，两处各记一遍必然漂移。
 */
function collectDecisions(messages: TranscriptMessage[]): PeerDecision[] {
  const decisions: PeerDecision[] = [];
  for (const msg of messages) {
    for (const block of msg.blocks) {
      const ask = block.askUserQuestion;
      if (ask) {
        decisions.push({
          kind: "ask",
          requestId: ask.requestId,
          questions: (Array.isArray(ask.questions)
            ? ask.questions
            : []) as PeerAskQuestion[],
          answered: ask.answered,
          skipped: ask.skipped,
        });
        continue;
      }
      const perm = block.toolPermission;
      if (perm) {
        decisions.push({
          kind: "permission",
          requestId: perm.requestId,
          toolName: perm.toolName,
          input: perm.toolInput,
          resolved: perm.resolved,
          allowed: perm.allowed,
        });
      }
    }
  }
  return decisions;
}
