// frontend/src/stores/peer-session-store.ts
//
// Peer Tab 的会话状态（R19）：一条 (fingerprint, sessionId) 对应一枚 Peer Tab。本 store
// 负责 attach → pull 补齐 → 实时事件归约 的顺序处理（seq 去重：pull 从 0 拉到高水位，
// attach 期间 ≤ 高水位的实时帧由 pull 覆盖、直接丢弃，> 高水位的立即归约；ready 之后
// 由 reducePeerEvent 的游标去重兜底）。发送 / 回答 / 工具权限 / 关闭走 peer 绑定；
// 关闭只 detach（不删除对端会话）。
//
// 事件通道是单条 Wails 广播（peer.event），按 fingerprint+sessionId 路由到各 Peer Tab。

import { create } from "zustand";

import {
  PeerAttach,
  PeerPull,
  PeerSteer,
  PeerSubmitAnswer,
  PeerSubmitToolPermission,
  PeerDetach,
} from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import type { wire } from "../../wailsjs/go/models";
import {
  createPeerTranscript,
  reducePeerEvent,
  reducePeerPullPage,
  type PeerEventFrame,
  type PeerTranscriptState,
} from "../components/agentre/peer/peer-transcript";

export const PEER_EVENT_CHANNEL = "peer.event";

export type PeerDecisionView =
  | {
      kind: "ask";
      requestId: string;
      questions: PeerAskQuestionView[];
      answered?: boolean;
      skipped?: boolean;
    }
  | {
      kind: "permission";
      requestId: string;
      toolName: string;
      toolCallId: string;
      input?: Record<string, unknown>;
      resolved?: boolean;
      allowed?: boolean;
    };

export type PeerAskQuestionView = {
  id?: string;
  question: string;
  header: string;
  multiSelect?: boolean;
  isOther?: boolean;
  isSecret?: boolean;
  options: { label: string; description: string; preview?: string }[];
};

export type PeerSessionView = {
  key: string;
  fingerprint: string;
  sessionId: number;
  title: string;
  deviceName: string;
  status: "attaching" | "ready" | "error";
  error?: string;
  /** attach 返回的高水位；attaching 期间 ≤ 高水位的实时帧由 pull 覆盖、直接丢弃。 */
  highWater: number;
  transcript: PeerTranscriptState;
  sending: boolean;
  lastError?: string;
};

type State = {
  sessions: Record<string, PeerSessionView>;
  attach: (args: {
    fingerprint: string;
    sessionId: number;
    title: string;
    deviceName: string;
  }) => Promise<void>;
  detach: (fingerprint: string, sessionId: number) => void;
  steer: (
    fingerprint: string,
    sessionId: number,
    text: string,
  ) => Promise<boolean>;
  submitAnswer: (args: {
    fingerprint: string;
    sessionId: number;
    requestId: string;
    answers: Array<{
      questionIndex: number;
      labels: string[];
      otherText?: string;
    }>;
    skipped?: boolean;
  }) => Promise<{ alreadyHandled: boolean } | { error: string }>;
  submitToolPermission: (args: {
    fingerprint: string;
    sessionId: number;
    requestId: string;
    allow: boolean;
    alwaysAllowSession?: boolean;
  }) => Promise<{ alreadyHandled: boolean } | { error: string }>;
};

export const peerKeyOf = (fingerprint: string, sessionId: number) =>
  `${fingerprint}:${sessionId}`;

