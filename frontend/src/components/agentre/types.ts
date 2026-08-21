import type { AgentStatus } from "@/stores/types";
export type { AgentStatus };

/**
 * 状态配色是纯展示投影，已随对话流卡片一起搬进 `@agentre-ai/agentre-ui`。
 * 这里保留转发：仓库内有 9 个引用点，把它们全部改指包只会淹没真正的改动。
 *
 * 颜色 token 词汇表（`AgentColor` / `agentColorOrder`）同理：定义随 tokens.css
 * 搬进包（转录里的 @提及 chip 要按 token 上色），这里同样只留转发。
 */
export { statusConfig } from "@agentre-ai/agentre-ui";
export { agentColorOrder } from "@agentre-ai/agentre-ui";
export type { AgentColor } from "@agentre-ai/agentre-ui";

import type { AgentColor } from "@agentre-ai/agentre-ui";

export const agentColorClassNames: Record<AgentColor, string> = {
  "agent-1": "bg-agent-1",
  "agent-2": "bg-agent-2",
  "agent-3": "bg-agent-3",
  "agent-4": "bg-agent-4",
  "agent-5": "bg-agent-5",
  "agent-6": "bg-agent-6",
  "agent-7": "bg-agent-7",
  "agent-8": "bg-agent-8",
  "agent-9": "bg-agent-9",
  "agent-10": "bg-agent-10",
  "agent-11": "bg-agent-11",
  "agent-12": "bg-agent-12",
  "agent-13": "bg-agent-13",
  "agent-14": "bg-agent-14",
  "agent-15": "bg-agent-15",
  "agent-16": "bg-agent-16",
  // 共享包的身份色板只有 --agent-1..16 和 --agent-foreground，**没有中性那一档**，
  // 而 `neutral` 是 AgentColor 的正式成员。真正的修法是往包里补一个
  // --agent-neutral（跨仓改动），在那之前这里只能写字面色。
  // eslint-disable-next-line no-restricted-syntax
  neutral: "bg-neutral-600",
};

export const agentTextColorClassNames: Record<AgentColor, string> = {
  "agent-1": "text-agent-1",
  "agent-2": "text-agent-2",
  "agent-3": "text-agent-3",
  "agent-4": "text-agent-4",
  "agent-5": "text-agent-5",
  "agent-6": "text-agent-6",
  "agent-7": "text-agent-7",
  "agent-8": "text-agent-8",
  "agent-9": "text-agent-9",
  "agent-10": "text-agent-10",
  "agent-11": "text-agent-11",
  "agent-12": "text-agent-12",
  "agent-13": "text-agent-13",
  "agent-14": "text-agent-14",
  "agent-15": "text-agent-15",
  "agent-16": "text-agent-16",
  neutral: "text-foreground",
};

export function agentTextColorClassName(
  token: string | null | undefined,
  fallback: AgentColor = "agent-1",
): string {
  if (!token) return agentTextColorClassNames[fallback];
  return (
    agentTextColorClassNames[token as AgentColor] ??
    agentTextColorClassNames[fallback]
  );
}
