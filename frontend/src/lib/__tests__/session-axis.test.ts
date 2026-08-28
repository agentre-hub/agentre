import { describe, expect, it } from "vitest";

import { flattenProjectTree } from "@/lib/session-axis";
import type { app } from "../../../wailsjs/go/models";

/**
 * 「按项目」轴的组顺序 —— 把后端给的项目树摊平成深度优先的 id + 缩进层级。
 *
 * 会话的分桶不在这一层：三个轴各有自己的分页查询，会话拿回来时已经分好组了。
 */

function node(
  id: number,
  children: app.ProjectTreeNode[] = [],
): app.ProjectTreeNode {
  return {
    project: { id, name: `p${id}` },
    children,
  } as unknown as app.ProjectTreeNode;
}

describe("flattenProjectTree", () => {
  it("Given a nested project tree, When it is flattened, Then it is depth-first with the indent level of each node", () => {
    const tree = [node(1, [node(2), node(3, [node(4)])]), node(5)];

    expect(flattenProjectTree(tree)).toEqual([
      { id: 1, depth: 0 },
      { id: 2, depth: 1 },
      { id: 3, depth: 1 },
      { id: 4, depth: 2 },
      { id: 5, depth: 0 },
    ]);
  });

  it("Given a node without a usable id, When the tree is flattened, Then it is skipped instead of emitting id 0", () => {
    const tree = [{ children: [] } as unknown as app.ProjectTreeNode, node(7)];

    expect(flattenProjectTree(tree)).toEqual([{ id: 7, depth: 0 }]);
  });
});
