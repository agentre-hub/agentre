import { create } from "zustand";

// SessionReadState 是「点开未读 → 侧栏立即消未读」的乐观状态层。
//
// 来由：后端 MarkChatSessionRead 写库后会更新 last_read_at；但 sidebar 缓存
// 的 ListAgents 响应里 lastReadAt 仍是旧值，下次重拉 ListAgents 前看上去
// 还是未读。这里维护一个客户端覆盖，让 sidebar 在重拉之前先用 max(server, override)
// 计算 attention rank / displayStatus，实现「点了就消」。
//
// 单调推进（同后端 MarkRead 语义）。条目永不清理 —— sessionId+8B 量级可控；
// 后续 ListAgents 拿到的服务端 lastReadAt ≥ override 时 max 自然就是服务端值，
// 不会出现"过期 override 阻挡服务端新值"。
type SessionReadState = {
  overrides: Map<number, number>;
  markRead: (sessionId: number, ts: number) => void;
};

export const useSessionReadStore = create<SessionReadState>((set) => ({
  overrides: new Map(),
  markRead: (sessionId, ts) =>
    set((state) => {
      if (sessionId <= 0 || ts <= 0) return state;
      const cur = state.overrides.get(sessionId) ?? 0;
      if (ts <= cur) return state;
      const next = new Map(state.overrides);
      next.set(sessionId, ts);
      return { overrides: next };
    }),
}));
