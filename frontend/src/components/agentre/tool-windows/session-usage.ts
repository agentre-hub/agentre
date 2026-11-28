import type { chat_svc } from "../../../../wailsjs/go/models";

export type SessionUsageTotals = {
  input: number;
  output: number;
  cacheHits: number;
};

/**
 * 本会话各条消息**已记录**用量之和：输入、输出、缓存命中。
 *
 * 只读消息自己的记录列：子代理的用量住在它们的卡片里（块内），不进这里。各后端的
 * 统计口径不同（Anthropic 的输入不含缓存，OpenAI 的含），所以这是「记录之和」而不是
 * 一个跨后端可比的账单数。没有任何记录时返回 null —— 调用方据此不画这一行。
 */
export function sumSessionUsage(
  messages: readonly chat_svc.ChatMessage[],
): SessionUsageTotals | null {
  let input = 0;
  let output = 0;
  let cacheHits = 0;
  for (const m of messages) {
    input += m.promptTokens ?? 0;
    output += m.completionTokens ?? 0;
    cacheHits += m.cachedTokens ?? 0;
  }
  if (input === 0 && output === 0 && cacheHits === 0) return null;
  return { input, output, cacheHits };
}
