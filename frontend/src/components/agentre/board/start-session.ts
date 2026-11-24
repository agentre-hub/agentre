import type { TaskFormValue } from "@agentre-hub/agentre-ui";

import type { NewSessionSeed } from "@/stores/chat-tabs-store";

/** 开始会话要的 Agent 信息：只认 id 与能不能对话。 */
type StartAgent = { id: number; chattable?: boolean };

export type StartSessionPlan = {
  projectId: number;
  agentId: number;
  seed: NewSessionSeed;
};

/** 输入框预填：任务标题，有描述时空一行再接描述。 */
export function startSessionText(task: TaskFormValue): string {
  const body = task.description.trim();
  return body ? `${task.title}\n\n${body}` : task.title;
}

/**
 * 「开始会话」要打开的新建会话：项目取任务所属项目，Agent / 后端 / 模型取任务上指定
 * 的值；没指定（或指定的 Agent 已不可对话）的维度走新建会话的默认——Agent 先取上次
 * 用过的，再取第一个可对话的。没有任何可对话的 Agent 时返回 null。
 */
export function planStartSession(
  task: TaskFormValue,
  agents: readonly StartAgent[],
  lastAgentId: number | null,
): StartSessionPlan | null {
  const chattable = agents.filter((agent) => agent.chattable !== false);
  const assigned = chattable.find((agent) => agent.id === task.assigneeAgentId);
  const fallback =
    chattable.find((agent) => agent.id === lastAgentId) ?? chattable[0];
  const agent = assigned ?? fallback;
  if (!agent) return null;

  const seed: NewSessionSeed = {
    text: startSessionText(task),
    issueId: task.id,
  };
  // 后端与模型是指定给那个 Agent 的：换了 Agent 就不再成立。
  if (assigned) {
    if (task.agentBackendId) seed.backendId = task.agentBackendId;
    if (task.llmProviderKey) {
      seed.providerKey = task.llmProviderKey;
      seed.modelKey = task.llmModelKey;
    }
  }
  return { projectId: task.projectId ?? 0, agentId: agent.id, seed };
}
