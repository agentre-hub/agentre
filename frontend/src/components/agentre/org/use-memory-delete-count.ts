import * as React from "react";

import type { MemoryOwnerRef } from "@agentre-hub/agentre-ui";

import { MemoryCountOwned } from "../../../../wailsjs/go/app/App";

/**
 * 删除确认里「会一并删掉几条记忆」的数：owners 名下的全部条目（含已归档——级联删的
 * 就是这么多），由 MemoryCountOwned 数。
 *
 * 只在 `enabled`（确认框开着）时读，owners 变了（部门换删除策略）重读。读失败或还
 * 没读到都返回 null —— 确认框这时不提记忆，而不是说一句「没有记忆」去冒充读到的结果。
 */
export function useMemoryDeleteCount(
  owners: MemoryOwnerRef[],
  enabled: boolean,
): number | null {
  const [count, setCount] = React.useState<number | null>(null);
  // 只按内容重读：调用方每次渲染给的可能是新数组，内容没变就不该重读。
  const key = JSON.stringify(owners.map((o) => [o.kind, o.id]));

  React.useEffect(() => {
    // 关着，或 owners 变了：旧数不再作数（换了删除策略时，旧策略的条数不能留在确认框里）。
    setCount(null);
    if (!enabled) return;
    let alive = true;
    const wanted = (JSON.parse(key) as [MemoryOwnerRef["kind"], number][]).map(
      ([kind, id]) => ({ kind, id }),
    );
    void (async () => {
      try {
        const total = await MemoryCountOwned(wanted);
        if (alive) setCount(total ?? 0);
      } catch {
        if (alive) setCount(null);
      }
    })();
    return () => {
      alive = false;
    };
  }, [key, enabled]);

  return count;
}
