import * as React from "react";
import { useInRouterContext, useNavigate } from "react-router-dom";

import type { MemoryOwnerRef } from "@agentre-hub/agentre-ui";

import { MemoryCountsByOwner } from "../../../../wailsjs/go/app/App";
import { memoryOwnerPath } from "@/lib/app-routing";

export type MemoryOwnerEntry = { count: number; open: () => void };

/**
 * 某个归属的「记忆 N 条」入口：数字取自 MemoryCountsByOwner，`open` 带着归属跳到
 * 记忆页。返回 null 表示这一行不该画 —— 还在读、读失败，或不在路由里（没处可跳）。
 *
 * 读失败只是少一行：它是详情页上的一个附属入口，不能因为它拖垮宿主页面，也不该
 * 画一个「0 条」去冒充读到的结果。没有这个归属的计数行就是 0（后端只列有条目的归属）。
 */
export function useMemoryOwnerEntry(
  owner: MemoryOwnerRef,
): MemoryOwnerEntry | null {
  const inRouter = useInRouterContext();
  // 路由上下文在一个组件的整个生命期里不会变，条件调用不会改变 hook 次序。
  // eslint-disable-next-line react-hooks/rules-of-hooks
  const navigate = inRouter ? useNavigate() : null;
  const [count, setCount] = React.useState<number | null>(null);
  const { kind, id } = owner;

  React.useEffect(() => {
    // 换了归属：上一个归属的数不能顶着新归属显示。
    setCount(null);
    // 还没有归属（没打开的项目设置弹窗给 id 0）：没什么可读的。
    if (kind !== "global" && id <= 0) return;
    let alive = true;
    void (async () => {
      try {
        const rows = await MemoryCountsByOwner();
        if (!alive) return;
        const hit = (rows ?? []).find(
          (r) => r.owner.kind === kind && r.owner.id === id,
        );
        setCount(hit?.count ?? 0);
      } catch {
        if (alive) setCount(null);
      }
    })();
    return () => {
      alive = false;
    };
  }, [kind, id]);

  if (count === null || !navigate) return null;
  return { count, open: () => navigate(memoryOwnerPath({ kind, id })) };
}
