// frontend/src/stores/chat-agents-store.ts
//
// chat-agents-store 是「左侧 sidebar 的 agents 列表」唯一数据源。所有消费方
// (ChatPage / 命令面板 / App / 顶层 indicator) 共享同一份 state, 任何一处触发
// reload 都让其他订阅者立刻看到最新数据。
//
// 这个 store 取代了原先 hook 内的 useState —— hook 之间各自维护一份会造成
// 「sidebar 拉过、命令面板没拉」的不一致, 且 ChatPanelHost 这种「不直接调
// hook 的组件」根本没办法触发刷新 (这是新建会话不进左栏的根因)。改成 store
// 之后 ChatPanelHost 直接 useChatAgentsStore.getState().reload() 即可。
//
// reload 并发去重: 同一时刻只跑一个 ListChatAgents in-flight; 后续 reload 调用
// 复用同一个 promise, 避免 sidebar 拉一遍、命令面板紧接着又拉一遍。

import { create } from "zustand";

import { ListChatAgents } from "../../wailsjs/go/app/App";
import type { chat_svc } from "../../wailsjs/go/models";

import { hasSessionStream, useChatStreamsStore } from "./chat-streams-store";
import {
  normalizeSessionSnapshot,
  useSessionStatusStore,
  type SessionStatusPatch,
} from "./session-status-store";
import { useSessionMetaStore, type SessionMeta } from "./session-meta-store";
import type { AgentStatus } from "./types";

// AgentSlim: chat-agents-store 对外暴露的 agent 行形态 —— 在 Wails 原生 ChatAgentItem
// 基础上附加 sessionIds（去重的全量 id 列表）。
//
// 实现细节: reload 时直接 Object.assign 把 sessionIds 挂到 Wails class 实例上(原地修改),
// 不用 spread。这样既保留 class 方法 (convertValues 等), 也让既有 ChatAgentItem[] 消费方
// 不用改类型 —— AgentSlim is-a ChatAgentItem。
//
// 后端 ChatAgentItem 已经带上, 但 wailsjs codegen 只有 make dev / wails build 时才刷新,
// 这里手工 mirror 让 TS 在 codegen 未运行的 worktree 也能编译通过 (运行时仍走 Wails
// 序列化的真实 JSON, 不影响数据流)。
export type AgentSlim = chat_svc.ChatAgentItem & {
  sessionIds: number[];
  hasBackendTarget?: boolean;
  deviceID?: string;
  deviceName?: string;
  online?: boolean;
};

// 兼容: 历史代码用 "ChatAgentItem" 这个名字指代「chat-agents-store 提供的 agent 行」。
// 别名指向 AgentSlim, 让 useChatAgents() 的消费方拿到 sessionIds (App.tsx reconcile 用),
// 同时由于 AgentSlim is-a chat_svc.ChatAgentItem, 旧的字段访问代码也都通过。
export type ChatAgentItem = AgentSlim;

type State = {
  agents: AgentSlim[];
  loading: boolean;
  error: string | null;
};

type Actions = {
  reload: () => Promise<void>;
  // 测试隔离用, 生产代码不该调。
  __reset: () => void;
};

// in-flight reload promise: 并发调用 reload() 时复用, 避免重复 RPC。
let inflight: Promise<void> | null = null;

function listKnownSessions(a: chat_svc.ChatAgentItem) {
  const out: chat_svc.ChatSessionLite[] = [];
  const seen = new Set<number>();
  for (const s of [...(a.sessions ?? []), ...(a.attentionSessions ?? [])]) {
    if (seen.has(s.id)) continue;
    seen.add(s.id);
    out.push(s);
  }
  return out;
}

function listSessionIds(a: AgentSlim | chat_svc.ChatAgentItem) {
  const provided = (a as { sessionIds?: unknown }).sessionIds;
  if (Array.isArray(provided)) {
    return provided.filter(
      (id): id is number => typeof id === "number" && id > 0,
    );
  }
  const ids = new Set<number>();
  for (const s of listKnownSessions(a)) ids.add(s.id);
  return Array.from(ids);
}

