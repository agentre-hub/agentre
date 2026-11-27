import { failureDetailText } from "../transcript/failure-detail";

/**
 * 写操作超时：请求发出去了，服务端可能已经落库。认法：错误名带 timeout，或机器原话里有
 * "timed out / timed-out / timeout"。全包只有这一份——看板 / 项目 / 标签的新建与审批 /
 * 提问的提交认的是同一件事。原话按 `failureDetailText` 取（Error、裸字符串、带 message
 * 等字段的对象），与卡片上折叠的「详情」同一个来源。
 */
export function isTimeoutFailure(cause: unknown): boolean {
  if (cause instanceof Error && /timeout/i.test(cause.name)) return true;
  return /timed?[ -]?out/i.test(failureDetailText(cause));
}
