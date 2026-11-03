// use-agent-identity 把「一个 Agent 的完整身份」从 chat-agents-store 取出来：
// 名字 / 颜色 token / 图标 key / 上传图片。会话 meta 里没有后两项
// （stores/types.ts 的 SessionMetaSnapshot 只有 agentId / agentName / agentColor），
// 所以凡是只读 meta 的位置（会话索引行、对话 tab、通知）都从这里补齐。
//
// 规格 2026-11-02「预置头像」决策 17：这些位置此前只画首字母 + 颜色，现在统一按
// 上传图片 ▸ 预设 ▸ 图标 ▸ 首字母显示完整身份。Agent 不在 store 里时返回 null，
// 由调用方回落 meta 的名字与颜色。
import * as React from "react";

import { useChatAgentsStore, type AgentSlim } from "@/stores/chat-agents-store";

export type AgentIdentity = {
  name: string;
  /** 颜色 token（`agent-1`…`agent-16`），不是 css 值。 */
  color: string;
  avatarIcon: string;
  avatarDataUrl: string;
};

/**
 * AgentSlim → 完整身份。名字缺失时留空（调用方回落 meta），颜色原样给 token，
 * 由各处的记号自己决定怎么上色。
 */
export function agentIdentityOf(
  agent: AgentSlim | null | undefined,
): AgentIdentity | null {
  if (!agent) return null;
  return {
    name: agent.name ?? "",
    color: agent.avatarColor ?? "",
    avatarIcon: agent.avatarIcon ?? "",
    avatarDataUrl: agent.avatarDataUrl ?? "",
  };
}

export function useAgentIdentity(
  agentId: number | null | undefined,
): AgentIdentity | null {
  const agents = useChatAgentsStore((s) => s.agents);

  return React.useMemo(() => {
    if (!agentId || agentId <= 0) return null;
    return agentIdentityOf(agents.find((a) => a.id === agentId));
  }, [agents, agentId]);
}
