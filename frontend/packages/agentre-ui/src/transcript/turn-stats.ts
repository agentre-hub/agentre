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
  const completionApprox = estimated > 0;
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
    completionApprox,
    waitingFirstToken,
  };
}
