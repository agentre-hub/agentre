// 空会话改选另一档时尚未拿到该档的模型绑定，不能借用 Agent 主档的值。
export function draftCLIModelBinding(
  agent: { agentBackendId?: number; agentCLIModel?: string } | null | undefined,
  overrideId: number | null,
): string | undefined {
  return overrideId && overrideId !== agent?.agentBackendId
    ? undefined
    : agent?.agentCLIModel;
}