// 初始 loading=true: 反映「还没拉过, 别把空 agents 当成最终态」, 与原 hook 行为对齐
// (原 useState(true))。这样命令面板在 useChatAgents 首次 mount 的那一帧不会闪「无结果」。
export const useChatAgentsStore = create<State & Actions>((set) => ({
  agents: [],
  loading: true,
  error: null,
  reload: () => {
    if (inflight) return inflight;
    set({ loading: true, error: null });
    inflight = (async () => {
      try {
        const resp = await ListChatAgents();
        const agents = resp.agents ?? [];
        // 把快照里所有 session 的 status 灌进 session-status-store, 让 tab /
        // sidebar / toolbar 通过同一个 store 读到「running / waiting / idle」。
        // bulkUpsert 内部逐条同值短路, 一刷只在真有差异时才换 Map 引用。
        const entries: [number, SessionStatusPatch][] = [];
        // 诊断: ListChatAgents 是远 DB 异步快照, 与 stream 内乐观写 / session_status
        // 推帧之间存在 race。命中以下两类时打 warn, 是排查状态时序的关键线索:
        //   (a) sid 有活跃 LiveStream 但快照说 status="error" / "idle" —— 说明
        //       响应是 Send 把 DB 翻 "running" 之前抓的旧快照；统一归一化入口会
        //       忽略它对实时运行态的覆盖。
        //   (b) sid 没有活跃 stream 但快照与 store 现值不一致, 仅 dev 调试观察用。
        const streamsState = useChatStreamsStore.getState();
        const statusesState = useSessionStatusStore.getState();
        for (const a of agents) {
          for (const s of listKnownSessions(a)) {
            const snapshotStatus = (s.status as AgentStatus) || "idle";
            const hasActiveStream = hasSessionStream(streamsState, s.id);
            if (hasActiveStream) {
              const prev = statusesState.statuses.get(s.id);
              if (
                prev &&
                prev.agentStatus !== snapshotStatus &&
                snapshotStatus !== "running" &&
                snapshotStatus !== "waiting"
              ) {
                console.warn(
                  "[chat-agents-store] ignored stale agentStatus snapshot while LiveStream is active",
                  {
                    sessionId: s.id,
                    prevAgentStatus: prev.agentStatus,
                    snapshotAgentStatus: snapshotStatus,
                  },
                );
              }
            }
            entries.push([
              s.id,
              normalizeSessionSnapshot(
                s.id,
                {
                  agentStatus: snapshotStatus,
                  needsAttention: s.needsAttention ?? false,
                  bgRunning: s.bgRunning ?? false,
                },
                hasActiveStream,
              ),
            ]);
          }
        }
        useSessionStatusStore.getState().bulkUpsert(entries);

        // 把 sessions 的静态字段分发到 session-meta-store。
        //
        // projectId 现在由 ChatSessionLite 直接带上（0 = 未挂项目）。此前它只有
        // ChatSessionDetail 才有 —— 也就是「这条会话被打开过」之后才知道自己属于哪个
        // 项目，侧栏因此永远拿不到；单一会话索引按项目分组、以及按 Agent 分组时行首
        // 那个项目色字形（决策 4）都要它。
        //
        // 载荷**没提到** projectId 时（旧快照 / 半截数据）不写这个键，而不是补一个 0：
        // 0 是「随手对话」这个有意义的取值，凭空写进去会把一条挂着项目的会话搬到别的组。
        const metaEntries: [number, Partial<SessionMeta>][] = [];
        for (const a of agents) {
          for (const s of listKnownSessions(a)) {
            metaEntries.push([
              s.id,
              {
                agentId: a.id,
                agentName: a.name,
                agentColor: a.avatarColor || "agent-1",
                ...(s.projectId === undefined
                  ? {}
                  : { projectId: s.projectId }),
                title: s.title || "",
                lastMessageAt: s.lastMessageAt ?? 0,
                lastReadAt: s.lastReadAt ?? 0,
              },
            ]);
          }
        }
        useSessionMetaStore.getState().bulkUpsert(metaEntries);

        // 构造 AgentSlim: 原地 Object.assign 给 Wails class 实例挂 sessionIds 字段,
        // 不用 spread —— spread 会丢失 class 方法 (convertValues), 触发 TS 错误。
        const slimAgents = agents.map((a) => {
          return Object.assign(a, {
            sessionIds: listSessionIds(a),
          }) as AgentSlim;
        });

        set({ agents: slimAgents, loading: false, error: null });
      } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        set({ loading: false, error: msg });
      } finally {
        inflight = null;
      }
    })();
    return inflight;
  },
  __reset: () => {
    inflight = null;
    set({ agents: [], loading: true, error: null });
  },
}));
