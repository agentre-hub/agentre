/**
 * 写操作超时：请求发出去了，服务端可能已经落库。认法：错误名带 timeout，或消息里有
 * "timed out / timed-out / timeout"（与审批停靠条同一条规则）。
 */
export function isTimeoutFailure(cause: unknown): boolean {
  if (cause instanceof Error && /timeout/i.test(cause.name)) return true;
  const message = cause instanceof Error ? cause.message : String(cause);
  return /timed?[ -]?out/i.test(message);
}
