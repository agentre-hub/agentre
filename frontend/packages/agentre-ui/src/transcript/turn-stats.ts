export type LiveTurnInput = {
  startedAt: number;
  firstTokenAt: number | null;
  generationMs: number;
  burstStartedAt: number | null;
  promptTokens: number;
  completionTokens: number;
  cachedTokens: number;
  cacheCreationTokens: number;
  reasoningTokens: number;
  model: string;
  liveText: string;
};

export type LiveTurnStats = {
  model: string;
  promptTokens: number;
  completionTokens: number;
  cachedTokens: number;
  cacheCreationTokens: number;
  reasoningTokens: number;
  durationMs: number;
  firstTokenMs: number;
  tokensPerSec: number;
  completionApprox: boolean;
  waitingFirstToken: boolean;
};

export function estimateCompletionTokens(text: string): number {
  if (!text) return 0;
  return Math.max(1, Math.ceil(text.length / 4));
}

export function computeLiveTurnStats(
  input: LiveTurnInput & { now: number },
): LiveTurnStats {
  const durationMs = Math.max(0, input.now - input.startedAt);
  const firstTokenAt = input.firstTokenAt;
  const waitingFirstToken = firstTokenAt == null;
  const firstTokenMs = waitingFirstToken
    ? durationMs
    : Math.max(0, firstTokenAt - input.startedAt);
  const estimated = estimateCompletionTokens(input.liveText);
  const completionTokens = input.completionTokens + estimated;
  let generationMs = input.generationMs;
  if (input.burstStartedAt != null) {
    generationMs += Math.max(0, input.now - input.burstStartedAt);
  }
  const tokensPerSec =
    !waitingFirstToken && completionTokens > 0 && generationMs > 0
      ? completionTokens / (generationMs / 1000)
      : 0;
  return {
    model: input.model,
    promptTokens: input.promptTokens,
    completionTokens,
    cachedTokens: input.cachedTokens,
    cacheCreationTokens: input.cacheCreationTokens,
    reasoningTokens: input.reasoningTokens,
    durationMs,
    firstTokenMs,
    tokensPerSec,
    // 这个函数只在一轮还活着时被调用(终态消息走的是终态帧上的定值),所以这里
    // 恒为 true:计数与 tok/s 都还会再涨,不是终值。曾经按「此刻有没有未统计的
    // 可见文字」判断,结果 `~` 会在工具执行的空档里闪掉,把中间值说成了终值。
    completionApprox: true,
    waitingFirstToken,
  };
}
