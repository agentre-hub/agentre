import { describe, expect, it } from "vitest";

import { buildAxisGroups } from "./axis-groups";
import type { IndexRow, ProjectNode } from "./axis-groups";
import { nestProjectGroups } from "./project-group-tree";

function row(key: string, projectSyncId: string): IndexRow {
  return {
    key,
    sessionId: 1,
    fingerprint: "fp",
    agentSyncId: "",
    projectSyncId,
    updatedAt: 1,
    title: key,
    lifecycleState: "idle",
  };
}

const projects: ProjectNode[] = [
  { syncId: "root", name: "Root", sortOrder: 0 },
  { syncId: "child", name: "Child", parentSyncId: "root", sortOrder: 0 },
  { syncId: "grand", name: "Grand", parentSyncId: "child", sortOrder: 0 },
  { syncId: "other", name: "Other", sortOrder: 1 },
];

describe("nestProjectGroups", () => {
  it("把项目轴的平铺深度列表还原成树：子项目挂在父组下，兜底组仍是根", () => {
    const groups = buildAxisGroups("project", {
      rows: [row("a", "root"), row("b", "grand"), row("c", "")],
      projects,
      agents: [],
      machines: [],
    });

    const tree = nestProjectGroups(groups);

    expect(tree.map((n) => n.group.key)).toEqual([
      "root",
      "other",
      "__unassigned_project__",
    ]);
    expect(tree[0].children.map((n) => n.group.key)).toEqual(["child"]);
    expect(tree[0].children[0].children.map((n) => n.group.key)).toEqual([
      "grand",
    ]);
    expect(tree[1].children).toEqual([]);
  });

  it("每个节点带上整棵子树的行：折叠父项目时气泡要冒后代的会话", () => {
    const groups = buildAxisGroups("project", {
      rows: [row("a", "root"), row("b", "grand")],
      projects,
      agents: [],
      machines: [],
    });

    const [root] = nestProjectGroups(groups);

    expect(root.subtreeRows.map((r) => r.key).sort()).toEqual(["a", "b"]);
    expect(root.children[0].subtreeRows.map((r) => r.key)).toEqual(["b"]);
  });

  it("非项目轴没有深度：每组都是没有子节点的根", () => {
    const groups = buildAxisGroups("agent", {
      rows: [row("a", "root")],
      projects,
      agents: [{ syncId: "x", name: "X" }],
      machines: [],
    });

    const tree = nestProjectGroups(groups);

    expect(tree.every((n) => n.children.length === 0)).toBe(true);
    expect(tree.map((n) => n.group.key)).toEqual(groups.map((g) => g.key));
  });
});
