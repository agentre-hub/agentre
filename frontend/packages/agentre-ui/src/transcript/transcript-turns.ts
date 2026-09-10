// transcript-turns 是「什么算一轮」的唯一一份判定。
//
// 界面上有两个地方要认轮:转录区给自主续轮挂 banner(autonomousIds),以及「回到底部」
// 药丸报「下面还有 N 轮」。两处若各拼各的判定,迟早在自主续轮或旁白行这类边角上分家 ——
// 与 chat-panel.tsx 里「数出来的行与画出来的行必须同源」是同一条理由。
//
// 判据:
//   - user 消息开一轮;
//   - assistant 消息只有在它紧邻的前一条**不是** user 时才开一轮 —— 正常轮是
//     user→assistant,auto-continue / steer 也是 user→assistant,只有后台任务完成后
//     CLI 自己跑的那一轮是 assistant→assistant(没有 user 行);
//   - 会话首条 assistant 永不算自主续轮(没有"前一条"可比)。
//
// 只承载供应商切换 notice 的旁白行在这条判定里**完全透明**:它自己不开轮,也不推进
// 「前一条是谁」—— 否则它垫在两条真实 assistant 之间就会把自主续轮判定拆断,或者反过来
// 让回退提示被误判成自主续轮。判据见 generating-indicator.ts 的 isNoticeOnlyMessage。

import type { TranscriptMessage } from "./dto";
import { isNoticeOnlyMessage } from "./generating-indicator";

/** 判轮只需要这三个字段;调用方的消息类型比它宽是允许的。 */
export type TurnMessage = Pick<TranscriptMessage, "id" | "role" | "blocks">;

/**
 * walkTurnStarts 按顺序回调每一条「开了一轮」的消息下标。
 * 两个导出函数的差别只在收集什么,遍历规则必须是同一份。
 */
function walkTurnStarts(
  messages: readonly TurnMessage[],
  visit: (index: number, autonomous: boolean) => void,
): void {
  let prevRole: string | undefined;
  for (let i = 0; i < messages.length; i++) {
    const m = messages[i];
    if (isNoticeOnlyMessage(m)) continue;
    if (m.role === "user") {
      visit(i, false);
    } else if (
      prevRole !== undefined &&
      m.role === "assistant" &&
      prevRole !== "user"
    ) {
      visit(i, true);
    }
    prevRole = m.role;
  }
}

/** 自主续轮(没有 user 行的那种轮)的首条 assistant 消息 id。 */
export function autonomousTurnMessageIds(
  messages: readonly TurnMessage[],
): Set<number> {
  const ids = new Set<number>();
  walkTurnStarts(messages, (index, autonomous) => {
    if (autonomous) ids.add(messages[index].id);
  });
  return ids;
}

/**
 * countTurnsAfterMessage 数「afterMessageId 那条消息之后还开了几轮」。
 *
 * afterMessageId 是视口下沿那条消息。数不出边界(null)、或它已经不在列表里(重载/
 * 删除之后)时一律返回 0 —— 宁可少说一句,也不猜一个够不着的数字。
 *
 * 不需要考虑压缩折叠:折叠只砍掉**前缀**(压缩边界之前的旧消息),被折叠的永远在边界
 * 上方,不落进计数范围。
 */
export function countTurnsAfterMessage(
  messages: readonly TurnMessage[],
  afterMessageId: number | null,
): number {
  if (afterMessageId === null) return 0;
  const boundary = messages.findIndex((m) => m.id === afterMessageId);
  if (boundary < 0) return 0;
  let count = 0;
  walkTurnStarts(messages, (index) => {
    if (index > boundary) count++;
  });
  return count;
}

/**
 * computeBottomVisibleMessageId 找滚动容器里**视口下沿**那条消息 —— 即最后一条顶边
 * 仍在视口内的 `[data-message-id]` 行所属的消息。`countTurnsAfterMessage` 的边界就
 * 是它:两者是同一件事(「读者落后多少」)的两半,所以住在一起。
 *
 * 为什么不是视口**顶**那条:那样用户正看着的这一屏也会被算进「下面还有」,数字恒偏大。
 * (视口顶那条另有用处 —— 宿主的滚动位置恢复锚点,那是宿主自己的事。)
 *
 * 行上的 `data-message-id` 由宿主的行外层挂,两端都挂。虚拟列表的 overscan 会把视口
 * 下方的行也渲进 DOM,所以必须按几何筛,不能取末行;DOM 顺序 ≈ 消息顺序,故一旦遇到
 * 顶边已越过下沿的行就可以停。
 *
 * 找不到(无消息行 / 容器未布局,rect 全是 0)返回 null —— 调用方据此退回「回到底部」,
 * 不猜一个够不着的数字。
 */
export function computeBottomVisibleMessageId(el: HTMLElement): number | null {
  const containerBottom = el.getBoundingClientRect().bottom;
  const rows = el.querySelectorAll<HTMLElement>("[data-message-id]");
  let found: number | null = null;
  for (const row of rows) {
    if (row.getBoundingClientRect().top >= containerBottom) break;
    const id = Number(row.getAttribute("data-message-id"));
    if (Number.isFinite(id)) found = id;
  }
  return found;
}
