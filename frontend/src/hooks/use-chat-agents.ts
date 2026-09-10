import { useEffect } from "react";

import { useChatAgentsStore, type AgentSlim } from "@/stores/chat-agents-store";

export type { AgentSlim };

// useChatAgents 是 chat-agents-store 的薄包装: 订阅 store 字段 + 首次 mount
// 触发 reload。所有调用方共享同一份 agents 数据 (sidebar / 命令面板 / App 顶层),
// reload 在 store 内做并发去重, 多个组件同时 mount 也只跑一次 ListChatAgents。
//
// 注意: ChatPanelHost 之类不调本 hook 的地方触发刷新, 直接调
// useChatAgentsStore.getState().reload() —— 这是修复「新建会话不进左栏」的
// 关键路径 (chat-panel-host.tsx)。
export function useChatAgents() {
  const agents = useChatAgentsStore((s) => s.agents);
  const loading = useChatAgentsStore((s) => s.loading);
  const error = useChatAgentsStore((s) => s.error);
  const reload = useChatAgentsStore((s) => s.reload);

  useEffect(() => {
    void reload();
  }, [reload]);

  return { agents, loading, error, reload };
}
