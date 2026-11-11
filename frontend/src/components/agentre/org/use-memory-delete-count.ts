import * as React from "react";

import type { MemoryOwnerRef } from "@agentre-hub/agentre-ui";

import { MemoryCountsByOwner } from "../../../../wailsjs/go/app/App";

/**
 * 删除确认里「会一并删掉几条记忆」的数：把 owners 各自的条数加起来。
 *
 * 只在 `enabled`（确认框开着）时读，owners 变了（部门换删除策略）重读。读失败或还
 * 没读到都返回 null —— 确认框这时不提记忆，而不是说一句「没有记忆」去冒充读到的结果。
 * 计数口径沿用 MemoryCountsByOwner（只数正常条目，已归档的不在内）。
 */
export function useMemoryDeleteCount(
  owners: MemoryOwnerRef[],
  enabled: boolean,
): number | null {
  const [count, setCount] = React.useState<number | null>(null);
  const key = owners.map((o) => `${o.kind}:${o.id}`).join(",");

  React.useEffect(() => {
    if (!enabled) {
      setCount(null);
      return;
    }
    let alive = true;
    const wanted = new Set(key === "" ? [] : key.split(","));
    void (async () => {
      try {
        const rows = await MemoryCountsByOwner();
        if (!alive) return;
        setCount(
          (rows ?? [])
            .filter((r) => wanted.has(`${r.owner.kind}:${r.owner.id}`))
            .reduce((sum, r) => sum + r.count, 0),
        );
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
