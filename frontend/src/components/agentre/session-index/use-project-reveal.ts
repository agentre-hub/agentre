import * as React from "react";
import {
  readSidebarExpanded,
  writeSidebarExpanded,
} from "@agentre-hub/agentre-ui";

import type { app } from "../../../../wailsjs/go/models";

/**
 * 组的展开过渡是 150ms（SessionGroup 的 grid 行高）。等它走完再滚：收着的那一截在
 * 走完之前不占高度，滚动容器根本够不到底。
 */
const EXPAND_SETTLE_MS = 200;
/** 亮多久。够看清「落在哪儿」，又不至于像一个选中态留在那儿。 */
const HIGHLIGHT_MS = 1800;
/** 树里一直没等到它（重取失败）就算了 —— 不留一个几分钟后才突然触发的亮相。 */
const GIVE_UP_MS = 5000;

/** 从根到 `projectID` 的祖先 id（不含它自己）。树里没有它返回 null。 */
function ancestorsOf(
  nodes: app.ProjectTreeNode[],
  projectID: number,
  trail: number[] = [],
): number[] | null {
  for (const node of nodes) {
    const id = node.project?.id ?? 0;
    if (id === projectID) return trail;
    const hit = ancestorsOf(node.children ?? [], projectID, [...trail, id]);
    if (hit) return hit;
  }
  return null;
}

type ProjectReveal = {
  /** 此刻亮着的那个项目；0 = 没有。 */
  revealedProjectID: number;
  /** 把这个项目亮出来。它可以还不在树里 —— 等树带上它再动。 */
  revealProject: (projectID: number) => void;
};

// useProjectReveal:把一个项目落到用户眼前 —— 祖先展开、滚到位、亮一下。
//
// 建完项目只重取一遍树是不够的:父项目收着时新建的子项目根本看不见,而用户刚做完
// 的事不该要他自己去找。组头靠 `data-project-id` 认,亮不亮由 `revealedProjectID`
// 递下去。
function useProjectReveal(tree: app.ProjectTreeNode[]): ProjectReveal {
  const [target, setTarget] = React.useState(0);
  // 祖先链压成字符串当依赖:树每轮校准都可能换引用,而链没变就不该把计时重来一遍。
  const chainKey = React.useMemo(() => {
    const chain = target > 0 ? ancestorsOf(tree, target) : null;
    return chain ? chain.join(",") : null;
  }, [tree, target]);
  const found = chainKey !== null;

  React.useEffect(() => {
    if (target <= 0) return;
    if (chainKey === null) {
      const giveUp = setTimeout(() => setTarget(0), GIVE_UP_MS);
      return () => clearTimeout(giveUp);
    }
    for (const id of chainKey ? chainKey.split(",") : []) {
      const key = `project:${id}`;
      // 只动明确收着的:没存过偏好的组本来就是展开的,不替用户写一条偏好。
      if (readSidebarExpanded(key) === false) writeSidebarExpanded(key, true);
    }
    const scroll = setTimeout(() => {
      const reduceMotion = window.matchMedia?.(
        "(prefers-reduced-motion: reduce)",
      ).matches;
      document.querySelector(`[data-project-id="${target}"]`)?.scrollIntoView({
        block: "nearest",
        behavior: reduceMotion ? "auto" : "smooth",
      });
    }, EXPAND_SETTLE_MS);
    const done = setTimeout(() => setTarget(0), HIGHLIGHT_MS);
    return () => {
      clearTimeout(scroll);
      clearTimeout(done);
    };
  }, [target, chainKey]);

  return { revealedProjectID: found ? target : 0, revealProject: setTarget };
}

export { useProjectReveal };
