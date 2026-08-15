// Shared props for canonical card components. Kept in a separate file so card
// modules can import it without pulling in the registry (which itself imports
// every card — would cause a circular import otherwise).

import type { TranscriptBlock } from "@agentre-ai/agentre-ui";

export type PlanActionStream = {
  sessionId: number;
  userMessageId: number;
  assistantMessageId: number;
  stream: string;
};

export type AgentSpawnChildBlocks = {
  all: TranscriptBlock[];
  byRun: ReadonlyMap<string, TranscriptBlock[]>;
};

export type CanonicalCardProps = {
  toolBlock: TranscriptBlock;
  resultBlock?: TranscriptBlock;
  cwd?: string;
  sessionId?: number;
  /** 本卡所属的 assistant 消息 id —— 审批类卡片做乐观更新时用它定位对应的那条 LiveStream。 */
  messageId?: number;
  onPlanActionStarted?: (stream: PlanActionStream, userText: string) => void;
  // onStopSubagent 停掉这张卡对应的正在运行的子 agent / 后台任务(下发 CLI stop_task)。
  // 入参是发起它的 tool_use_id(= toolBlock.toolUseId);仅 backend 支持时由 chat-panel 传入。
  onStopSubagent?: (toolUseId: string) => void;
  /** Stable key for transcript-local UI state that must survive virtualization unmounts. */
  uiStateKey?: string;
  /** Stable mounted chat tab key for UI drafts that must survive route/tab remounts. */
  tabStateKey?: string;
  // childBlocks 仅 agent.spawn 用 — 父工具下的子调用先按 parentToolUseId,
  // 再按 subagentRunId 分组；all 保留缺失或未知 run id 的 unmatched 子块。
  childBlocks?: AgentSpawnChildBlocks;
};