export const usePeerSessionsStore = create<State>((set, get) => ({
  sessions: {},

  attach: async ({ fingerprint, sessionId, title, deviceName }) => {
    ensureSubscribed();
    const key = peerKeyOf(fingerprint, sessionId);
    if (get().sessions[key]) return;

    set((state) => ({
      sessions: {
        ...state.sessions,
        [key]: {
          key,
          fingerprint,
          sessionId,
          title,
          deviceName,
          status: "attaching",
          highWater: 0,
          transcript: createPeerTranscript(),
          sending: false,
        },
      },
    }));

    let highWater = 0;
    try {
      const att = await PeerAttach({ fingerprint, sessionId } as Parameters<
        typeof PeerAttach
      >[0]);
      highWater = att?.latestSeq ?? 0;
      set((state) => ({
        sessions: {
          ...state.sessions,
          [key]: { ...state.sessions[key], highWater },
        },
      }));
    } catch (e) {
      set((state) => ({
        sessions: {
          ...state.sessions,
          [key]: {
            ...state.sessions[key],
            status: "error",
            error: errorMessage(e),
          },
        },
      }));
      return;
    }

    // pull 补齐：从 0 拉到高水位（对端桌面端历史不回收，OldestSeq 恒为第一条）。
    let pullCursor = 0;
    for (;;) {
      let page: wire.SessionPullResult;
      try {
        page = await PeerPull({
          fingerprint,
          sessionId,
          cursor: pullCursor,
        } as Parameters<typeof PeerPull>[0]);
      } catch (e) {
        set((state) => ({
          sessions: {
            ...state.sessions,
            [key]: {
              ...state.sessions[key],
              status: "error",
              error: errorMessage(e),
            },
          },
        }));
        return;
      }
      const current = get().sessions[key];
      if (!current) return; // 拉取期间被关闭
      set((state) => {
        const session = state.sessions[key];
        if (!session) return state; // detach 竞态：连接中已被关闭
        return {
          sessions: {
            ...state.sessions,
            [key]: {
              ...session,
              transcript: reducePeerPullPage(
                session.transcript,
                (page?.notifications ?? []).map((n) => ({
                  seq: n.seq,
                  params: n.params as unknown as {
                    sessionId: number;
                    event: unknown;
                  },
                })),
              ),
            },
          },
        };
      });
      pullCursor = page?.cursor ?? pullCursor;
      if (!page?.hasMore || pullCursor >= highWater) break;
    }

    // attach 期间 ≤ 高水位的实时帧已由 pull 覆盖；这里把游标抬到高水位，
    // ready 之后 > 高水位的实时帧正常归约、≤ 高水位的重复帧被去重丢弃。
    const current = get().sessions[key];
    if (!current) return;
    set((state) => {
      const session = state.sessions[key];
      if (!session) return state;
      return {
        sessions: {
          ...state.sessions,
          [key]: {
            ...session,
            status: "ready",
            transcript: {
              ...session.transcript,
              cursor: Math.max(session.transcript.cursor, highWater),
            },
          },
        },
      };
    });
  },

  detach: (fingerprint, sessionId) => {
    const key = peerKeyOf(fingerprint, sessionId);
    if (!get().sessions[key]) return;
    set((state) => {
      const sessions = { ...state.sessions };
      delete sessions[key];
      return { sessions };
    });
    // 只结束本端接入，不删除对端会话（R19）。
    void Promise.resolve(PeerDetach(fingerprint, sessionId)).catch(() => {});
  },

  steer: async (fingerprint, sessionId, text) => {
    const key = peerKeyOf(fingerprint, sessionId);
    const session = get().sessions[key];
    if (!session) return false;
    set((state) => ({
      sessions: {
        ...state.sessions,
        [key]: { ...state.sessions[key], sending: true },
      },
    }));
    try {
      await PeerSteer({ fingerprint, sessionId, text } as Parameters<
        typeof PeerSteer
      >[0]);
      return true;
    } catch (e) {
      set((state) => ({
        sessions: {
          ...state.sessions,
          [key]: {
            ...state.sessions[key],
            sending: false,
            lastError: errorMessage(e),
          },
        },
      }));
      return false;
    } finally {
      set((state) => ({
        sessions: {
          ...state.sessions,
          [key]: { ...state.sessions[key], sending: false },
        },
      }));
    }
  },

  submitAnswer: async ({
    fingerprint,
    sessionId,
    requestId,
    answers,
    skipped,
  }) => {
    try {
      const res = await PeerSubmitAnswer({
        fingerprint,
        sessionId,
        requestId,
        answers,
        skipped,
      } as Parameters<typeof PeerSubmitAnswer>[0]);
      return { alreadyHandled: !!res?.alreadyHandled };
    } catch (e) {
      return { error: errorMessage(e) };
    }
  },

  submitToolPermission: async ({
    fingerprint,
    sessionId,
    requestId,
    allow,
    alwaysAllowSession,
  }) => {
    try {
      const res = await PeerSubmitToolPermission({
        fingerprint,
        sessionId,
        requestId,
        allow,
        alwaysAllowSession,
      } as Parameters<typeof PeerSubmitToolPermission>[0]);
      return { alreadyHandled: !!res?.alreadyHandled };
    } catch (e) {
      return { error: errorMessage(e) };
    }
  },
}));

function errorMessage(e: unknown): string {
  if (e instanceof Error) return e.message;
  const err = e as { message?: string } | null;
  return err?.message ?? String(e);
}

// 单条 Wails 广播订阅：peer.event → 按 fingerprint+sessionId 路由到各 Peer Tab。
// attach 期间 ≤ 高水位的实时帧由 pull 覆盖、丢弃；ready 之后由游标去重兜底。
let subscribed = false;
function ensureSubscribed() {
  if (subscribed) return;
  subscribed = true;
  // 后端按 ~16ms 的窗口把帧攒成一批再广播(见 internal/app/peer_event_batch.go):
  // 一批一次 Wails 广播、一次 setState,而不是一个 token 一次。
  // 帧本身**不合并** —— 每帧带自己的 seq,下面按帧去重的口径因此和逐帧送达时一样。
  // 一批里可以混着不同对端 / 不同会话的帧,所以要按 key 分别落。
  EventsOn(PEER_EVENT_CHANNEL, (payload: unknown) => {
    if (!Array.isArray(payload) || payload.length === 0) return;
    const frames = payload as PeerEventFrame[];
    usePeerSessionsStore.setState((state) => {
      let sessions = state.sessions;
      let changed = false;
      for (const frame of frames) {
        if (!frame || typeof frame !== "object") continue;
        const key = peerKeyOf(frame.fingerprint, frame.sessionId);
        // 从**已经应用过本批前面几帧**的那份里取,同一条会话的连续帧才能顺序累积。
        const session = sessions[key];
        if (!session) continue;
        if (
          session.status === "attaching" &&
          (frame.seq ?? 0) <= session.highWater
        ) {
          continue;
        }
        if (!changed) {
          sessions = { ...sessions };
          changed = true;
        }
        sessions[key] = {
          ...session,
          transcript: reducePeerEvent(session.transcript, frame),
        };
      }
      // 整批都被去重丢掉时保持同一个引用,不制造一次无意义的更新。
      return changed ? { sessions } : state;
    });
  });
}

// 供测试注入：重置订阅（vitest 模块隔离下自动）。
export const __resetPeerSubscriptionForTesting = () => {
  subscribed = false;
};
