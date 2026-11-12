import * as React from "react";

import { MemoryRestrictedTargets } from "../../../../wailsjs/go/app/App";
import type { memory_svc } from "../../../../wailsjs/go/models";

/**
 * agent 的哪些执行目标不带记忆或只能读摘要。这是页面上的附注信息：读取失败就当没有，
 * 不弹错、不挡住记忆本身。
 */
export function useRestrictedTargets(
  agentId: number | null,
): memory_svc.RestrictedTarget[] {
  const [targets, setTargets] = React.useState<memory_svc.RestrictedTarget[]>(
    [],
  );
  React.useEffect(() => {
    if (agentId === null) return;
    let cancelled = false;
    MemoryRestrictedTargets(agentId)
      .then((res) => {
        if (!cancelled) setTargets(res ?? []);
      })
      .catch(() => {
        if (!cancelled) setTargets([]);
      });
    return () => {
      cancelled = true;
    };
  }, [agentId]);
  return targets;
}
