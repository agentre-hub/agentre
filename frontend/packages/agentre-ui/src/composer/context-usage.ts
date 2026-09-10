/**
 * Composer 底栏「上下文用量」的取值规则 —— 两个宿主共用这一份。
 *
 * 前端不做 provider/backend family-specific 聚合：runtime translator 在每条
 * usage / 消息上自报 `totalInputTokens`（按 family 算好的总输入），前端直接读。
 * 三条规则：
 *   - `contextWindow <= 0` 返回 `undefined`（整块不渲染）；
 *   - `liveUsage.totalInputTokens > 0` 优先（turn 进行中阶梯式刷新）；
 *   - 否则从尾部往前找首条 `totalInputTokens > 0` 的 assistant 消息；
 *   - 都没有就是 `{ used: 0, max }`，渲染 0/max 的占位进度条。
 *
 * `liveUsage` 只有桌面端有（浏览器宿主的会话镜像没有流式 usage 这条通道），
 * 所以它是可选参数：不传就等价于只扫消息，也就是 server 那份原本的行为。
 */

export type ContextUsageMessage = {
  role: string;
  totalInputTokens?: number;
};

export type ContextUsageLive = {
  totalInputTokens?: number;
};

export type ContextUsage = { used: number; max: number };

export function computeContextUsage(
  messages: readonly ContextUsageMessage[],
  contextWindow: number,
  liveUsage?: ContextUsageLive | null,
): ContextUsage | undefined {
  if (contextWindow <= 0) return undefined;

  if (liveUsage?.totalInputTokens && liveUsage.totalInputTokens > 0) {
    return { used: liveUsage.totalInputTokens, max: contextWindow };
  }

  let used = 0;
  for (let index = messages.length - 1; index >= 0; index--) {
    const message = messages[index];
    if (message.role !== "assistant") continue;
    if (message.totalInputTokens && message.totalInputTokens > 0) {
      used = message.totalInputTokens;
      break;
    }
  }

  return { used, max: contextWindow };
}
