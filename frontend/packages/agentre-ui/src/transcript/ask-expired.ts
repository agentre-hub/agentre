import { failureDetailText } from "./failure-detail";

/**
 * 运行时表示「按 requestID 找不到等待中的请求」的信号文本：提问已被回答过、被新消息
 * 跳过，或会话已结束。与后端哨兵 `agentruntime.ErrWaiterNotFound`
 * （internal/pkg/agentruntime/errors.go）的文本逐字对齐——各 runtime 都用 `%w` 包它，
 * 所以这段文本总在错误串里。
 */
const ASK_EXPIRED_SIGNALS: readonly string[] = [
  "no waiting request for requestID",
];

/**
 * 提交回答失败的原因是不是「提问已不存在」。只有这一种失败该锁卡；认不出的一律
 * 当成可重试的失败。
 */
export function isAskExpiredError(e: unknown): boolean {
  const text = failureDetailText(e);
  return ASK_EXPIRED_SIGNALS.some((signal) => text.includes(signal));
}
