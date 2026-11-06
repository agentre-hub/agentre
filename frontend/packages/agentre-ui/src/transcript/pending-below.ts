// pending-below:「回到底部」药丸的「N 项待处理」——视口下沿之后仍未决的审批 / 提问个数。
// 宿主把已合并好的消息(持久化块 + 在流 liveBlocks)交进来,规则只在这里一份。
import type { TranscriptBlock } from "./dto";

function isResolvedAsk(ask: {
  answered?: boolean;
  skipped?: boolean;
  expired?: boolean;
}): boolean {
  return !!(ask.answered || ask.skipped || ask.expired);
}

// 一个块若是仍未回答的审批 / 提问,返回它的 requestId,否则 null。
// 覆盖 canonical 与旧形态两套载荷;与卡片自己的「已决」判据同口径。
export function pendingDecisionId(block: TranscriptBlock): string | null {
  const c = block.canonical;
  if (c?.kind === "tool.permission" && c.toolPermission) {
    return c.toolPermission.resolved ? null : c.toolPermission.requestId;
  }
  if (c?.kind === "user.ask" && c.userAsk) {
    return isResolvedAsk(c.userAsk) ? null : c.userAsk.requestId;
  }
  const p = block.toolPermission;
  if (p) return p.resolved ? null : p.requestId;
  const q = block.askUserQuestion;
  if (q) return isResolvedAsk(q) ? null : q.requestId;
  const a = block.toolApproval;
  if (a) return a.status === "pending" ? a.requestId : null;
  const e = block.execApproval;
  if (e) return e.status === "pending" ? e.id : null;
  return null;
}

// id 大于视口下沿那条消息的消息里,仍未决的审批 / 提问个数,按 requestId 去重
// (同一张卡可能在持久化块与在流块里各出现一次,宿主可传重复 id 的消息)。
// 视口下沿未知(null)时数不出「下面」,记 0 —— 与 turnsBelow 同口径。
export function countPendingBelow(
  messages: ReadonlyArray<{
    id: number;
    blocks?: ReadonlyArray<TranscriptBlock>;
  }>,
  bottomVisibleId: number | null,
): number {
  if (bottomVisibleId === null) return 0;
  const pending = new Set<string>();
  for (const m of messages) {
    if (m.id <= bottomVisibleId) continue;
    for (const b of m.blocks ?? []) {
      const id = pendingDecisionId(b);
      if (id) pending.add(id);
    }
  }
  return pending.size;
}
